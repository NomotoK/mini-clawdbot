package llm

import (
	"context"
	"fmt"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"mini-clawdbot/internal/config"
)

// NewToolCallingModel 使用 OpenAI 兼容配置初始化 Eino ToolCallingChatModel。
//
// 参数：
// - ctx: 初始化过程使用的上下文
// - cfg: 已校验配置（APIKey、BaseURL、Model、Timeout）
//
// 返回：
// - model.ToolCallingChatModel: 可直接用于 ReAct Agent 的模型实例
// - error: 初始化失败时返回（例如鉴权失败、地址无效等）
func NewToolCallingModel(ctx context.Context, cfg config.Config) (model.ToolCallingChatModel, error) {
	chatModel, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
		Timeout: cfg.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("init openai model: %w", err)
	}
	return chatModel, nil
}
