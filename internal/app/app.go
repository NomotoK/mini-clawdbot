package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"mini-clawdbot/internal/agent"
	"mini-clawdbot/internal/bus"
	"mini-clawdbot/internal/channels"
	"mini-clawdbot/internal/config"
	"mini-clawdbot/internal/cron"
	"mini-clawdbot/internal/gateway"
	"mini-clawdbot/internal/llm"
	"mini-clawdbot/internal/router"
	"mini-clawdbot/internal/service"
	"mini-clawdbot/internal/session"
	"mini-clawdbot/internal/tools"
	"mini-clawdbot/internal/toolruntime"
)

// Options 描述应用初始化时的可选参数。
type Options struct {
	// MaxStep 为 ReAct 最大步数。<=0 时在下游回退到默认值。
	MaxStep int
	// ModelOverride 非空时覆盖环境变量中的 MINI_CLAW_MODEL。
	ModelOverride string
}

// App 聚合并管理 MVP 运行所需依赖：
//
// - runner: ReAct 执行器
// - session: 内存消息会话（当前仅单轮，仍保留扩展位）
type App struct {
	runner       *agent.ReactRunner
	session      *session.MemorySession
	bus          *bus.MessageBus
	channelMgr   *channels.ChannelManager
	agentMgr     *agent.AgentManager
	toolRuntime  *toolruntime.Runtime
	cronSvc      *cron.Service
	gateway      *gateway.Server
	gatewayOn    bool
	supervisor   *service.Supervisor
	sessionStore session.Store
	cfg          config.Config
}

// New 通过“.env + 环境变量”初始化完整应用依赖。
//
// 参数：
// - ctx: 初始化上下文
// - opts: 运行参数（maxStep/model override）
//
// 返回：
// - *App: 可执行 RunOnce 的应用实例
// - error: .env 读取、配置加载、模型初始化或工具构建失败时返回
func New(ctx context.Context, opts Options) (*App, error) {
	// 当前工作目录会作为工具执行目录，同时用于向上定位项目根目录。
	wd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}

	// 自动加载项目根目录 .env（若不存在则忽略）。
	// 若未找到项目根目录，则退化为在当前工作目录查找 .env。
	root, rootErr := ResolveProjectRoot(wd)
	dotenvPath := filepath.Join(wd, ".env")
	if rootErr == nil {
		dotenvPath = filepath.Join(root, ".env")
	}
	if err := config.LoadDotEnvIfExists(dotenvPath); err != nil {
		return nil, err
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		return nil, err
	}
	cfg = config.WithModelOverride(cfg, opts.ModelOverride)

	chatModel, err := llm.NewToolCallingModel(ctx, cfg)
	if err != nil {
		return nil, err
	}

	a, err := newWithDependenciesAndService(ctx, chatModel, opts.MaxStep, wd, cfg.Service)
	if err != nil {
		return nil, err
	}
	a.cfg = cfg
	return a, nil
}

// NewWithDependencies 使用外部注入依赖创建 App。
//
// 该构造函数主要用于测试与组合场景，避免强依赖环境变量和真实模型。
//
// 参数：
// - ctx: 初始化上下文
// - chatModel: 注入的 tool-calling 模型
// - maxStep: ReAct 最大步数
// - workingDir: 工具执行目录（不能为空）
//
// 返回：
// - *App: 应用实例
// - error: 参数非法或依赖构建失败时返回
func NewWithDependencies(ctx context.Context, chatModel model.ToolCallingChatModel, maxStep int, workingDir string) (*App, error) {
	return newWithDependenciesAndService(ctx, chatModel, maxStep, workingDir, config.ServiceConfig{})
}

