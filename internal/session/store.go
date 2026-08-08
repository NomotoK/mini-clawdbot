package session

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
	"mini-clawdbot/internal/bus"
	"mini-clawdbot/internal/toolruntime"
)

// Store 定义按 SessionKey 访问会话历史的最小接口。(数据结构为 Message 切片)
type Store interface {
	Add(key bus.SessionKey, msg *schema.Message)
	Messages(key bus.SessionKey) []*schema.Message// Messages 返回指定会话消息历史（浅拷贝）
	ContextMessages(key bus.SessionKey, maxMessages int) []*schema.Message
	RecordEvent(ctx context.Context, key bus.SessionKey, eventType string, payload map[string]any) error
	Recover(ctx context.Context) error
}

// ToolAuditRecord 是工具审计结构化记录。
type ToolAuditRecord struct {
	SessionKey bus.SessionKey                `json:"session_key"`
	Timestamp  time.Time                     `json:"timestamp"`
	ToolName   string                        `json:"tool_name"`
	TraceID    string                        `json:"trace_id"`
	ToolCallID string                        `json:"tool_call_id"`
	Args       map[string]any                `json:"args,omitempty"`
	Result     toolruntime.ExecutionResult   `json:"result"`
}

// AuditQueryable 支持查询工具审计日志。
type AuditQueryable interface {
	QueryToolAudits(ctx context.Context, limit int) ([]ToolAuditRecord, error)
}

// MemoryStore 是基于 MemorySession 的内存会话存储。
type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[bus.SessionKey]*MemorySession
}

// NewMemoryStore 创建内存会话存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[bus.SessionKey]*MemorySession),
	}
}

// Add 追加消息到指定会话。
func (s *MemoryStore) Add(key bus.SessionKey, msg *schema.Message) {
	if msg == nil || key == "" {
		return
	}
	s.getOrCreate(key).Add(msg)
}

// Messages 返回指定会话消息历史（浅拷贝）。
func (s *MemoryStore) Messages(key bus.SessionKey) []*schema.Message {
	if key == "" {
		return nil
	}
	return s.getOrCreate(key).Messages()
}

// ContextMessages 返回裁剪后的上下文消息，确保不截断 tool call/result 对。
func (s *MemoryStore) ContextMessages(key bus.SessionKey, maxMessages int) []*schema.Message {
	return trimMessagesKeepToolPairs(s.Messages(key), maxMessages)
}

// RecordEvent 在内存存储中是 no-op，仅保留接口兼容。
func (s *MemoryStore) RecordEvent(context.Context, bus.SessionKey, string, map[string]any) error {
	return nil
}

// Recover 在内存存储中是 no-op。
func (s *MemoryStore) Recover(context.Context) error {
	return nil
}

// RecordToolAudit 在内存存储中回放为通用事件。
func (s *MemoryStore) RecordToolAudit(ctx context.Context, event toolruntime.AuditEvent) error {
	key := event.SessionKey
	if key == "" {
		key = bus.NewSessionKey("system", "default", "cron", "root")
	}
	return s.RecordEvent(ctx, key, EventToolAudit, map[string]any{
		"tool_name":    event.ToolName,
		"trace_id":     event.TraceID,
		"tool_call_id": event.ToolCallID,
		"args":         event.Args,
		"result":       event.Result,
	})
}

// QueryToolAudits 在内存存储中无持久记录，返回空结果。
func (s *MemoryStore) QueryToolAudits(context.Context, int) ([]ToolAuditRecord, error) {
	return []ToolAuditRecord{}, nil
}

func (s *MemoryStore) getOrCreate(key bus.SessionKey) *MemorySession {
	s.mu.RLock()
	existing, ok := s.sessions[key]
	s.mu.RUnlock()
	if ok {
		return existing
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok = s.sessions[key]; ok {
		return existing
	}
	created := NewMemorySession()
	s.sessions[key] = created
	return created
}

func trimMessagesKeepToolPairs(messages []*schema.Message, maxMessages int) []*schema.Message {
	if maxMessages <= 0 || len(messages) <= maxMessages {
		return copyMessages(messages)
	}

	start := len(messages) - maxMessages
	if start < 0 {
		start = 0
	}

	for start > 0 {
		current := messages[start]
		if current == nil {
			start--
			continue
		}

		if current.Role == schema.Tool {
			callID := current.ToolCallID
			if callID == "" {
				start++
				break
			}
			foundAssistant := false
			for i := start - 1; i >= 0; i-- {
				msg := messages[i]
				if msg == nil || msg.Role != schema.Assistant || len(msg.ToolCalls) == 0 {
					continue
				}
				if hasToolCallID(msg, callID) {
					start = i
					foundAssistant = true
					break
				}
			}
			if !foundAssistant {
				start++
				break
			}
			continue
		}

		if current.Role == schema.Assistant && len(current.ToolCalls) > 0 {
			missingToolResult := false
			for _, call := range current.ToolCalls {
				if !hasToolResultInRange(messages, start+1, call.ID) {
					missingToolResult = true
					break
				}
			}
			if missingToolResult {
				start--
				continue
			}
		}
		break
	}

	if start < 0 {
		start = 0
	}
	return copyMessages(messages[start:])
}

func hasToolCallID(msg *schema.Message, callID string) bool {
	for _, call := range msg.ToolCalls {
		if call.ID == callID {
			return true
		}
	}
	return false
}

func hasToolResultInRange(messages []*schema.Message, from int, callID string) bool {
	for i := from; i < len(messages); i++ {
		msg := messages[i]
		if msg == nil {
			continue
		}
		if msg.Role == schema.Tool && msg.ToolCallID == callID {
			return true
		}
	}
	return false
}

func copyMessages(messages []*schema.Message) []*schema.Message {
	out := make([]*schema.Message, len(messages))
	copy(out, messages)
	return out
}
