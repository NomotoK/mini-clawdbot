package agent

import (
	"context"
	"errors"
	"fmt"

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

	// ReAct Agent 由 ToolCallingModel + ToolsNode + MaxStep 组成。
	ragent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: tools,
		},
		MaxStep: maxStep,
	})
	if err != nil {
		return nil, fmt.Errorf("init react agent: %w", err)
	}

	return &ReactRunner{agent: ragent, maxStep: maxStep}, nil
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
	if len(messages) == 0 {
		return nil, errors.New("at least one message is required")
	}

	msg, err := r.agent.Generate(ctx, messages)
	if err != nil {
		// 对 MaxStep 错误做业务友好化包装，便于 CLI 直出。
		if errors.Is(err, compose.ErrExceedMaxSteps) {
			return nil, fmt.Errorf("agent reached max step limit (%d): %w", r.maxStep, err)
		}
		return nil, fmt.Errorf("react run failed: %w", err)
	}

	return msg, nil
}
