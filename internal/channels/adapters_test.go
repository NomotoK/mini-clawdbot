package channels

import (
	"testing"

	"mini-clawdbot/internal/bus"
)

func TestConvertTelegramInbound_SessionKeyMapping(t *testing.T) {
	withThread, ok := convertTelegramInbound("acc-1", TelegramUpdate{
		Message: &TelegramMessage{
			MessageID:       10,
			FromID:          100,
			ChatID:          200,
			Text:            "hello",
			MessageThreadID: 88,
		},
	})
	if !ok {
		t.Fatal("expected telegram inbound converted")
	}
	if got, want := string(withThread.SessionKey()), "telegram/acc-1/200/88"; got != want {
		t.Fatalf("unexpected session key with thread: got=%q want=%q", got, want)
	}

	noThread, ok := convertTelegramInbound("acc-1", TelegramUpdate{
		Message: &TelegramMessage{
			MessageID: 11,
			FromID:    100,
			ChatID:    200,
			Text:      "hello again",
		},
	})
	if !ok {
		t.Fatal("expected telegram inbound converted without thread")
	}
	if got, want := string(noThread.SessionKey()), "telegram/acc-1/200/root"; got != want {
		t.Fatalf("unexpected session key without thread: got=%q want=%q", got, want)
	}
}

func TestConvertFeishuInbound_SessionKeyMapping(t *testing.T) {
	withRoot, ok := convertFeishuInbound("acc-2", &FeishuEvent{
		Message: &FeishuMessage{
			MessageID: "m-1",
			SenderID:  "u-1",
			ChatID:    "c-1",
			RawJSON:   `{"text":"hi from root"}`,
			RootID:    "root-thread-1",
		},
	})
	if !ok {
		t.Fatal("expected feishu inbound converted")
	}
	if got, want := withRoot.ThreadID, "root-thread-1"; got != want {
		t.Fatalf("unexpected thread fallback from root: got=%q want=%q", got, want)
	}
	if got, want := string(withRoot.SessionKey()), "feishu/acc-2/c-1/root-thread-1"; got != want {
		t.Fatalf("unexpected session key with root thread: got=%q want=%q", got, want)
	}

	noThread, ok := convertFeishuInbound("acc-2", &FeishuEvent{
		Message: &FeishuMessage{
			MessageID: "m-2",
			SenderID:  "u-1",
			ChatID:    "c-1",
			Text:      "plain text",
		},
	})
	if !ok {
		t.Fatal("expected feishu inbound converted without thread")
	}
	if got, want := string(noThread.SessionKey()), string(bus.NewSessionKey("feishu", "acc-2", "c-1", "")); got != want {
		t.Fatalf("unexpected session key without thread: got=%q want=%q", got, want)
	}
}
