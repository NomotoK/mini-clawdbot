package toolruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultTimeout        = 20 * time.Second
	maxTimeout            = 300 * time.Second
	defaultMaxOutputBytes = 64 * 1024
)

// Runtime 是统一工具执行运行时。
type Runtime struct {
	mu    sync.RWMutex
	specs map[string]ToolSpec
	audit AuditSink
}

// New 创建运行时。
func New(audit AuditSink) *Runtime {
	return &Runtime{
		specs: make(map[string]ToolSpec),
		audit: audit,
	}
}

// Register 注册工具定义。
func (r *Runtime) Register(spec ToolSpec) error {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return fmt.Errorf("tool name is required")
	}
	if spec.Executor == nil {
		return fmt.Errorf("tool %s executor is required", name)
	}

	spec.Name = name
	spec.Policy = normalizePolicy(spec.Policy)

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.specs[name]; ok {
		return fmt.Errorf("tool %s already registered", name)
	}
	r.specs[name] = spec
	return nil
}

// Get 返回工具定义。
func (r *Runtime) Get(name string) (ToolSpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	spec, ok := r.specs[name]
	return spec, ok
}

// List 返回已注册工具名。
func (r *Runtime) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.specs))
	for n := range r.specs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Execute 统一执行入口。
func (r *Runtime) Execute(ctx context.Context, name string, args map[string]any) (ExecutionResult, error) {
	spec, ok := r.Get(name)
	if !ok {
		return ExecutionResult{
			Status:    ExecutionStatusError,
			ErrorCode: "tool_not_found",
		}, fmt.Errorf("tool %s not found", name)
	}

	start := time.Now()
	if args == nil {
		args = map[string]any{}
	}

	if err := validateSchema(args, spec.Schema); err != nil {
		result := ExecutionResult{
			Status:     ExecutionStatusDenied,
			ErrorCode:  "validation_failed",
			DurationMs: elapsedMs(start),
		}
		r.recordAudit(ctx, spec.Name, args, result)
		return result, err
	}
	if err := validatePolicy(args, spec.Policy); err != nil {
		result := ExecutionResult{
			Status:     ExecutionStatusDenied,
			ErrorCode:  "policy_denied",
			DurationMs: elapsedMs(start),
		}
		r.recordAudit(ctx, spec.Name, args, result)
		return result, err
	}

	timeout := spec.Policy.Timeout
	execCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	result, err := spec.Executor.Execute(execCtx, args, spec)
	if result.DurationMs <= 0 {
		result.DurationMs = elapsedMs(start)
	}
	if execCtx.Err() == context.DeadlineExceeded {
		result.Status = ExecutionStatusError
		result.ErrorCode = "timeout"
		if err == nil {
			err = fmt.Errorf("tool %s timed out", spec.Name)
		}
	}

	result = applyOutputLimit(result, spec.Policy.MaxOutputBytes)
	r.recordAudit(ctx, spec.Name, args, result)
	return result, err
}

func (r *Runtime) recordAudit(ctx context.Context, toolName string, args map[string]any, result ExecutionResult) {
	if r.audit == nil {
		return
	}
	meta := ExecutionMetaFromContext(ctx)
	_ = r.audit.RecordToolAudit(ctx, AuditEvent{
		Timestamp:  time.Now(),
		ToolName:   toolName,
		SessionKey: meta.SessionKey,
		TraceID:    meta.TraceID,
		ToolCallID: meta.ToolCallID,
		Args:       sanitizeArgs(args),
		Result:     result,
	})
}

func normalizePolicy(p ToolPolicy) ToolPolicy {
	if p.Timeout <= 0 {
		p.Timeout = defaultTimeout
	}
	if p.Timeout > maxTimeout {
		p.Timeout = maxTimeout
	}
	if p.MaxOutputBytes <= 0 {
		p.MaxOutputBytes = defaultMaxOutputBytes
	}
	if p.SandboxMode == "" {
		p.SandboxMode = SandboxModeHost
	}
	if p.FallbackPolicy == "" {
		p.FallbackPolicy = FallbackPolicyFailClose
	}
	if p.NetworkPolicy.Mode == "" {
		p.NetworkPolicy.Mode = "none"
	}
	return p
}

func validateSchema(args map[string]any, schema map[string]ParamSpec) error {
	for name, spec := range schema {
		val, ok := args[name]
		if spec.Required && !ok {
			return fmt.Errorf("missing required field %q", name)
		}
		if !ok {
			continue
		}
		if err := checkType(name, val, spec.Type); err != nil {
			return err
		}
	}
	return nil
}

func checkType(name string, value any, typ ParamType) error {
	switch typ {
	case ParamTypeString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("field %q must be string", name)
		}
	case ParamTypeInteger:
		switch value.(type) {
		case int, int32, int64, float64, float32:
		default:
			return fmt.Errorf("field %q must be integer", name)
		}
	case ParamTypeBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("field %q must be boolean", name)
		}
	case ParamTypeObject:
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("field %q must be object", name)
		}
	}
	return nil
}

func validatePolicy(args map[string]any, policy ToolPolicy) error {
	if len(policy.AllowedWorkDirs) > 0 {
		wd, _ := args["working_dir"].(string)
		if wd != "" {
			absWD, err := filepath.Abs(wd)
			if err != nil {
				return fmt.Errorf("resolve working_dir: %w", err)
			}
			allowed := false
			for _, allow := range policy.AllowedWorkDirs {
				absAllow, err := filepath.Abs(allow)
				if err != nil {
					continue
				}
				if strings.HasPrefix(absWD, absAllow) {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("working_dir %q not allowed", wd)
			}
		}
	}

	cmd, _ := args["command"].(string)
	if cmd == "" {
		return nil
	}
	lower := strings.ToLower(cmd)
	for _, deny := range policy.CommandPolicy.DenyPatterns {
		deny = strings.TrimSpace(strings.ToLower(deny))
		if deny != "" && strings.Contains(lower, deny) {
			return fmt.Errorf("command blocked by deny pattern %q", deny)
		}
	}
	if len(policy.CommandPolicy.AllowCommands) == 0 {
		return nil
	}
	cmdName := strings.Fields(cmd)
	if len(cmdName) == 0 {
		return fmt.Errorf("empty command")
	}
	for _, allow := range policy.CommandPolicy.AllowCommands {
		if cmdName[0] == allow {
			return nil
		}
	}
	return fmt.Errorf("command %q not in allow list", cmdName[0])
}

func applyOutputLimit(result ExecutionResult, limit int) ExecutionResult {
	if limit <= 0 || len(result.Output) <= limit {
		return result
	}
	result.Output = result.Output[:limit]
	result.Truncated = true
	return result
}

func elapsedMs(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

func sanitizeArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		if strings.Contains(strings.ToLower(k), "token") ||
			strings.Contains(strings.ToLower(k), "secret") ||
			strings.Contains(strings.ToLower(k), "password") {
			out[k] = "***"
			continue
		}
		out[k] = v
	}
	return out
}

