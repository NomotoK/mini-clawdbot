package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"mini-clawdbot/internal/bus"
)

// FeishuWSClient 抽象飞书 WebSocket 事件源。
type FeishuWSClient interface {
	Receive(ctx context.Context) (*FeishuEvent, error)
}

// FeishuSender 抽象飞书消息发送端。
type FeishuSender interface {
	Send(ctx context.Context, req FeishuSendRequest) error
}

// FeishuEvent 表示飞书消息事件最小结构。
type FeishuEvent struct {
	Message *FeishuMessage
}

// FeishuMessage 是飞书消息最小字段集合。
type FeishuMessage struct {
	MessageID string
	SenderID  string
	ChatID    string
	ChatType  string
	Text      string
	RawJSON   string
	ThreadID  string
	RootID    string
	CreatedAt time.Time
}

// FeishuSendRequest 是发送到飞书的最小请求结构。
type FeishuSendRequest struct {
	ChatID   string
	ThreadID string
	ReplyTo  string
	Text     string
}

// FeishuAdapter 实现 ChannelAdapter。
type FeishuAdapter struct {
	accountID string
	wsClient  FeishuWSClient
	sender    FeishuSender

	recv chan *bus.InboundMessage
}

// NewFeishuAdapter 创建飞书 adapter。
func NewFeishuAdapter(accountID string, wsClient FeishuWSClient, sender FeishuSender) *FeishuAdapter {
	return &FeishuAdapter{
		accountID: normalizeAccountID(accountID),
		wsClient:  wsClient,
		sender:    sender,
		recv:      make(chan *bus.InboundMessage, 64),
	}
}

func (a *FeishuAdapter) Name() string {
	return "feishu"
}

func (a *FeishuAdapter) Start(ctx context.Context) error {
	if a.wsClient == nil {
		return fmt.Errorf("feishu ws client is nil")
	}
	go a.wsLoop(ctx)
	return nil
}

func (a *FeishuAdapter) Stop(context.Context) error {
	return nil
}

func (a *FeishuAdapter) Send(ctx context.Context, msg *bus.OutboundMessage) error {
	if a.sender == nil {
		return fmt.Errorf("feishu sender is nil")
	}
	return a.sender.Send(ctx, FeishuSendRequest{
		ChatID:   msg.ChatID,
		ThreadID: msg.ThreadID,
		ReplyTo:  msg.ReplyTo,
		Text:     msg.Content,
	})
}

func (a *FeishuAdapter) Receive() <-chan *bus.InboundMessage {
	return a.recv
}

func (a *FeishuAdapter) wsLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		evt, err := a.wsClient.Receive(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
			continue
		}
		msg, ok := convertFeishuInbound(a.accountID, evt)
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

func convertFeishuInbound(accountID string, evt *FeishuEvent) (*bus.InboundMessage, bool) {
	if evt == nil || evt.Message == nil {
		return nil, false
	}
	content := evt.Message.Text
	if content == "" && evt.Message.RawJSON != "" {
		var textPayload struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(evt.Message.RawJSON), &textPayload); err == nil {
			content = textPayload.Text
		}
	}
	if content == "" {
		return nil, false
	}

	threadID := evt.Message.ThreadID
	if threadID == "" {
		threadID = evt.Message.RootID
	}

	return &bus.InboundMessage{
		Channel:   "feishu",
		AccountID: normalizeAccountID(accountID),
		SenderID:  evt.Message.SenderID,
		ChatID:    evt.Message.ChatID,
		ThreadID:  threadID,
		Content:   content,
		Timestamp: evt.Message.CreatedAt,
		Metadata: map[string]any{
			"message_id": evt.Message.MessageID,
			"chat_type":  evt.Message.ChatType,
		},
	}, true
}
