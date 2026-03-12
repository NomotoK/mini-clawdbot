package router

import (
	"testing"

	"mini-clawdbot/internal/bus"
)

func TestSessionRouter_RouteInbound_WithThread(t *testing.T) {
	r := NewSessionRouter()
	msg := &bus.InboundMessage{
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "chat-100",
		ThreadID:  "thread-9",
	}

	got := r.RouteInbound(msg)
	want := bus.NewSessionKey("telegram", "acc-1", "chat-100", "thread-9")
	if got != want {
		t.Fatalf("unexpected session key: got=%q want=%q", got, want)
	}
}

func TestSessionRouter_RouteInbound_WithoutThreadFallbackRoot(t *testing.T) {
	r := NewSessionRouter()
	msg := &bus.InboundMessage{
		Channel:   "feishu",
		AccountID: "acc-2",
		ChatID:    "chat-x",
		ThreadID:  "",
	}

	got := r.RouteInbound(msg)
	want := bus.NewSessionKey("feishu", "acc-2", "chat-x", "")
	if got != want {
		t.Fatalf("unexpected session key fallback: got=%q want=%q", got, want)
	}
}

func TestSessionRouter_RouteOutbound_WithThreadAndFallback(t *testing.T) {
	r := NewSessionRouter()

	withThread := &bus.OutboundMessage{
		Channel:   "telegram",
		AccountID: "acc-3",
		ChatID:    "chat-y",
		ThreadID:  "15",
	}
	gotWithThread := r.RouteOutbound(withThread)
	wantWithThread := bus.NewSessionKey("telegram", "acc-3", "chat-y", "15")
	if gotWithThread != wantWithThread {
		t.Fatalf("unexpected outbound session key with thread: got=%q want=%q", gotWithThread, wantWithThread)
	}

	withoutThread := &bus.OutboundMessage{
		Channel:   "telegram",
		AccountID: "acc-3",
		ChatID:    "chat-y",
	}
	gotWithoutThread := r.RouteOutbound(withoutThread)
	wantWithoutThread := bus.NewSessionKey("telegram", "acc-3", "chat-y", "")
	if gotWithoutThread != wantWithoutThread {
		t.Fatalf("unexpected outbound session key fallback: got=%q want=%q", gotWithoutThread, wantWithoutThread)
	}
}
