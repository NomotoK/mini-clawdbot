package toolruntime

import "time"

// DefaultRuntimeConfig 定义默认工具运行时构建参数。
type DefaultRuntimeConfig struct {
	WorkingDir    string
	EnableDocker  bool
	DockerPolicy  DockerPolicy
}

// BuildDefaultRuntime 构建内置工具运行时。
func BuildDefaultRuntime(cfg DefaultRuntimeConfig, audit AuditSink) (*Runtime, error) {
	rt := New(audit)

	hostPolicy := ToolPolicy{
		Timeout:         20 * time.Second,
		MaxOutputBytes:  64 * 1024,
		AllowedWorkDirs: []string{cfg.WorkingDir},
		SandboxMode:     SandboxModeHost,
		FallbackPolicy:  FallbackPolicyFailClose,
		CommandPolicy: CommandPolicy{
			DenyPatterns: []string{"rm -rf", "mkfs", "shutdown", "reboot", "dd if="},
			RiskLevel:    "medium",
		},
		NetworkPolicy: NetworkPolicy{Mode: "none"},
	}
	if cfg.EnableDocker {
		hostPolicy.SandboxMode = SandboxModeDocker
	}

	specs := []ToolSpec{
		{
			Name: "read_file",
			Schema: map[string]ParamSpec{
				"path": {Type: ParamTypeString, Required: true},
			},
			Policy: ToolPolicy{
				Timeout:         10 * time.Second,
				MaxOutputBytes:  64 * 1024,
				AllowedWorkDirs: []string{cfg.WorkingDir},
				SandboxMode:     SandboxModeHost,
			},
			Executor: NewReadFileExecutor(cfg.WorkingDir),
		},
		{
			Name: "list_dir",
			Schema: map[string]ParamSpec{
				"path": {Type: ParamTypeString, Required: true},
			},
			Policy: ToolPolicy{
				Timeout:         10 * time.Second,
				MaxOutputBytes:  64 * 1024,
				AllowedWorkDirs: []string{cfg.WorkingDir},
				SandboxMode:     SandboxModeHost,
			},
			Executor: NewListDirExecutor(cfg.WorkingDir),
		},
		{
			Name: "run_shell",
			Schema: map[string]ParamSpec{
				"command": {Type: ParamTypeString, Required: true},
			},
			Policy:   hostPolicy,
			Executor: NewShellExecutor(cfg.WorkingDir, cfg.DockerPolicy),
		},
		{
			Name: "web_fetch",
			Schema: map[string]ParamSpec{
				"url": {Type: ParamTypeString, Required: true},
			},
			Policy: ToolPolicy{
				Timeout:         15 * time.Second,
				MaxOutputBytes:  64 * 1024,
				AllowedWorkDirs: []string{cfg.WorkingDir},
				SandboxMode:     SandboxModeHost,
			},
			Executor: NewWebExecutor(),
		},
		{
			Name: "browser_action",
			Schema: map[string]ParamSpec{
				"action": {Type: ParamTypeString, Required: true},
			},
			Policy: ToolPolicy{
				Timeout:         20 * time.Second,
				MaxOutputBytes:  64 * 1024,
				AllowedWorkDirs: []string{cfg.WorkingDir},
				SandboxMode:     SandboxModeHost,
			},
			Executor: NewBrowserExecutor(),
		},
	}

	for _, spec := range specs {
		if err := rt.Register(spec); err != nil {
			return nil, err
		}
	}
	return rt, nil
}

