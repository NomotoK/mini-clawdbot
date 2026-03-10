package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeToolCallingModel 用于集成测试：
// - 第 1 次收到 user 消息时返回 read_file 的 tool call
// - 第 2 次收到 tool 消息时返回最终 answer
// 该行为用于验证 ReAct 的完整闭环。
type fakeToolCallingModel struct{}

// WithTools 在测试中直接返回自身，满足 ToolCallingChatModel 接口即可。
func (f *fakeToolCallingModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return f, nil
}

// Generate 按最后一条消息角色驱动伪状态机。
func (f *fakeToolCallingModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("empty input")
	}
	last := input[len(input)-1]

	if last.Role == schema.Tool {
		return schema.AssistantMessage("done: "+last.Content, nil), nil
	}

	if last.Role == schema.User {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID:   "call-readme",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "read_file",
				Arguments: `{"path":"README.md"}`,
			},
		}}), nil
	}

	return schema.AssistantMessage("unhandled", nil), nil
}

// Stream 在测试中复用 Generate，并将单条消息包装成流返回。
func (f *fakeToolCallingModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := f.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

// TestRunOnceToolCallLoop 验证 app.RunOnce 能完成 tool call -> final response 闭环。
func TestRunOnceToolCallLoop(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello from readme"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	a, err := NewWithDependencies(context.Background(), &fakeToolCallingModel{}, 6, root)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	out, err := a.RunOnce(context.Background(), "please read README")
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !strings.Contains(out, "hello from readme") {
		t.Fatalf("unexpected output: %s", out)
	}
}

// TestResolveProjectRoot 验证从嵌套目录向上查找 go.mod 的逻辑。
func TestResolveProjectRoot(t *testing.T) {
	d := t.TempDir()
	nested := filepath.Join(d, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	goMod := filepath.Join(d, "go.mod")
	if err := os.WriteFile(goMod, []byte("module mini-clawdbot\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	root, err := ResolveProjectRoot(nested)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}
	if root != d {
		t.Fatalf("unexpected root: %s", root)
	}
}
