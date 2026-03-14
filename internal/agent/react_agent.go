package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// ReactRunner 是对 Eino ReAct Agent 的轻量封装。
//
// 该封装专注 MVP 目标：
// - 接收消息历史并执行一次 ReAct 运行
// - 统一处理最大步数错误文案
// - 对外暴露简洁的 Run 接口
type ReactRunner struct {
	// agent 是底层 Eino ReAct 实例。
	agent *react.Agent
	// maxStep 保存配置值，用于错误提示时回显。
	maxStep int
}

// TraceEvent 表示一次 LLM/工具链路事件。
type TraceEvent struct {
	Kind      string
	Role      schema.RoleType
	Content   string
	ToolName  string
	ToolCall  string
	Arguments string
	Timestamp time.Time
}

// RunTrace 记录一次 ReAct 执行的事件轨迹。
type RunTrace struct {
	Events []TraceEvent
}

const reactSystemPrompt = "You are a helpful assistant with access to shell tools.\n" +
	"When deciding to use a tool, always follow the tool schema strictly.\n" +
	"Rules for run_shell:\n" +
	"- Always provide a real, immediately executable shell command as the 'command' argument.\n" +
	"- Never use placeholder text like CREATE_SCRIPT_PLACEHOLDER or TODO.\n" +
	"- On macOS/Linux, use 'python3' instead of 'python'.\n" +
	"- To create files, prefer 'cat > file << 'EOF' ... EOF' or 'printf'.\n" +
	"- If environment context is uncertain, call list_dir/read_file first before writing commands.\n" +
	"Before every tool call, verify arguments are concrete, valid, and executable."

// NewReactRunner 使用工具调用模型与工具列表构建 ReAct 运行器。
//
// 参数：
// - ctx: 初始化上下文
// - chatModel: 支持 tool-calling 的模型实现（必填）
// - tools: 可调用工具列表
// - maxStep: 最大图执行步数；<=0 时会回退到默认值 6
//
// 返回：
// - *ReactRunner: 可执行 Run 的封装实例
// - error: 初始化失败（模型/工具配置问题）时返回
func NewReactRunner(ctx context.Context, chatModel model.ToolCallingChatModel, tools []einotool.BaseTool, maxStep int) (*ReactRunner, error) {
	if chatModel == nil {
		return nil, errors.New("chat model is required")
	}
	if maxStep <= 0 {
		maxStep = 6
	}

	toolNames, err := collectToolNames(ctx, tools)
	if err != nil {
		return nil, fmt.Errorf("collect tool names: %w", err)
	}

	// ReAct Agent 由 ToolCallingModel + ToolsNode + MaxStep 组成。
	ragent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		MessageModifier:  buildSystemPromptModifier(reactSystemPrompt),
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:                tools,
			UnknownToolsHandler:  buildUnknownToolsHandler(toolNames),
			ExecuteSequentially:  true,
			ToolArgumentsHandler: toolArgumentsHandler,
			ToolCallMiddlewares: []compose.ToolMiddleware{
				{
					Invokable: wrapInvokableToolErrors,
				},
			},
		},
		MaxStep: maxStep,
	})
	if err != nil {
		return nil, fmt.Errorf("init react agent: %w", err)
	}

	return &ReactRunner{agent: ragent, maxStep: maxStep}, nil
}

func buildSystemPromptModifier(systemPrompt string) react.MessageModifier {
	return func(_ context.Context, input []*schema.Message) []*schema.Message {
		trimmed := strings.TrimSpace(systemPrompt)
		if trimmed == "" {
			return input
		}

		for _, msg := range input {
			if msg == nil || msg.Role != schema.System {
				continue
			}
			if strings.TrimSpace(msg.Content) == trimmed {
				return input
			}
		}

		out := make([]*schema.Message, 0, len(input)+1)
		out = append(out, schema.SystemMessage(trimmed))
		out = append(out, input...)
		return out
	}
}

