package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
)

const (
	// defaultShellTimeoutSec 是 run_shell 未指定 timeout 时使用的默认秒数。
	defaultShellTimeoutSec = 20
	// maxShellTimeoutSec 是 run_shell 可接受的最大超时秒数，用于避免长时间阻塞。
	maxShellTimeoutSec = 60
)

// blockedShellKeywords 定义高危命令关键词黑名单。
// 当前策略采用“子串匹配”，命中任一关键词即拒绝执行。
var blockedShellKeywords = []string{"rm -rf", "mkfs", "shutdown", "reboot"}

// BuildMVPTools 构建 MVP 所需的三类工具：read_file/list_dir/run_shell。
//
// 参数：
// - workingDir: 工具执行根目录（相对路径将基于该目录解析）
//
// 返回：
// - []einotool.BaseTool: 可交给 Eino ToolsNode 的工具列表
// - error: 工具推断或路径解析失败时返回
func BuildMVPTools(workingDir string) ([]einotool.BaseTool, error) {
	// 统一为绝对路径，确保工具行为不受调用方当前目录影响。
	wd, err := filepath.Abs(workingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve working dir: %w", err)
	}

	// toolSet 持有共享运行时上下文（当前仅 workingDir）。
	ts := &toolSet{workingDir: wd}

	// InferTool 会基于入参结构体自动生成 JSON Schema，减少手写 schema 成本。
	readFileTool, err := toolutils.InferTool("read_file", "Read file content by path", ts.readFile)
	if err != nil {
		return nil, fmt.Errorf("build read_file tool: %w", err)
	}

	listDirTool, err := toolutils.InferTool("list_dir", "List directory entries by path", ts.listDir)
	if err != nil {
		return nil, fmt.Errorf("build list_dir tool: %w", err)
	}

	runShellTool, err := toolutils.InferTool("run_shell", "Run a shell command in project working directory", ts.runShell)
	if err != nil {
		return nil, fmt.Errorf("build run_shell tool: %w", err)
	}

	return []einotool.BaseTool{readFileTool, listDirTool, runShellTool}, nil
}

// toolSet 聚合工具实现需要共享的上下文状态。
// 当前仅保存 workingDir，后续可扩展权限策略、审计器等。
type toolSet struct {
	// workingDir 是工具执行目录：
	// - read_file/list_dir 的相对路径基于该目录解析
	// - run_shell 的命令在该目录中执行
	workingDir string
}

// readFileInput 是 read_file 的 JSON 入参结构。
type readFileInput struct {
	// Path 支持绝对/相对路径；相对路径会在 resolvePath 中转换。
	Path string `json:"path" jsonschema_description:"Path to the file"`
}

// readFile 读取目标文件并返回原始文本内容。
//
// 参数：
// - ctx: 上下文（当前逻辑未直接使用）
// - in: 入参结构，包含 path
//
// 返回：
// - string: 文件完整内容
// - error: 参数缺失、路径无效或读取失败时返回
func (t *toolSet) readFile(_ context.Context, in readFileInput) (string, error) {
	if strings.TrimSpace(in.Path) == "" {
		return "", errors.New("path is required")
	}

	absPath := t.resolvePath(in.Path)
	content, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("read_file failed: %w", err)
	}

	return string(content), nil
}

// listDirInput 是 list_dir 的 JSON 入参结构。
type listDirInput struct {
	// Path 目标目录路径，不能为空。
	Path string `json:"path" jsonschema_description:"Path to directory"`
}

// listDir 列出目录下一级条目，并用 [DIR] 标记子目录。
//
// 参数：
// - ctx: 上下文（当前逻辑未直接使用）
// - in: 入参结构，包含目录 path
//
// 返回：
// - string: 按字典序拼接后的条目文本（每行一个）
// - error: 参数缺失或目录读取失败时返回
func (t *toolSet) listDir(_ context.Context, in listDirInput) (string, error) {
	if strings.TrimSpace(in.Path) == "" {
		return "", errors.New("path is required")
	}

	absPath := t.resolvePath(in.Path)
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return "", fmt.Errorf("list_dir failed: %w", err)
	}

	// items 保存渲染后的条目文本；预分配减少扩容次数。
	items := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name = "[DIR] " + name
		}
		items = append(items, name)
	}

	// 排序保证模型多次调用时输出稳定，便于测试与 diff。
	sort.Strings(items)
	return strings.Join(items, "\n"), nil
}

// runShellInput 是 run_shell 的 JSON 入参结构。
type runShellInput struct {
	// Command 为要执行的 shell 命令字符串（必填）。
	Command string `json:"command" jsonschema_description:"Shell command to run"`
	// TimeoutSec 为可选超时秒数；<=0 用默认值，>max 会被截断到 max。
	TimeoutSec int `json:"timeout_sec,omitempty" jsonschema_description:"Timeout seconds, default 20, max 60"`
}

// runShell 在固定 workingDir 中执行命令并返回输出。
//
// 安全策略：
// - 命中 blockedShellKeywords 即拒绝执行
// - 执行超时默认 20s，最大 60s
//
// 输出策略：
// - 成功优先返回 stdout
// - stdout 为空则回退 stderr
// - 两者都为空时返回固定提示文本
//
// 参数：
// - ctx: 外层上下文（用于取消或继承超时）
// - in: 命令及超时入参
//
// 返回：
// - string: 命令输出文本
// - error: 参数错误、策略拦截、执行失败或超时时返回
func (t *toolSet) runShell(ctx context.Context, in runShellInput) (string, error) {
	command := strings.TrimSpace(in.Command)
	if command == "" {
		return "", errors.New("command is required")
	}
	if isBlockedCommand(command) {
		return "", errors.New("command blocked by safety policy")
	}

	// timeoutSec 最终执行超时值，应用默认与上限策略。
	timeoutSec := in.TimeoutSec
	if timeoutSec <= 0 {
		timeoutSec = defaultShellTimeoutSec
	}
	if timeoutSec > maxShellTimeoutSec {
		timeoutSec = maxShellTimeoutSec
	}

	// execCtx 绑定本次命令执行超时。
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	// 使用 sh -c 保持与常见 shell 命令习惯一致。
	cmd := exec.CommandContext(execCtx, "sh", "-c", command)
	cmd.Dir = t.workingDir

	// stdout/stderr 分离采集，便于失败时拼接诊断信息。
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if execCtx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("run_shell timed out after %ds", timeoutSec)
	}
	if err != nil {
		combined := strings.TrimSpace(stdout.String() + "\n" + stderr.String())
		if combined == "" {
			return "", fmt.Errorf("run_shell failed: %w", err)
		}
		return "", fmt.Errorf("run_shell failed: %w\n%s", err, combined)
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		output = strings.TrimSpace(stderr.String())
	}
	if output == "" {
		output = "(command completed with no output)"
	}

	return output, nil
}

// resolvePath 将用户输入路径解析为最终文件系统路径。
//
// 规则：
// - 绝对路径：直接 Clean 后返回
// - 相对路径：拼接 workingDir 后返回
func (t *toolSet) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(t.workingDir, filepath.Clean(path))
}

// isBlockedCommand 检查命令是否命中高危关键词。
//
// 参数：
// - command: 原始命令字符串
//
// 返回：
// - true: 命中黑名单，应该拒绝执行
// - false: 未命中黑名单，可继续执行
func isBlockedCommand(command string) bool {
	lowered := strings.ToLower(command)
	for _, kw := range blockedShellKeywords {
		if strings.Contains(lowered, kw) {
			return true
		}
	}
	return false
}
