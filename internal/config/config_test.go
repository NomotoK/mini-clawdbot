package config

import (
	"testing"
	"time"
)

// TestLoadFromEnvMissingAPIKey 验证 API Key 缺失时返回错误。
func TestLoadFromEnvMissingAPIKey(t *testing.T) {
	t.Setenv("MINI_CLAW_API_KEY", "")
	t.Setenv("MINI_CLAW_MODEL", "")
	t.Setenv("MINI_CLAW_TIMEOUT_SEC", "")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected error when API key missing")
	}
}

// TestLoadFromEnvDefaults 验证默认模型和默认超时回填逻辑。
func TestLoadFromEnvDefaults(t *testing.T) {
	t.Setenv("MINI_CLAW_API_KEY", "test-key")
	t.Setenv("MINI_CLAW_MODEL", "")
	t.Setenv("MINI_CLAW_TIMEOUT_SEC", "")
	t.Setenv("MINI_CLAW_BASE_URL", "")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Model != defaultModel {
		t.Fatalf("unexpected model: %s", cfg.Model)
	}
	if cfg.Timeout != 60*time.Second {
		t.Fatalf("unexpected timeout: %v", cfg.Timeout)
	}
}

// TestLoadFromEnvInvalidTimeout 验证 timeout 不是正整数时返回错误。
func TestLoadFromEnvInvalidTimeout(t *testing.T) {
	t.Setenv("MINI_CLAW_API_KEY", "test-key")
	t.Setenv("MINI_CLAW_TIMEOUT_SEC", "abc")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected timeout parse error")
	}
}