func newWithDependenciesAndService(ctx context.Context, chatModel model.ToolCallingChatModel, maxStep int, workingDir string, svcCfg config.ServiceConfig) (*App, error) {
	if strings.TrimSpace(workingDir) == "" {
		return nil, fmt.Errorf("working directory is required")
	}

	messageBus := bus.NewMessageBus(bus.Config{})
	store, err := session.NewJSONLStore(session.JSONLStoreConfig{RootDir: workingDir})
	if err != nil {
		return nil, fmt.Errorf("init session jsonl store: %w", err)
	}
	auditSink := &compositeAuditSink{store: store, bus: messageBus}
	toolRuntime, err := toolruntime.BuildDefaultRuntime(toolruntime.DefaultRuntimeConfig{
		WorkingDir:   workingDir,
		EnableDocker: svcCfg.Tools.EnableDocker,
	}, auditSink)
	if err != nil {
		return nil, fmt.Errorf("init tool runtime: %w", err)
	}
	toolList, toolRuntime, err := tools.BuildMVPTools(workingDir, toolRuntime)
	if err != nil {
		return nil, err
	}

	runner, err := agent.NewReactRunner(ctx, chatModel, toolList, maxStep)
	if err != nil {
		return nil, err
	}
	sessionRouter := router.NewSessionRouter()
	channelMgr := channels.NewChannelManager(messageBus)
	agentMgr := agent.NewAgentManager(
		messageBus,
		sessionRouter,
		runner,
		store,
		agent.ManagerConfig{},
	)
	cronSvc, err := cron.NewService(cron.Config{
		RootDir:      workingDir,
		PollInterval: time.Second,
	}, messageBus, toolRuntime)
	if err != nil {
		return nil, fmt.Errorf("init cron service: %w", err)
	}
	var gw *gateway.Server
	gatewayOn := svcCfg.Gateway.Enabled
	if gatewayOn {
		gwCfg := gateway.Config{
			Host:      svcCfg.Gateway.Host,
			Port:      svcCfg.Gateway.Port,
			AuthToken: svcCfg.Gateway.AuthToken,
		}
		gw = gateway.NewServer(gwCfg, messageBus, store, channelMgr, cronSvc)
	}

	app := &App{
		runner:       runner,
		session:      session.NewMemorySession(),
		bus:          messageBus,
		channelMgr:   channelMgr,
		agentMgr:     agentMgr,
		toolRuntime:  toolRuntime,
		cronSvc:      cronSvc,
		gateway:      gw,
		gatewayOn:    gatewayOn,
		sessionStore: store,
	}
	app.supervisor = app.buildSupervisor()
	return app, nil
}

// RegisterConfiguredChannels 根据当前配置注册内置渠道适配器。
//
// 返回：
// - int: 成功注册的渠道数量
// - error: 构建或注册任一渠道失败时返回错误
func (a *App) RegisterConfiguredChannels() (int, error) {
	registered := 0
	if a.cfg.Feishu.Enabled {
		wsClient, err := channels.NewFeishuLiveWSClient(channels.FeishuLiveConfig{
			AppID:             a.cfg.Feishu.AppID,
			AppSecret:         a.cfg.Feishu.AppSecret,
			VerificationToken: a.cfg.Feishu.VerificationToken,
			EncryptKey:        a.cfg.Feishu.EncryptKey,
		})
		if err != nil {
			return registered, fmt.Errorf("build feishu ws client: %w", err)
		}

		sender, err := channels.NewFeishuLiveSender(a.cfg.Feishu.AppID, a.cfg.Feishu.AppSecret)
		if err != nil {
			return registered, fmt.Errorf("build feishu sender: %w", err)
		}

		if err := a.RegisterFeishuChannel(a.cfg.Feishu.AccountID, wsClient, sender); err != nil {
			return registered, fmt.Errorf("register feishu channel: %w", err)
		}
		registered++
	}
	return registered, nil
}

// RunOnce 执行一次用户请求并返回最终文本。
//
// 行为说明：
// 1. 将 user 输入写入内存会话
// 2. 把当前会话消息传入 ReAct 执行
// 3. 记录最终消息并返回可展示文本
//
// 参数：
// - ctx: 执行上下文
// - userInput: 用户原始文本
//
// 返回：
// - string: 最终输出文本
// - error: 输入为空或 ReAct 执行失败时返回
func (a *App) RunOnce(ctx context.Context, userInput string) (string, error) {
	trimmed := strings.TrimSpace(userInput)
	if trimmed == "" {
		return "", fmt.Errorf("user input is empty")
	}

	// 先记录用户消息，再执行模型，保证会话历史完整。
	a.session.Add(schema.UserMessage(trimmed))

	finalMsg, err := a.runner.Run(ctx, a.session.Messages())
	if err != nil {
		return "", err
	}
	a.session.Add(finalMsg)

	// MVP 以文本输出为主；若 content 非空直接返回。
	if strings.TrimSpace(finalMsg.Content) != "" {
		return finalMsg.Content, nil
	}

	// 非文本或空文本场景的保底输出，避免 CLI 无输出。
	return fmt.Sprintf("role=%s content=%q", finalMsg.Role, finalMsg.Content), nil
}

// Serve 启动常驻服务链路（ChannelManager + AgentManager）。
func (a *App) Serve(ctx context.Context) error {
	if a.channelMgr == nil || a.agentMgr == nil || a.bus == nil || a.sessionStore == nil || a.supervisor == nil {
		return errors.New("app serve dependencies are not ready")
	}
	return a.supervisor.Run(ctx)
}

// Bus 返回应用总线实例，用于外部注入或测试。
func (a *App) Bus() *bus.MessageBus {
	return a.bus
}

// Supervisor 返回服务监督器。
func (a *App) Supervisor() *service.Supervisor {
	return a.supervisor
}

// Gateway 返回网关服务实例。
func (a *App) Gateway() *gateway.Server {
	return a.gateway
}

// CronService 返回 cron 服务实例。
func (a *App) CronService() *cron.Service {
	return a.cronSvc
}

