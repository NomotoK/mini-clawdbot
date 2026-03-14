package channels

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"mini-clawdbot/internal/bus"
)

// TelegramPoller 抽象 Telegram long polling 客户端。
type TelegramPoller interface {
	Poll(ctx context.Context) ([]TelegramUpdate, error)
}

// TelegramSender 抽象 Telegram 发送客户端。
type TelegramSender interface {
	Send(ctx context.Context, req TelegramSendRequest) error
}

// TelegramUpdate 是 long polling 返回的更新事件。
type TelegramUpdate struct {
	Message *TelegramMessage
}

// TelegramMessage 是 Telegram 消息最小字段集合。
type TelegramMessage struct {
	MessageID       int64
	FromID          int64
	ChatID          int64
	Text            string
	Caption         string
	MessageThreadID int64
}

// TelegramSendRequest 是 Telegram 发送消息请求。
type TelegramSendRequest struct {
	ChatID          int64
	MessageThreadID int64
	ReplyTo         string
	Text            string
}

// TelegramAdapter 实现 ChannelAdapter。
type TelegramAdapter struct {
	accountID string
	poller    TelegramPoller
	sender    TelegramSender

	recv chan *bus.InboundMessage
}

// NewTelegramAdapter 创建 Telegram adapter。
func NewTelegramAdapter(accountID string, poller TelegramPoller, sender TelegramSender) *TelegramAdapter {
	return &TelegramAdapter{
		accountID: normalizeAccountID(accountID),
		poller:    poller,
		sender:    sender,
		recv:      make(chan *bus.InboundMessage, 64),
	}
}

func (a *TelegramAdapter) Name() string {
	return "telegram"
}

func (a *TelegramAdapter) Start(ctx context.Context) error {
	if a.poller == nil {
		return fmt.Errorf("telegram poller is nil")
	}
	go a.pollLoop(ctx)
	return nil
}

func (a *TelegramAdapter) Stop(context.Context) error {
	return nil
}

func (a *TelegramAdapter) Send(ctx context.Context, msg *bus.OutboundMessage) error {
	if a.sender == nil {
		return fmt.Errorf("telegram sender is nil")
	}
	chatID, err := strconv.ParseInt(msg.ChatID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid telegram chat_id %q: %w", msg.ChatID, err)
	}
	threadID, _ := strconv.ParseInt(msg.ThreadID, 10, 64)
	return a.sender.Send(ctx, TelegramSendRequest{
		ChatID:          chatID,
		MessageThreadID: threadID,
		ReplyTo:         msg.ReplyTo,
		Text:            msg.Content,
	})
}

func (a *TelegramAdapter) Receive() <-chan *bus.InboundMessage {
	return a.recv
}
// pollLoop 是 Telegram adapter 的主循环，持续轮询 Telegram 更新并转化为 InboundMessage 发布到 recv 通道。
func (a *TelegramAdapter) pollLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		updates, err := a.poller.Poll(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
			continue
		}

		for i := range updates {
			msg, ok := convertTelegramInbound(a.accountID, updates[i])
			if !ok {
				continue
			}

			select {
			case <-ctx.Done():
				return
			case a.recv <- msg:
			}
		}
	}
}
// convertTelegramInbound 将 TelegramUpdate 转换为 InboundMessage，提取必要字段并规范化。
func convertTelegramInbound(accountID string, update TelegramUpdate) (*bus.InboundMessage, bool) {
	if update.Message == nil {
		return nil, false
	}
	content := update.Message.Text
	if content == "" {
		content = update.Message.Caption
	}
	if content == "" {
		return nil, false
	}

	threadID := ""
	if update.Message.MessageThreadID > 0 {
		threadID = strconv.FormatInt(update.Message.MessageThreadID, 10)
	}

	return &bus.InboundMessage{
		Channel:   "telegram",
		AccountID: normalizeAccountID(accountID),
		SenderID:  strconv.FormatInt(update.Message.FromID, 10),
		ChatID:    strconv.FormatInt(update.Message.ChatID, 10),
		ThreadID:  threadID,
		Content:   content,
		Metadata: map[string]any{
			"message_id": update.Message.MessageID,
		},
	}, true
}