func collectToolNames(ctx context.Context, tools []einotool.BaseTool) ([]string, error) {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil {
			return nil, err
		}
		if info == nil || strings.TrimSpace(info.Name) == "" {
			continue
		}
		names = append(names, info.Name)
	}
	sort.Strings(names)
	return names, nil
}
// buildUnknownToolsHandler 构建一个处理未知工具调用的函数，返回可用工具列表提示。
func buildUnknownToolsHandler(knownTools []string) func(ctx context.Context, name, input string) (string, error) {
	known := strings.Join(knownTools, ", ")
	return func(_ context.Context, name, _ string) (string, error) {
		if known == "" {
			return fmt.Sprintf("unknown tool %q", name), nil
		}
		return fmt.Sprintf("unknown tool %q. Available tools: %s", name, known), nil
	}
}
// toolArgumentsHandler 验证工具调用参数的合法性，要求非空且为有效 JSON。
func toolArgumentsHandler(_ context.Context, name, arguments string) (string, error) {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return "", fmt.Errorf("tool %q arguments are required", name)
	}
	if !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("tool %q arguments must be valid JSON", name)
	}
	return trimmed, nil
}

func wrapInvokableToolErrors(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
	return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
		out, err := next(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("tool %q failed (call_id=%s): %w", input.Name, input.CallID, err)
		}
		return out, nil
	}
}

// Run 触发一次 ReAct 执行并返回最终消息。
//
// 参数：
// - ctx: 执行上下文（可用于取消/超时）
// - messages: 输入消息历史，至少包含 1 条 user message
//
// 返回：
// - *schema.Message: ReAct 最终输出消息（通常为 assistant）
// - error: 执行失败时返回；若超出最大步数，会返回带 maxStep 的明确错误
func (r *ReactRunner) Run(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
	msg, _, err := r.RunWithTrace(ctx, messages)
	return msg, err
}

// RunWithTrace 执行 ReAct 并返回最终消息与执行轨迹。
//
// trace 采集逻辑基于 Eino 的 react.WithMessageFuture：
// - assistant/tool 生成消息会进入迭代器
// - tool call / tool result / llm 文本输出会映射为事件
func (r *ReactRunner) RunWithTrace(ctx context.Context, messages []*schema.Message) (*schema.Message, *RunTrace, error) {
	if len(messages) == 0 {
		return nil, nil, errors.New("at least one message is required")
	}

	opt, future := react.WithMessageFuture()
	msg, err := r.agent.Generate(ctx, messages, opt)
	if err != nil {
		// 对 MaxStep 错误做业务友好化包装，便于 CLI 直出。
		if errors.Is(err, compose.ErrExceedMaxSteps) {
			return nil, nil, fmt.Errorf("agent reached max step limit (%d): %w", r.maxStep, err)
		}
		return nil, nil, fmt.Errorf("react run failed: %w", err)
	}

	trace := &RunTrace{Events: collectTraceEvents(future)}
	return msg, trace, nil
}

// collectTraceEvents 从消息未来对象中收集跟踪事件。
//
// 事件类型包括：
// - tool_call: 代表工具调用请求，包含工具名和参数
// - tool_result: 代表工具调用结果，包含输出内容
// - llm_output: 代表 LLM 生成的文本输出
func collectTraceEvents(future react.MessageFuture) []TraceEvent {
	iter := future.GetMessages()
	events := make([]TraceEvent, 0, 8)
	for {
		msg, ok, err := iter.Next()
		if err != nil || !ok {
			break
		}
		if msg == nil {
			continue
		}
		now := time.Now()
		if len(msg.ToolCalls) > 0 {
			for _, call := range msg.ToolCalls {
				events = append(events, TraceEvent{
					Kind:      "tool_call",
					Role:      msg.Role,
					ToolName:  call.Function.Name,
					ToolCall:  call.ID,
					Arguments: call.Function.Arguments,
					Timestamp: now,
				})
			}
			continue
		}
		if msg.Role == schema.Tool {
			events = append(events, TraceEvent{
				Kind:      "tool_result",
				Role:      msg.Role,
				Content:   msg.Content,
				ToolName:  msg.ToolName,
				ToolCall:  msg.ToolCallID,
				Timestamp: now,
			})
			continue
		}
		events = append(events, TraceEvent{
			Kind:      "llm_output",
			Role:      msg.Role,
			Content:   msg.Content,
			Timestamp: now,
		})
	}
	return events
}
