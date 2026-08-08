package toolruntime

import (
	"context"
	"time"

	"mini-clawdbot/internal/bus"
)

// SandboxMode 表示工具执行沙箱模式。
type SandboxMode string

const (
	SandboxModeHost   SandboxMode = "host"
	SandboxModeDocker SandboxMode = "docker"
)

// ExecutionStatus 表示执行状态。
type ExecutionStatus string

const (
	ExecutionStatusOK     ExecutionStatus = "ok"
	ExecutionStatusDenied ExecutionStatus = "denied"
	ExecutionStatusError  ExecutionStatus = "error"
)

// ParamType 描述参数类型。
type ParamType string

const (
	ParamTypeString  ParamType = "string"
	ParamTypeInteger ParamType = "integer"
	ParamTypeBoolean ParamType = "boolean"
	ParamTypeObject  ParamType = "object"
)

// ParamSpec 描述参数校验规则。
type ParamSpec struct {
	Type     ParamType
	Required bool
}

// ToolPolicy 定义工具策略。
type ToolPolicy struct {
	Timeout        time.Duration
	MaxOutputBytes int
	AllowedWorkDirs []string
	SandboxMode    SandboxMode
	CommandPolicy  CommandPolicy
	NetworkPolicy  NetworkPolicy
	FallbackPolicy FallbackPolicy
}

// FallbackPolicy 定义沙箱失败时策略。
type FallbackPolicy string

const (
	FallbackPolicyFailClose FallbackPolicy = "fail_close"
)

// CommandPolicy 定义命令级控制。
type CommandPolicy struct {
	DenyPatterns  []string
	AllowCommands []string
	RiskLevel     string
}

// NetworkPolicy 定义网络级控制。
type NetworkPolicy struct {
	Mode string
}

// DockerPolicy 定义 Docker 限额。
type DockerPolicy struct {
	Image            string
	CPUCount         float64
	MemoryMB         int64
	PidsLimit        int64
	Network          string
	ReadOnlyRootfs   bool
	CapDrop          []string
	NoNewPrivileges  bool
	WorkDir          string
	AutoRemove       bool
}

// ToolSpec 描述统一工具。
type ToolSpec struct {
	Name     string
	Schema   map[string]ParamSpec
	Policy   ToolPolicy
	Executor Executor
}

// ExecuteMeta 描述本次调用上下文元数据。
type ExecuteMeta struct {
	SessionKey bus.SessionKey
	TraceID    string
	ToolCallID string
}

// ExecutionResult 统一执行结果。
type ExecutionResult struct {
	Status     ExecutionStatus `json:"status"`
	Output     string          `json:"output,omitempty"`
	Truncated  bool            `json:"truncated"`
	DurationMs int64           `json:"duration_ms"`
	ExitCode   int             `json:"exit_code,omitempty"`
	ErrorCode  string          `json:"error_code,omitempty"`
	Metadata   map[string]any  `json:"metadata,omitempty"`
}

// AuditEvent 是工具审计事件。
type AuditEvent struct {
	Timestamp  time.Time      `json:"timestamp"`
	ToolName   string         `json:"tool_name"`
	SessionKey bus.SessionKey `json:"session_key"`
	TraceID    string         `json:"trace_id"`
	ToolCallID string         `json:"tool_call_id"`
	Args       map[string]any `json:"args,omitempty"`
	Result     ExecutionResult `json:"result"`
}

// AuditSink 接收审计事件。
type AuditSink interface {
	RecordToolAudit(ctx context.Context, event AuditEvent) error
}

// Executor 执行具体工具逻辑。
type Executor interface {
	Execute(ctx context.Context, args map[string]any, spec ToolSpec) (ExecutionResult, error)
}

