package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"mini-clawdbot/internal/tools"
)

// infiniteToolModel 每次都返回同一个 tool call，
// 用于触发 ReAct MaxStep 上限错误分支。
type infiniteToolModel struct{}

// WithTools 在测试中直接返回自身。
func (m *infiniteToolModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

// Generate 始终返回 list_dir 的工具调用，不产生最终回答。
func (m *infiniteToolModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return schema.AssistantMessage("", []schema.ToolCall{{
		ID:   "loop-call",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "list_dir",
			Arguments: `{"path":"."}`,
		},
	}}), nil
}

// Stream 在测试中复用 Generate，并包装为单元素流。
func (m *infiniteToolModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

type traceToolModel struct{}

func (m *traceToolModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *traceToolModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("empty input")
	}
	last := input[len(input)-1]
	if last.Role == schema.Tool {
		return schema.AssistantMessage("final: "+last.Content, nil), nil
	}
	return schema.AssistantMessage("", []schema.ToolCall{{
		ID:   "call-readme",
		Type: "function",
		Function: schema.FunctionCall{
			Name:      "read_file",
			Arguments: `{"path":"README.md"}`,
		},
	}}), nil
}

func (m *traceToolModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

// TestRunMaxStepExceeded 验证 runner 在无限工具调用场景下会返回 max-step 错误。
func TestRunMaxStepExceeded(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	toolList, _, err := tools.BuildMVPTools(root, nil)
	if err != nil {
		t.Fatalf("build tools: %v", err)
	}

	r, err := NewReactRunner(context.Background(), &infiniteToolModel{}, toolList, 2)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	_, err = r.Run(context.Background(), []*schema.Message{schema.UserMessage("loop")})
	if err == nil {
		t.Fatal("expected max step error")
	}
	if !strings.Contains(err.Error(), "max step") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunWithTraceCollectsToolAndLLMEvents(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("trace-content"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	toolList, _, err := tools.BuildMVPTools(root, nil)
	if err != nil {
		t.Fatalf("build tools: %v", err)
	}

	r, err := NewReactRunner(context.Background(), &traceToolModel{}, toolList, 6)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	finalMsg, trace, err := r.RunWithTrace(context.Background(), []*schema.Message{schema.UserMessage("read readme")})
	if err != nil {
		t.Fatalf("run with trace: %v", err)
	}
	if finalMsg == nil || !strings.Contains(finalMsg.Content, "trace-content") {
		t.Fatalf("unexpected final msg: %+v", finalMsg)
	}
	if trace == nil || len(trace.Events) == 0 {
		t.Fatalf("expected trace events, got %+v", trace)
	}

	hasToolCall := false
	hasToolResult := false
	hasLLMOutput := false
	for _, evt := range trace.Events {
		switch evt.Kind {
		case "tool_call":
			hasToolCall = true
		case "tool_result":
			hasToolResult = true
		case "llm_output":
			hasLLMOutput = true
		}
	}
	if !hasToolCall || !hasToolResult || !hasLLMOutput {
		t.Fatalf("incomplete trace events: %+v", trace.Events)
	}
}
