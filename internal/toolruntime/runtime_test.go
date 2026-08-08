package toolruntime

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRuntimePolicyDeniedAndTruncate(t *testing.T) {
	rt := New(nil)
	err := rt.Register(ToolSpec{
		Name: "echo",
		Schema: map[string]ParamSpec{
			"command": {Type: ParamTypeString, Required: true},
		},
		Policy: ToolPolicy{
			MaxOutputBytes: 2,
			CommandPolicy: CommandPolicy{
				DenyPatterns: []string{"rm -rf"},
			},
		},
		Executor: FuncExecutor(func(ctx context.Context, args map[string]any, spec ToolSpec) (ExecutionResult, error) {
			return ExecutionResult{Status: ExecutionStatusOK, Output: "abcd"}, nil
		}),
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err = rt.Execute(context.Background(), "echo", map[string]any{"command": "rm -rf /"})
	if err == nil || !strings.Contains(err.Error(), "deny pattern") {
		t.Fatalf("expected deny pattern error, got %v", err)
	}

	res, err := rt.Execute(context.Background(), "echo", map[string]any{"command": "echo ok"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Truncated || res.Output != "ab" {
		t.Fatalf("expected truncated result, got %+v", res)
	}
}

func TestRuntimeTimeout(t *testing.T) {
	rt := New(nil)
	err := rt.Register(ToolSpec{
		Name: "slow",
		Schema: map[string]ParamSpec{
			"command": {Type: ParamTypeString, Required: true},
		},
		Policy: ToolPolicy{
			Timeout: 100 * time.Millisecond,
		},
		Executor: FuncExecutor(func(ctx context.Context, args map[string]any, spec ToolSpec) (ExecutionResult, error) {
			select {
			case <-ctx.Done():
				return ExecutionResult{}, ctx.Err()
			case <-time.After(500 * time.Millisecond):
				return ExecutionResult{Status: ExecutionStatusOK, Output: "done"}, nil
			}
		}),
	})
	if err != nil {
		t.Fatalf("register slow: %v", err)
	}

	res, err := rt.Execute(context.Background(), "slow", map[string]any{"command": "sleep"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if res.ErrorCode != "timeout" {
		t.Fatalf("expected timeout code, got %+v", res)
	}
}

func TestDockerModeFailClose(t *testing.T) {
	rt := New(nil)
	err := rt.Register(ToolSpec{
		Name: "docker_shell",
		Schema: map[string]ParamSpec{
			"command": {Type: ParamTypeString, Required: true},
		},
		Policy: ToolPolicy{
			SandboxMode:    SandboxModeDocker,
			FallbackPolicy: FallbackPolicyFailClose,
		},
		Executor: NewShellExecutor(t.TempDir(), DockerPolicy{
			Image:   "this-image-should-not-exist:latest",
			Network: "none",
		}),
	})
	if err != nil {
		t.Fatalf("register docker_shell: %v", err)
	}

	res, err := rt.Execute(context.Background(), "docker_shell", map[string]any{"command": "echo hello"})
	if err == nil {
		t.Fatal("expected docker mode to fail without host fallback")
	}
	if res.Status != ExecutionStatusError {
		t.Fatalf("expected error status, got %+v", res)
	}
}
