package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
	"mini-clawdbot/internal/bus"
)

const (
	// EventInboundReceived 表示收到用户入站消息。
	EventInboundReceived = "inbound_received"
	// EventLLMAssistant 表示 LLM 生成 assistant 消息。
	EventLLMAssistant = "llm_assistant"
	// EventToolCall 表示工具调用事件。
	EventToolCall = "tool_call"
	// EventToolResult 表示工具返回事件。
	EventToolResult = "tool_result"
	// EventOutboundSent 表示出站发送事件。
	EventOutboundSent = "outbound_sent"
	// EventError 表示错误事件。
	EventError = "error"
)

// JSONLStoreConfig 定义 JSONLStore 配置。
type JSONLStoreConfig struct {
	// RootDir 为项目根目录；数据目录固定为 RootDir/data/sessions。
	RootDir string
}

// EventRecord 表示 JSONL 审计事件。
type EventRecord struct {
	SessionKey string                 `json:"session_key"`
	EventType  string                 `json:"event_type"`
	Timestamp  time.Time              `json:"timestamp"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
}

// JSONLStore 是 append-only 的 JSONL 会话存储。
type JSONLStore struct {
	dir string

	mem *MemoryStore
	mu  sync.Mutex
}

// NewJSONLStore 创建并恢复 JSONLStore。
func NewJSONLStore(cfg JSONLStoreConfig) (*JSONLStore, error) {
	root := strings.TrimSpace(cfg.RootDir)
	if root == "" {
		root = "."
	}
	sessionsDir := filepath.Join(root, "data", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sessions dir: %w", err)
	}

	store := &JSONLStore{
		dir: sessionsDir,
		mem: NewMemoryStore(),
	}
	if err := store.Recover(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

// Add 仅更新内存快照；审计日志通过 RecordEvent 追加。
func (s *JSONLStore) Add(key bus.SessionKey, msg *schema.Message) {
	s.mem.Add(key, msg)
}

// Messages 返回完整历史快照。
func (s *JSONLStore) Messages(key bus.SessionKey) []*schema.Message {
	return s.mem.Messages(key)
}

// ContextMessages 返回裁剪后的上下文快照，不影响 JSONL 审计日志。
func (s *JSONLStore) ContextMessages(key bus.SessionKey, maxMessages int) []*schema.Message {
	return s.mem.ContextMessages(key, maxMessages)
}

// RecordEvent 以 append-only 模式写入 JSONL。
func (s *JSONLStore) RecordEvent(_ context.Context, key bus.SessionKey, eventType string, payload map[string]any) error {
	if key == "" {
		return nil
	}
	record := EventRecord{
		SessionKey: string(key),
		EventType:  eventType,
		Timestamp:  time.Now(),
		Payload:    payload,
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal event record: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	filePath := s.sessionPath(key)
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open session file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append session event: %w", err)
	}
	return nil
}

// Recover 扫描 JSONL 并回放为内存快照。
func (s *JSONLStore) Recover(_ context.Context) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("list sessions dir: %w", err)
	}

	s.mem = NewMemoryStore()
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		if err := s.replayFile(filepath.Join(s.dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (s *JSONLStore) replayFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open session file %s: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var record EventRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			return fmt.Errorf("decode event record: %w", err)
		}
		if record.SessionKey == "" {
			continue
		}
		s.applyRecord(record)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan session file %s: %w", path, err)
	}
	return nil
}

func (s *JSONLStore) applyRecord(record EventRecord) {
	key := bus.SessionKey(record.SessionKey)
	switch record.EventType {
	case EventInboundReceived:
		content, _ := record.Payload["content"].(string)
		if strings.TrimSpace(content) != "" {
			s.mem.Add(key, schema.UserMessage(content))
		}
	case EventLLMAssistant:
		content, _ := record.Payload["content"].(string)
		s.mem.Add(key, schema.AssistantMessage(content, nil))
	case EventToolCall:
		callID, _ := record.Payload["tool_call_id"].(string)
		toolName, _ := record.Payload["name"].(string)
		arguments, _ := record.Payload["arguments"].(string)
		if callID == "" || toolName == "" {
			return
		}
		msg := schema.AssistantMessage("", []schema.ToolCall{
			{
				ID:   callID,
				Type: "function",
				Function: schema.FunctionCall{
					Name:      toolName,
					Arguments: arguments,
				},
			},
		})
		s.mem.Add(key, msg)
	case EventToolResult:
		callID, _ := record.Payload["tool_call_id"].(string)
		toolName, _ := record.Payload["name"].(string)
		content, _ := record.Payload["content"].(string)
		if callID == "" {
			return
		}
		s.mem.Add(key, schema.ToolMessage(content, callID, schema.WithToolName(toolName)))
	}
}

func (s *JSONLStore) sessionPath(key bus.SessionKey) string {
	return filepath.Join(s.dir, safeSessionKey(key)+".jsonl")
}

func safeSessionKey(key bus.SessionKey) string {
	raw := string(key)
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		default:
			return r
		}
	}, raw)
}
