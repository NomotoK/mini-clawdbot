package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultModel 是在环境变量未显式指定时使用的默认模型名。
	defaultModel = "gpt-4o-mini"
	// defaultTimeoutSec 是模型请求超时时间的默认秒数。
	defaultTimeoutSec = 60
)

// Config 描述 mini-clawdbot 的模型连接配置。
//
// 字段说明：
// - APIKey: OpenAI 兼容接口的鉴权密钥（必填）
// - BaseURL: OpenAI 兼容网关地址（可选）
// - Model: 模型名（为空则使用默认值）
// - Timeout: HTTP 请求超时时间
//
// 该结构仅承载 MVP 所需最小配置项。
type Config struct {
	APIKey  string
	BaseURL string
	Model   string
	Timeout time.Duration
	Feishu  FeishuConfig
	Service ServiceConfig
}

// ServiceConfig 是 M3 分层服务配置。
type ServiceConfig struct {
	Channels ManagerSection  `json:"channels"`
	Gateway  GatewaySection  `json:"gateway"`
	Tools    ToolsSection    `json:"tools"`
	Security SecuritySection `json:"security"`
	Session  SessionSection  `json:"session"`
	Cron     CronSection     `json:"cron"`
	Manager  ManagerSection  `json:"manager"`
}

type GatewaySection struct {
	Enabled   bool
	Host      string
	Port      int
	AuthToken string
}

type ToolsSection struct {
	EnableDocker bool
}

type SecuritySection struct {
	DefaultTimeoutSec int
	MaxOutputBytes    int
}

type SessionSection struct {
	MaxContextMessages int
}

type CronSection struct {
	Enabled bool
}

type ManagerSection struct {
	WorkerQueueSize int
}

// FeishuConfig 描述飞书渠道所需的最小配置。
//
// 字段说明：
// - Enabled: 是否启用飞书渠道注册
// - AppID/AppSecret: 飞书应用凭证（Enabled=true 时必填）
// - AccountID: 渠道账号标识，用于区分同渠道多账号（默认 "default"）
// - VerificationToken/EncryptKey: 事件校验参数（可选，按飞书应用配置填写）
type FeishuConfig struct {
	Enabled           bool
	AppID             string
	AppSecret         string
	AccountID         string
	VerificationToken string
	EncryptKey        string
}

