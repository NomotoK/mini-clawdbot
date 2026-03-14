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
	t.Setenv("MINI_CLAW_FEISHU_ENABLED", "")
	t.Setenv("MINI_CLAW_FEISHU_ACCOUNT_ID", "")

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
	if cfg.Feishu.Enabled {
		t.Fatal("feishu should be disabled by default")
	}
	if cfg.Feishu.AccountID != "default" {
		t.Fatalf("unexpected feishu account_id: %s", cfg.Feishu.AccountID)
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

func TestLoadFromEnvFeishuEnabledValidation(t *testing.T) {
	t.Setenv("MINI_CLAW_API_KEY", "test-key")
	t.Setenv("MINI_CLAW_FEISHU_ENABLED", "true")
	t.Setenv("MINI_CLAW_FEISHU_APP_ID", "")
	t.Setenv("MINI_CLAW_FEISHU_APP_SECRET", "")

	_, err := LoadFromEnv()
	if err == nil {
		t.Fatal("expected feishu credential validation error")
	}
}

func TestLoadFromEnvFeishuEnabled(t *testing.T) {
	t.Setenv("MINI_CLAW_API_KEY", "test-key")
	t.Setenv("MINI_CLAW_FEISHU_ENABLED", "1")
	t.Setenv("MINI_CLAW_FEISHU_APP_ID", "cli_app_id")
	t.Setenv("MINI_CLAW_FEISHU_APP_SECRET", "cli_app_secret")
	t.Setenv("MINI_CLAW_FEISHU_ACCOUNT_ID", "feishu-prod")
	t.Setenv("MINI_CLAW_FEISHU_VERIFICATION_TOKEN", "vt")
	t.Setenv("MINI_CLAW_FEISHU_ENCRYPT_KEY", "ek")

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Feishu.Enabled {
		t.Fatal("feishu should be enabled")
	}
	if cfg.Feishu.AccountID != "feishu-prod" {
		t.Fatalf("unexpected account id: %s", cfg.Feishu.AccountID)
	}
	if cfg.Feishu.AppID != "cli_app_id" || cfg.Feishu.AppSecret != "cli_app_secret" {
		t.Fatalf("unexpected feishu credentials: %+v", cfg.Feishu)
	}
	if cfg.Feishu.VerificationToken != "vt" || cfg.Feishu.EncryptKey != "ek" {
		t.Fatalf("unexpected feishu webhook fields: %+v", cfg.Feishu)
	}
}

func TestParseBoolEnv(t *testing.T) {
	trueVals := []string{"1", "true", "yes", "on", "TRUE"}
	for _, v := range trueVals {
		got, err := parseBoolEnv(v)
		if err != nil || !got {
			t.Fatalf("expected true for %q, got=%v err=%v", v, got, err)
		}
	}

	falseVals := []string{"", "0", "false", "no", "off", "FALSE"}
	for _, v := range falseVals {
		got, err := parseBoolEnv(v)
		if err != nil || got {
			t.Fatalf("expected false for %q, got=%v err=%v", v, got, err)
		}
	}

	if _, err := parseBoolEnv("not-bool"); err == nil {
		t.Fatal("expected parse error for invalid bool")
	}
}
