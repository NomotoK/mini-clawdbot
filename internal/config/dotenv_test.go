package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadDotEnvIfExistsLoadsVariables 验证 .env 会被自动读取并注入环境变量。
func TestLoadDotEnvIfExistsLoadsVariables(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, ".env")
	content := "MINI_CLAW_API_KEY=from_env_file\nMINI_CLAW_MODEL=gpt-x\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	t.Setenv("MINI_CLAW_API_KEY", "")
	t.Setenv("MINI_CLAW_MODEL", "")

	if err := LoadDotEnvIfExists(p); err != nil {
		t.Fatalf("load dotenv: %v", err)
	}

	if got := os.Getenv("MINI_CLAW_API_KEY"); got != "from_env_file" {
		t.Fatalf("unexpected api key: %s", got)
	}
	if got := os.Getenv("MINI_CLAW_MODEL"); got != "gpt-x" {
		t.Fatalf("unexpected model: %s", got)
	}
}

// TestLoadDotEnvIfExistsDoesNotOverride 验证已存在环境变量不会被 .env 覆盖。
func TestLoadDotEnvIfExistsDoesNotOverride(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, ".env")
	if err := os.WriteFile(p, []byte("MINI_CLAW_MODEL=from_file\n"), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	t.Setenv("MINI_CLAW_MODEL", "from_shell")
	if err := LoadDotEnvIfExists(p); err != nil {
		t.Fatalf("load dotenv: %v", err)
	}
	if got := os.Getenv("MINI_CLAW_MODEL"); got != "from_shell" {
		t.Fatalf("env should not be overridden, got: %s", got)
	}
}

// TestLoadDotEnvIfExistsMissingFile 验证 .env 缺失时返回 nil。
func TestLoadDotEnvIfExistsMissingFile(t *testing.T) {
	err := LoadDotEnvIfExists(filepath.Join(t.TempDir(), ".env"))
	if err != nil {
		t.Fatalf("missing file should not fail: %v", err)
	}
}

// TestLoadDotEnvIfExistsInvalidLine 验证非法格式会返回错误。
func TestLoadDotEnvIfExistsInvalidLine(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, ".env")
	if err := os.WriteFile(p, []byte("BROKEN_LINE\n"), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	if err := LoadDotEnvIfExists(p); err == nil {
		t.Fatal("expected parse error for invalid line")
	}
}
