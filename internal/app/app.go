package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"mini-clawdbot/internal/agent"
	"mini-clawdbot/internal/config"
	"mini-clawdbot/internal/llm"
	"mini-clawdbot/internal/session"
	"mini-clawdbot/internal/tools"
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
	runner  *agent.ReactRunner
	session *session.MemorySession
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

	return NewWithDependencies(ctx, chatModel, opts.MaxStep, wd)
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
	if strings.TrimSpace(workingDir) == "" {
		return nil, fmt.Errorf("working directory is required")
	}

	toolList, err := tools.BuildMVPTools(workingDir)
	if err != nil {
		return nil, err
	}

	runner, err := agent.NewReactRunner(ctx, chatModel, toolList, maxStep)
	if err != nil {
		return nil, err
	}

	return &App{
		runner:  runner,
		session: session.NewMemorySession(),
	}, nil
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
