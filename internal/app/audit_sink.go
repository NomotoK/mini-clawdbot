package app

import (
	"context"

	"mini-clawdbot/internal/bus"
	"mini-clawdbot/internal/session"
	"mini-clawdbot/internal/toolruntime"
)

type compositeAuditSink struct {
	store session.Store
	bus   *bus.MessageBus
}

func (s *compositeAuditSink) RecordToolAudit(ctx context.Context, event toolruntime.AuditEvent) error {
	if recorder, ok := s.store.(interface {
		RecordToolAudit(context.Context, toolruntime.AuditEvent) error
	}); ok {
		_ = recorder.RecordToolAudit(ctx, event)
	}
	if s.bus != nil {
		_ = s.bus.PublishAudit(ctx, &bus.AuditEvent{
			TraceID:    event.TraceID,
			SessionKey: event.SessionKey,
			Kind:       "tool_audit",
			Payload: map[string]any{
				"tool_name":    event.ToolName,
				"tool_call_id": event.ToolCallID,
				"args":         event.Args,
				"result":       event.Result,
			},
		})
	}
	return nil
}

