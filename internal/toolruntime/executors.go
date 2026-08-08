package toolruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// FuncExecutor 将函数适配为 Executor。
type FuncExecutor func(ctx context.Context, args map[string]any, spec ToolSpec) (ExecutionResult, error)

func (f FuncExecutor) Execute(ctx context.Context, args map[string]any, spec ToolSpec) (ExecutionResult, error) {
	return f(ctx, args, spec)
}

// NewReadFileExecutor 创建 read_file 执行器。
func NewReadFileExecutor(baseDir string) Executor {
	return FuncExecutor(func(_ context.Context, args map[string]any, _ ToolSpec) (ExecutionResult, error) {
		p, _ := args["path"].(string)
		if strings.TrimSpace(p) == "" {
			return ExecutionResult{Status: ExecutionStatusDenied, ErrorCode: "invalid_path"}, fmt.Errorf("path is required")
		}
		abs := resolvePath(baseDir, p)
		data, err := os.ReadFile(abs)
		if err != nil {
			return ExecutionResult{Status: ExecutionStatusError, ErrorCode: "read_failed"}, err
		}
		return ExecutionResult{Status: ExecutionStatusOK, Output: string(data), ExitCode: 0}, nil
	})
}

// NewListDirExecutor 创建 list_dir 执行器。
func NewListDirExecutor(baseDir string) Executor {
	return FuncExecutor(func(_ context.Context, args map[string]any, _ ToolSpec) (ExecutionResult, error) {
		p, _ := args["path"].(string)
		if strings.TrimSpace(p) == "" {
			return ExecutionResult{Status: ExecutionStatusDenied, ErrorCode: "invalid_path"}, fmt.Errorf("path is required")
		}
		abs := resolvePath(baseDir, p)
		entries, err := os.ReadDir(abs)
		if err != nil {
			return ExecutionResult{Status: ExecutionStatusError, ErrorCode: "list_failed"}, err
		}
		items := make([]string, 0, len(entries))
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name = "[DIR] " + name
			}
			items = append(items, name)
		}
		sort.Strings(items)
		return ExecutionResult{
			Status:   ExecutionStatusOK,
			Output:   strings.Join(items, "\n"),
			ExitCode: 0,
		}, nil
	})
}

// NewShellExecutor 创建 shell 执行器。
func NewShellExecutor(baseDir string, dockerPolicy DockerPolicy) Executor {
	return FuncExecutor(func(ctx context.Context, args map[string]any, spec ToolSpec) (ExecutionResult, error) {
		cmdText, _ := args["command"].(string)
		if strings.TrimSpace(cmdText) == "" {
			return ExecutionResult{Status: ExecutionStatusDenied, ErrorCode: "invalid_command"}, fmt.Errorf("command is required")
		}

		switch spec.Policy.SandboxMode {
		case SandboxModeDocker:
			return execInDocker(ctx, cmdText, baseDir, dockerPolicy)
		default:
			return execOnHost(ctx, cmdText, baseDir)
		}
	})
}

func execOnHost(ctx context.Context, command, workdir string) (ExecutionResult, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = workdir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	output := strings.TrimSpace(stdout.String())
	if output == "" {
		output = strings.TrimSpace(stderr.String())
	}
	result := ExecutionResult{
		Output:   output,
		ExitCode: 0,
		Status:   ExecutionStatusOK,
	}
	if err != nil {
		result.Status = ExecutionStatusError
		result.ErrorCode = "exec_failed"
		if ee, ok := err.(*exec.ExitError); ok {
			result.ExitCode = ee.ExitCode()
		}
		if result.Output == "" {
			result.Output = err.Error()
		}
		return result, fmt.Errorf("shell command failed: %w", err)
	}
	return result, nil
}

func execInDocker(ctx context.Context, command, workdir string, policy DockerPolicy) (ExecutionResult, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return ExecutionResult{
			Status:    ExecutionStatusError,
			ErrorCode: "sandbox_unavailable",
		}, fmt.Errorf("docker unavailable: %w", err)
	}
	image := policy.Image
	if image == "" {
		image = "alpine:latest"
	}
	containerWorkdir := policy.WorkDir
	if containerWorkdir == "" {
		containerWorkdir = "/workspace"
	}
	network := policy.Network
	if network == "" {
		network = "none"
	}

	args := []string{
		"run", "--rm", "--network", network,
		"-v", fmt.Sprintf("%s:%s", workdir, containerWorkdir),
		"-w", containerWorkdir,
	}
	if policy.ReadOnlyRootfs {
		args = append(args, "--read-only")
	}
	if policy.NoNewPrivileges {
		args = append(args, "--security-opt", "no-new-privileges")
	}
	if policy.MemoryMB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%dm", policy.MemoryMB))
	}
	if policy.CPUCount > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%.2f", policy.CPUCount))
	}
	if policy.PidsLimit > 0 {
		args = append(args, "--pids-limit", fmt.Sprintf("%d", policy.PidsLimit))
	}
	for _, cap := range policy.CapDrop {
		args = append(args, "--cap-drop", cap)
	}
	args = append(args, image, "sh", "-c", command)

	dockerCmd := exec.CommandContext(ctx, "docker", args...)
	var out bytes.Buffer
	dockerCmd.Stdout = &out
	dockerCmd.Stderr = &out
	err := dockerCmd.Run()
	result := ExecutionResult{
		Output: strings.TrimSpace(out.String()),
		Status: ExecutionStatusOK,
	}
	if err != nil {
		result.Status = ExecutionStatusError
		result.ErrorCode = "sandbox_exec_failed"
		if ee, ok := err.(*exec.ExitError); ok {
			result.ExitCode = ee.ExitCode()
		}
		return result, fmt.Errorf("docker command failed: %w", err)
	}
	return result, nil
}

// NewWebExecutor 创建 web 工具执行器（基础 HTTP GET）。
func NewWebExecutor() Executor {
	return FuncExecutor(func(ctx context.Context, args map[string]any, _ ToolSpec) (ExecutionResult, error) {
		rawURL, _ := args["url"].(string)
		if strings.TrimSpace(rawURL) == "" {
			return ExecutionResult{Status: ExecutionStatusDenied, ErrorCode: "invalid_url"}, fmt.Errorf("url is required")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return ExecutionResult{Status: ExecutionStatusDenied, ErrorCode: "invalid_url"}, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return ExecutionResult{Status: ExecutionStatusError, ErrorCode: "http_failed"}, err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if err != nil {
			return ExecutionResult{Status: ExecutionStatusError, ErrorCode: "http_read_failed"}, err
		}
		payload, _ := json.Marshal(map[string]any{
			"status": resp.StatusCode,
			"body":   string(body),
		})
		return ExecutionResult{
			Status:   ExecutionStatusOK,
			Output:   string(payload),
			ExitCode: 0,
		}, nil
	})
}

// NewBrowserExecutor 创建浏览器工具占位执行器（M3 Phase1 Host 可用）。
func NewBrowserExecutor() Executor {
	return FuncExecutor(func(_ context.Context, args map[string]any, _ ToolSpec) (ExecutionResult, error) {
		action, _ := args["action"].(string)
		if strings.TrimSpace(action) == "" {
			action = "noop"
		}
		return ExecutionResult{
			Status:   ExecutionStatusOK,
			Output:   fmt.Sprintf("browser action %q accepted (host mode)", action),
			ExitCode: 0,
		}, nil
	})
}

func resolvePath(baseDir, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(baseDir, filepath.Clean(p))
}
