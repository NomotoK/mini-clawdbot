package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadFileSuccessAndNotFound 验证 read_file 的成功与文件不存在错误分支。
func TestReadFileSuccessAndNotFound(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ts := &toolSet{workingDir: tmp}
	out, err := ts.readFile(context.Background(), readFileInput{Path: "a.txt"})
	if err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	if out != "hello" {
		t.Fatalf("unexpected content: %q", out)
	}

	_, err = ts.readFile(context.Background(), readFileInput{Path: "missing.txt"})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestListDirInvalidPath 验证 list_dir 对非法目录路径会返回错误。
func TestListDirInvalidPath(t *testing.T) {
	ts := &toolSet{workingDir: t.TempDir()}
	_, err := ts.listDir(context.Background(), listDirInput{Path: "not-exists"})
	if err == nil {
		t.Fatal("expected list_dir error")
	}
}

// TestRunShellSuccessTimeoutBlocked 覆盖 run_shell 的核心行为：
// 1. 正常命令成功输出
// 2. 超时命令返回 timeout 错误
// 3. 高危命令被安全策略拦截
func TestRunShellSuccessTimeoutBlocked(t *testing.T) {
	ts := &toolSet{workingDir: t.TempDir()}

	out, err := ts.runShell(context.Background(), runShellInput{Command: "printf ok"})
	if err != nil {
		t.Fatalf("run_shell success case failed: %v", err)
	}
	if out != "ok" {
		t.Fatalf("unexpected output: %q", out)
	}

	_, err = ts.runShell(context.Background(), runShellInput{Command: "sleep 2", TimeoutSec: 1})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}

	_, err = ts.runShell(context.Background(), runShellInput{Command: "rm -rf /tmp/x"})
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected blocked error, got: %v", err)
	}
}