// LoadFromEnv 从环境变量读取配置并执行最小校验。
//
// 读取键：
// - MINI_CLAW_API_KEY（必填）
// - MINI_CLAW_BASE_URL（可选）
// - MINI_CLAW_MODEL（可选）
// - MINI_CLAW_TIMEOUT_SEC（可选，正整数）
//
// 返回：
// - Config: 解析后的配置对象
// - error: 缺失必填项或格式非法时返回错误
func LoadFromEnv() (Config, error) {
	// 先读取原始环境变量，后续再统一填默认值和校验。
	cfg := Config{
		APIKey:  os.Getenv("MINI_CLAW_API_KEY"),
		BaseURL: os.Getenv("MINI_CLAW_BASE_URL"),
		Model:   os.Getenv("MINI_CLAW_MODEL"),
		Feishu: FeishuConfig{
			AppID:             os.Getenv("MINI_CLAW_FEISHU_APP_ID"),
			AppSecret:         os.Getenv("MINI_CLAW_FEISHU_APP_SECRET"),
			AccountID:         os.Getenv("MINI_CLAW_FEISHU_ACCOUNT_ID"),
			VerificationToken: os.Getenv("MINI_CLAW_FEISHU_VERIFICATION_TOKEN"),
			EncryptKey:        os.Getenv("MINI_CLAW_FEISHU_ENCRYPT_KEY"),
		},
	}
	cfg.Service = defaultServiceConfig()

	// API Key 是必需字段，不允许空值。
	if cfg.APIKey == "" {
		return Config{}, fmt.Errorf("MINI_CLAW_API_KEY is required")
	}

	// 如果用户未指定模型，使用默认模型名。
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}

	// timeoutSec 保存中间整数值，最后统一转换为 time.Duration。
	timeoutSec := defaultTimeoutSec
	if raw := os.Getenv("MINI_CLAW_TIMEOUT_SEC"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return Config{}, fmt.Errorf("MINI_CLAW_TIMEOUT_SEC must be a positive integer")
		}
		timeoutSec = v
	}

	cfg.Timeout = time.Duration(timeoutSec) * time.Second
	enabled, err := parseBoolEnv(os.Getenv("MINI_CLAW_FEISHU_ENABLED"))
	if err != nil {
		return Config{}, fmt.Errorf("MINI_CLAW_FEISHU_ENABLED: %w", err)
	}
	cfg.Feishu.Enabled = enabled

	if cfg.Feishu.AccountID == "" {
		cfg.Feishu.AccountID = "default"
	}
	if cfg.Feishu.Enabled {
		if strings.TrimSpace(cfg.Feishu.AppID) == "" {
			return Config{}, fmt.Errorf("MINI_CLAW_FEISHU_APP_ID is required when MINI_CLAW_FEISHU_ENABLED=true")
		}
		if strings.TrimSpace(cfg.Feishu.AppSecret) == "" {
			return Config{}, fmt.Errorf("MINI_CLAW_FEISHU_APP_SECRET is required when MINI_CLAW_FEISHU_ENABLED=true")
		}
	}

	if host := strings.TrimSpace(os.Getenv("MINI_CLAW_GATEWAY_HOST")); host != "" {
		cfg.Service.Gateway.Host = host
	}
	if rawPort := strings.TrimSpace(os.Getenv("MINI_CLAW_GATEWAY_PORT")); rawPort != "" {
		v, err := strconv.Atoi(rawPort)
		if err != nil || v <= 0 || v > 65535 {
			return Config{}, fmt.Errorf("MINI_CLAW_GATEWAY_PORT must be within (0,65535]")
		}
		cfg.Service.Gateway.Port = v
	}
	cfg.Service.Gateway.AuthToken = strings.TrimSpace(os.Getenv("MINI_CLAW_GATEWAY_AUTH_TOKEN"))
	if enabled, err := parseBoolEnv(os.Getenv("MINI_CLAW_GATEWAY_ENABLED")); err == nil && os.Getenv("MINI_CLAW_GATEWAY_ENABLED") != "" {
		cfg.Service.Gateway.Enabled = enabled
	}
	if dockerEnabled, err := parseBoolEnv(os.Getenv("MINI_CLAW_TOOLS_DOCKER_ENABLED")); err == nil {
		cfg.Service.Tools.EnableDocker = dockerEnabled
	}

	return cfg, nil
}

// WithModelOverride 返回带模型覆盖的新配置副本。
//
// 参数：
// - cfg: 原始配置
// - model: CLI 传入的模型覆盖值；空字符串表示不覆盖
//
// 返回：
// - Config: 覆盖后的配置（原配置按值传递，不会被外部修改）
func WithModelOverride(cfg Config, model string) Config {
	if model == "" {
		return cfg
	}
	cfg.Model = model
	return cfg
}

// parseBoolEnv 解析布尔环境变量，支持 1/0、true/false、yes/no、on/off（大小写不敏感）。
func parseBoolEnv(raw string) (bool, error) {
	v := strings.TrimSpace(strings.ToLower(raw))
	switch v {
	case "", "0", "false", "no", "off":
		return false, nil
	case "1", "true", "yes", "on":
		return true, nil
	default:
		return false, fmt.Errorf("invalid boolean value %q", raw)
	}
}

func defaultServiceConfig() ServiceConfig {
	return ServiceConfig{
		Channels: ManagerSection{},
		Gateway: GatewaySection{
			Enabled: true,
			Host: "127.0.0.1",
			Port: 18080,
		},
		Tools: ToolsSection{
			EnableDocker: false,
		},
		Security: SecuritySection{
			DefaultTimeoutSec: 20,
			MaxOutputBytes:    64 * 1024,
		},
		Session: SessionSection{
			MaxContextMessages: 80,
		},
		Cron: CronSection{
			Enabled: true,
		},
		Manager: ManagerSection{
			WorkerQueueSize: 64,
		},
	}
}
