package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// FeishuLiveConfig 描述飞书实时收发客户端初始化所需参数。
type FeishuLiveConfig struct {
	AppID             string
	AppSecret         string
	VerificationToken string
	EncryptKey        string
}

// NewFeishuLiveWSClient 创建基于飞书 WebSocket 事件流的 FeishuWSClient。
func NewFeishuLiveWSClient(cfg FeishuLiveConfig) (FeishuWSClient, error) {
	if strings.TrimSpace(cfg.AppID) == "" {
		return nil, fmt.Errorf("feishu app id is required")
	}
	if strings.TrimSpace(cfg.AppSecret) == "" {
		return nil, fmt.Errorf("feishu app secret is required")
	}

	client := &feishuLiveWSClient{
		cfg:    cfg,
		events: make(chan *FeishuEvent, 128),
		start:  make(chan error, 1),
	}
	return client, nil
}

// NewFeishuLiveSender 创建基于飞书 OpenAPI 的消息发送客户端。
func NewFeishuLiveSender(appID, appSecret string) (FeishuSender, error) {
	if strings.TrimSpace(appID) == "" {
		return nil, fmt.Errorf("feishu app id is required")
	}
	if strings.TrimSpace(appSecret) == "" {
		return nil, fmt.Errorf("feishu app secret is required")
	}
	return &feishuLiveSender{
		client: lark.NewClient(appID, appSecret),
	}, nil
}

type feishuLiveWSClient struct {
	cfg FeishuLiveConfig

	once   sync.Once
	events chan *FeishuEvent
	start  chan error
}

func (c *feishuLiveWSClient) Receive(ctx context.Context) (*FeishuEvent, error) {
	c.startOnce(ctx)

	select {
	case err := <-c.start:
		if err != nil {
			return nil, err
		}
	default:
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case evt := <-c.events:
		return evt, nil
	}
}

func (c *feishuLiveWSClient) startOnce(ctx context.Context) {
	c.once.Do(func() {
		handler := dispatcher.NewEventDispatcher(c.cfg.VerificationToken, c.cfg.EncryptKey)
		handler.OnP2MessageReceiveV1(func(_ context.Context, event *larkim.P2MessageReceiveV1) error {
			feishuEvent, ok := convertFeishuSDKEvent(event)
			if !ok {
				return nil
			}
			select {
			case c.events <- feishuEvent:
			default:
				// 通道满时丢弃旧事件，优先保留新消息。
				select {
				case <-c.events:
				default:
				}
				c.events <- feishuEvent
			}
			return nil
		})

		wsClient := larkws.NewClient(
			c.cfg.AppID,
			c.cfg.AppSecret,
			larkws.WithEventHandler(handler),
		)

		go func() {
			err := wsClient.Start(ctx)
			select {
			case c.start <- err:
			default:
			}
		}()
	})
}

type feishuLiveSender struct {
	client *lark.Client
}

func (s *feishuLiveSender) Send(ctx context.Context, req FeishuSendRequest) error {
	if s.client == nil {
		return fmt.Errorf("feishu sender client is nil")
	}
	if strings.TrimSpace(req.ChatID) == "" {
		return fmt.Errorf("feishu chat id is required")
	}
	if strings.TrimSpace(req.Text) == "" {
		return fmt.Errorf("feishu message text is empty")
	}

	textContent, err := json.Marshal(map[string]string{"text": req.Text})
	if err != nil {
		return fmt.Errorf("marshal feishu text content: %w", err)
	}

	if strings.TrimSpace(req.ReplyTo) != "" {
		replyBody := larkim.NewReplyMessageReqBodyBuilder().
			MsgType("text").
			Content(string(textContent)).
			ReplyInThread(strings.TrimSpace(req.ThreadID) != "").
			Uuid(buildFeishuMessageUUID()).
			Build()

		replyReq := larkim.NewReplyMessageReqBuilder().
			MessageId(req.ReplyTo).
			Body(replyBody).
			Build()

		resp, err := s.client.Im.V1.Message.Reply(ctx, replyReq)
		if err != nil {
			return fmt.Errorf("reply feishu message: %w", err)
		}
		if !resp.Success() {
			return fmt.Errorf("reply feishu message failed: code=%d msg=%s", resp.Code, resp.Msg)
		}
		return nil
	}

	createBody := larkim.NewCreateMessageReqBodyBuilder().
		ReceiveId(req.ChatID).
		MsgType("text").
		Content(string(textContent)).
		Uuid(buildFeishuMessageUUID()).
		Build()

	createReq := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(larkim.ReceiveIdTypeChatId).
		Body(createBody).
		Build()

	resp, err := s.client.Im.V1.Message.Create(ctx, createReq)
	if err != nil {
		return fmt.Errorf("create feishu message: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("create feishu message failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

func convertFeishuSDKEvent(event *larkim.P2MessageReceiveV1) (*FeishuEvent, bool) {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil, false
	}
	if isFeishuAppSender(event.Event.Sender) {
		return nil, false
	}

	msg := event.Event.Message
	if msg.MessageType != nil && *msg.MessageType != "text" {
		return nil, false
	}

	createdAt := time.Now()
	if msg.CreateTime != nil {
		if ms, err := strconv.ParseInt(*msg.CreateTime, 10, 64); err == nil {
			createdAt = time.UnixMilli(ms)
		}
	}

	return &FeishuEvent{
		Message: &FeishuMessage{
			MessageID: getStringPtr(msg.MessageId),
			SenderID:  extractFeishuSenderID(event.Event.Sender),
			ChatID:    getStringPtr(msg.ChatId),
			ChatType:  getStringPtr(msg.ChatType),
			RawJSON:   getStringPtr(msg.Content),
			ThreadID:  getStringPtr(msg.ThreadId),
			RootID:    getStringPtr(msg.RootId),
			CreatedAt: createdAt,
		},
	}, true
}

func isFeishuAppSender(sender *larkim.EventSender) bool {
	if sender == nil || sender.SenderType == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(*sender.SenderType), "app")
}

func extractFeishuSenderID(sender *larkim.EventSender) string {
	if sender == nil || sender.SenderId == nil {
		return ""
	}
	if sender.SenderId.OpenId != nil && *sender.SenderId.OpenId != "" {
		return *sender.SenderId.OpenId
	}
	if sender.SenderId.UserId != nil && *sender.SenderId.UserId != "" {
		return *sender.SenderId.UserId
	}
	if sender.SenderId.UnionId != nil && *sender.SenderId.UnionId != "" {
		return *sender.SenderId.UnionId
	}
	return ""
}

func getStringPtr(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func buildFeishuMessageUUID() string {
	return fmt.Sprintf("mini-claw-%d", time.Now().UnixNano())
}
