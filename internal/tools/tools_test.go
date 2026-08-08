package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mini-clawdbot/internal/toolruntime"
)

func TestBuildMVPToolsAndRuntimeExecute(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, rt, err := BuildMVPTools(root, nil)
	if err != nil {
		t.Fatalf("build tools: %v", err)
	}

	read, err := rt.Execute(context.Background(), "read_file", map[string]any{"path": "a.txt"})
	if err != nil {
		t.Fatalf("read_file failed: %v", err)
	}
	if read.Output != "hello" {
		t.Fatalf("unexpected read_file output: %q", read.Output)
	}

	list, err := rt.Execute(context.Background(), "list_dir", map[string]any{"path": "."})
	if err != nil {
		t.Fatalf("list_dir failed: %v", err)
	}
	if !strings.Contains(list.Output, "a.txt") {
		t.Fatalf("unexpected list_dir output: %q", list.Output)
	}
}

func TestRuntimeShellPolicyTimeoutAndTruncate(t *testing.T) {
	root := t.TempDir()
	rt, err := toolruntime.BuildDefaultRuntime(toolruntime.DefaultRuntimeConfig{
		WorkingDir: root,
	}, nil)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	_, err = rt.Execute(context.Background(), "run_shell", map[string]any{"command": "rm -rf /tmp/x"})
	if err == nil || !strings.Contains(err.Error(), "deny pattern") {
		t.Fatalf("expected deny pattern error, got %v", err)
	}

	ok, err := rt.Execute(context.Background(), "run_shell", map[string]any{"command": "printf ok"})
	if err != nil {
		t.Fatalf("run_shell ok failed: %v", err)
	}
	if ok.Output != "ok" {
		t.Fatalf("unexpected output: %q", ok.Output)
	}

	spec, _ := rt.Get("run_shell")
	spec.Policy.MaxOutputBytes = 2
	if err := rt.Register(toolruntime.ToolSpec{
		Name: "run_shell_copy",
		Schema: spec.Schema,
		Policy: spec.Policy,
		Executor: spec.Executor,
	}); err != nil {
		t.Fatalf("register copy: %v", err)
	}
	r, err := rt.Execute(context.Background(), "run_shell_copy", map[string]any{"command": "printf 12345"})
	if err != nil {
		t.Fatalf("run shell copy: %v", err)
	}
	if !r.Truncated || r.Output != "12" {
		t.Fatalf("expected truncated output, got %+v", r)
	}
}

