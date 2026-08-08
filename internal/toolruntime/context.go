package toolruntime

import (
	"context"

	"github.com/cloudwego/eino/compose"
	"mini-clawdbot/internal/bus"
)

type contextKey string

const execMetaKey contextKey = "toolruntime_exec_meta"

// WithExecutionContext 注入会话链路信息。
func WithExecutionContext(ctx context.Context, sessionKey bus.SessionKey, traceID string) context.Context {
	meta := ExecuteMeta{
		SessionKey: sessionKey,
		TraceID:    traceID,
	}
	return context.WithValue(ctx, execMetaKey, meta)
}

// ExecutionMetaFromContext 提取执行上下文与 tool call id。
func ExecutionMetaFromContext(ctx context.Context) ExecuteMeta {
	meta, _ := ctx.Value(execMetaKey).(ExecuteMeta)
	meta.ToolCallID = compose.GetToolCallID(ctx)
	return meta
}

