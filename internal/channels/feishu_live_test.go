package channels

import (
	"testing"
	"time"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestConvertFeishuSDKEvent_TextMessage(t *testing.T) {
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId: strPtr("ou_test_open"),
				},
			},
			Message: &larkim.EventMessage{
				MessageId:   strPtr("om_123"),
				ChatId:      strPtr("oc_456"),
				ThreadId:    strPtr("omt_789"),
				RootId:      strPtr("om_root"),
				ChatType:    strPtr("group"),
				MessageType: strPtr("text"),
				Content:     strPtr(`{"text":"hello"}`),
				CreateTime:  strPtr("1700000000000"),
			},
		},
	}

	got, ok := convertFeishuSDKEvent(event)
	if !ok {
		t.Fatal("expected convert success")
	}
	if got.Message == nil {
		t.Fatal("expected message not nil")
	}
	if got.Message.MessageID != "om_123" || got.Message.ChatID != "oc_456" {
		t.Fatalf("unexpected mapped ids: %+v", got.Message)
	}
	if got.Message.SenderID != "ou_test_open" {
		t.Fatalf("unexpected sender id: %q", got.Message.SenderID)
	}
	if got.Message.RawJSON != `{"text":"hello"}` {
		t.Fatalf("unexpected content: %q", got.Message.RawJSON)
	}
	if got.Message.CreatedAt.Before(time.UnixMilli(1700000000000)) {
		t.Fatalf("unexpected create time: %v", got.Message.CreatedAt)
	}
}

func TestConvertFeishuSDKEvent_NonTextSkipped(t *testing.T) {
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Message: &larkim.EventMessage{
				MessageType: strPtr("image"),
			},
		},
	}
	if _, ok := convertFeishuSDKEvent(event); ok {
		t.Fatal("non-text event should be ignored")
	}
}

func TestConvertFeishuSDKEvent_AppSenderSkipped(t *testing.T) {
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderType: strPtr("app"),
			},
			Message: &larkim.EventMessage{
				MessageType: strPtr("text"),
				Content:     strPtr(`{"text":"hello"}`),
			},
		},
	}

	if _, ok := convertFeishuSDKEvent(event); ok {
		t.Fatal("app sender event should be ignored")
	}
}

func TestExtractFeishuSenderIDPriority(t *testing.T) {
	sender := &larkim.EventSender{
		SenderId: &larkim.UserId{
			OpenId:  strPtr("open"),
			UserId:  strPtr("user"),
			UnionId: strPtr("union"),
		},
	}
	if got := extractFeishuSenderID(sender); got != "open" {
		t.Fatalf("expected open id priority, got %q", got)
	}
}

func strPtr(v string) *string {
	return &v
}