// ChannelManager 返回渠道管理器，用于注册 adapter。
func (a *App) ChannelManager() *channels.ChannelManager {
	return a.channelMgr
}

// RegisterChannel 注册任意渠道适配器。
func (a *App) RegisterChannel(channel, accountID string, adapter channels.ChannelAdapter) error {
	if a.channelMgr == nil {
		return errors.New("channel manager is not ready")
	}
	return a.channelMgr.Register(channel, accountID, adapter)
}

// RegisterTelegramChannel 以顶层封装方式注册 Telegram 渠道。
func (a *App) RegisterTelegramChannel(accountID string, poller channels.TelegramPoller, sender channels.TelegramSender) error {
	adapter := channels.NewTelegramAdapter(accountID, poller, sender)
	return a.RegisterChannel(adapter.Name(), accountID, adapter)
}

// RegisterFeishuChannel 以顶层封装方式注册飞书渠道。
func (a *App) RegisterFeishuChannel(accountID string, wsClient channels.FeishuWSClient, sender channels.FeishuSender) error {
	adapter := channels.NewFeishuAdapter(accountID, wsClient, sender)
	return a.RegisterChannel(adapter.Name(), accountID, adapter)
}

// ResolveProjectRoot 从起始目录向上查找最近的 go.mod 所在目录。
//
// 参数：
// - start: 起始路径（可相对可绝对）
//
// 返回：
// - string: 找到的项目根目录
// - error: 向上遍历到文件系统根仍未找到 go.mod 时返回
func ResolveProjectRoot(start string) (string, error) {
	cur, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve abs path: %w", err)
	}

	for {
		candidate := filepath.Join(cur, "go.mod")
		if _, err := os.Stat(candidate); err == nil {
			return cur, nil
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("go.mod not found from %s upward", start)
		}
		cur = parent
	}
}

func (a *App) buildSupervisor() *service.Supervisor {
	components := []service.NamedComponent{
		{
			Name: "bus",
			Component: service.FuncComponent{
				NameValue: "bus",
				StopFn: func(context.Context) error {
					return a.bus.Close()
				},
				HealthFn: func(context.Context) service.HealthStatus {
					if a.bus == nil || a.bus.IsClosed() {
						return service.HealthStatus{Name: "bus", Status: service.StatusStopped}
					}
					return service.HealthStatus{Name: "bus", Status: service.StatusReady}
				},
			},
		},
		{
			Name: "session_store",
			Component: service.FuncComponent{
				NameValue: "session_store",
				InitFn: func(ctx context.Context) error {
					return a.sessionStore.Recover(ctx)
				},
				HealthFn: func(context.Context) service.HealthStatus {
					return service.HealthStatus{Name: "session_store", Status: service.StatusReady}
				},
			},
		},
		{
			Name: "tool_runtime",
			Component: service.FuncComponent{
				NameValue: "tool_runtime",
				HealthFn: func(context.Context) service.HealthStatus {
					if a.toolRuntime == nil {
						return service.HealthStatus{Name: "tool_runtime", Status: service.StatusStopped}
					}
					return service.HealthStatus{
						Name:   "tool_runtime",
						Status: service.StatusReady,
						Details: map[string]any{
							"tool_count": len(a.toolRuntime.List()),
						},
					}
				},
			},
		},
		{
			Name: "agent_manager",
			Component: service.FuncComponent{
				NameValue: "agent_manager",
				StartFn:   a.agentMgr.Start,
				StopFn:    a.agentMgr.Stop,
				HealthFn: func(context.Context) service.HealthStatus {
					return service.HealthStatus{
						Name:   "agent_manager",
						Status: service.StatusReady,
					}
				},
			},
		},
		{
			Name: "channel_manager",
			Component: service.FuncComponent{
				NameValue: "channel_manager",
				StartFn:   a.channelMgr.Start,
				StopFn:    a.channelMgr.Stop,
				HealthFn: func(context.Context) service.HealthStatus {
					return service.HealthStatus{Name: "channel_manager", Status: service.StatusReady}
				},
			},
		},
		{
			Name: "cron_service",
			Component: service.FuncComponent{
				NameValue: "cron_service",
				StartFn:   a.cronSvc.Start,
				StopFn:    a.cronSvc.Stop,
				HealthFn:  a.cronSvc.Health,
			},
		},
	}
	if a.gatewayOn && a.gateway != nil {
		components = append(components, service.NamedComponent{
			Name: "gateway",
			Component: service.FuncComponent{
				NameValue: "gateway",
				StartFn:   a.gateway.Start,
				StopFn:    a.gateway.Stop,
				HealthFn:  a.gateway.Health,
			},
		})
	}
	sup := service.NewSupervisor(service.SupervisorConfig{}, components...)
	if a.gatewayOn && a.gateway != nil {
		a.gateway.SetHealthProvider(sup.Health)
	}
	return sup
}
