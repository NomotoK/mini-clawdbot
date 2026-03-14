package session

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/schema"
	"mini-clawdbot/internal/bus"
)

func TestJSONLStore_RecordAppendAndRecover(t *testing.T) {
	root := t.TempDir()
	sessionKey := bus.NewSessionKey("telegram", "acc-1", "chat-1", "thread-1")

	store, err := NewJSONLStore(JSONLStoreConfig{RootDir: root})
	if err != nil {
		t.Fatalf("new jsonl store: %v", err)
	}

	if err := store.RecordEvent(context.Background(), sessionKey, EventInboundReceived, map[string]any{
		"content": "user-hello",
	}); err != nil {
		t.Fatalf("record inbound: %v", err)
	}
	if err := store.RecordEvent(context.Background(), sessionKey, EventToolCall, map[string]any{
		"tool_call_id": "call-1",
		"name":         "read_file",
		"arguments":    `{"path":"README.md"}`,
	}); err != nil {
		t.Fatalf("record tool call: %v", err)
	}
	if err := store.RecordEvent(context.Background(), sessionKey, EventToolResult, map[string]any{
		"tool_call_id": "call-1",
		"name":         "read_file",
		"content":      "file-content",
	}); err != nil {
		t.Fatalf("record tool result: %v", err)
	}
	if err := store.RecordEvent(context.Background(), sessionKey, EventLLMAssistant, map[string]any{
		"content": "assistant-final",
	}); err != nil {
		t.Fatalf("record assistant: %v", err)
	}
	if err := store.RecordEvent(context.Background(), sessionKey, EventOutboundSent, map[string]any{
		"content": "assistant-final",
	}); err != nil {
		t.Fatalf("record outbound: %v", err)
	}
	if err := store.RecordEvent(context.Background(), sessionKey, EventError, map[string]any{
		"message": "none",
	}); err != nil {
		t.Fatalf("record error: %v", err)
	}

	jsonlFile := filepath.Join(root, "data", "sessions", safeSessionKey(sessionKey)+".jsonl")
	lines := countLines(t, jsonlFile)
	if lines != 6 {
		t.Fatalf("expected 6 append-only lines, got %d", lines)
	}

	recovered, err := NewJSONLStore(JSONLStoreConfig{RootDir: root})
	if err != nil {
		t.Fatalf("recover jsonl store: %v", err)
	}
	history := recovered.Messages(sessionKey)
	if len(history) != 4 {
		t.Fatalf("expected 4 replayed messages, got %d", len(history))
	}
	if history[0].Role != schema.User || history[0].Content != "user-hello" {
		t.Fatalf("unexpected replayed user message: %+v", history[0])
	}
	if history[1].Role != schema.Assistant || len(history[1].ToolCalls) != 1 {
		t.Fatalf("unexpected replayed tool_call assistant message: %+v", history[1])
	}
	if history[2].Role != schema.Tool || history[2].ToolCallID != "call-1" {
		t.Fatalf("unexpected replayed tool result message: %+v", history[2])
	}
	if history[3].Role != schema.Assistant || history[3].Content != "assistant-final" {
		t.Fatalf("unexpected replayed assistant message: %+v", history[3])
	}
}

func TestContextMessages_TrimKeepsToolPairs(t *testing.T) {
	store := NewMemoryStore()
	key := bus.NewSessionKey("telegram", "acc-1", "chat-1", "")

	store.Add(key, schema.UserMessage("u1"))
	store.Add(key, schema.AssistantMessage("", []schema.ToolCall{{
		ID:   "tool-1",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "list_dir",
			Arguments: `{"path":"."}`,
		},
	}}))
	store.Add(key, schema.ToolMessage("tool-ok", "tool-1", schema.WithToolName("list_dir")))
	store.Add(key, schema.AssistantMessage("a1", nil))
	store.Add(key, schema.UserMessage("u2"))

	trimmed := store.ContextMessages(key, 3)
	if len(trimmed) < 4 {
		t.Fatalf("expected trim to extend for tool pair integrity, got len=%d", len(trimmed))
	}
	if trimmed[0].Role != schema.Assistant || len(trimmed[0].ToolCalls) != 1 {
		t.Fatalf("expected first trimmed message to be assistant tool_call, got %+v", trimmed[0])
	}
	if trimmed[1].Role != schema.Tool || trimmed[1].ToolCallID != "tool-1" {
		t.Fatalf("expected second trimmed message to be tool result pair, got %+v", trimmed[1])
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open file %s: %v", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan file %s: %v", path, err)
	}
	return count
}
