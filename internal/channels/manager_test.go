package channels

import (
	"context"
	"sync"
	"testing"
	"time"

	"mini-clawdbot/internal/bus"
)

type fakeAdapter struct {
	name    string
	recvCh  chan *bus.InboundMessage
	mu      sync.Mutex
	sent    []*bus.OutboundMessage
	started bool
	stopped bool
}

func newFakeAdapter(name string) *fakeAdapter {
	return &fakeAdapter{
		name:   name,
		recvCh: make(chan *bus.InboundMessage, 16),
		sent:   make([]*bus.OutboundMessage, 0),
	}
}

func (f *fakeAdapter) Name() string { return f.name }

func (f *fakeAdapter) Start(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
	return nil
}

func (f *fakeAdapter) Stop(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = true
	return nil
}

func (f *fakeAdapter) Send(_ context.Context, msg *bus.OutboundMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	return nil
}

func (f *fakeAdapter) Receive() <-chan *bus.InboundMessage {
	return f.recvCh
}

func (f *fakeAdapter) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func TestChannelManager_ForwardInboundAndDispatchOutbound(t *testing.T) {
	messageBus := bus.NewMessageBus(bus.Config{})
	t.Cleanup(func() { _ = messageBus.Close() })

	mgr := NewChannelManager(messageBus)
	adapterA := newFakeAdapter("telegram")
	adapterB := newFakeAdapter("telegram")

	if err := mgr.Register("telegram", "acc-a", adapterA); err != nil {
		t.Fatalf("register adapterA: %v", err)
	}
	if err := mgr.Register("telegram", "acc-b", adapterB); err != nil {
		t.Fatalf("register adapterB: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		_ = mgr.Stop(stopCtx)
	}()

	inSub, err := messageBus.SubscribeInbound()
	if err != nil {
		t.Fatalf("subscribe inbound: %v", err)
	}

	adapterA.recvCh <- &bus.InboundMessage{
		ChatID:   "chat-1",
		ThreadID: "thread-1",
		Content:  "hello",
	}

	received := mustRecv(t, inSub.Channel)
	if received.Channel != "telegram" || received.AccountID != "acc-a" {
		t.Fatalf("inbound normalization failed: %+v", received)
	}
	if got, want := string(received.SessionKey()), "telegram/acc-a/chat-1/thread-1"; got != want {
		t.Fatalf("unexpected inbound session key: got=%q want=%q", got, want)
	}

	if err := messageBus.PublishOutbound(context.Background(), &bus.OutboundMessage{
		Channel:   "telegram",
		AccountID: "acc-b",
		ChatID:    "chat-2",
		Content:   "reply",
	}); err != nil {
		t.Fatalf("publish outbound: %v", err)
	}

	waitUntil(t, time.Second, func() bool {
		return adapterB.sentCount() == 1
	})
	if adapterA.sentCount() != 0 {
		t.Fatalf("expected adapterA not to receive outbound, got=%d", adapterA.sentCount())
	}
}

func TestChannelManager_MissingAdapterPublishesError(t *testing.T) {
	messageBus := bus.NewMessageBus(bus.Config{})
	t.Cleanup(func() { _ = messageBus.Close() })

	mgr := NewChannelManager(messageBus)
	adapter := newFakeAdapter("telegram")
	if err := mgr.Register("telegram", "acc-a", adapter); err != nil {
		t.Fatalf("register adapter: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		_ = mgr.Stop(stopCtx)
	}()

	errSub, err := messageBus.SubscribeError()
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}

	if err := messageBus.PublishOutbound(context.Background(), &bus.OutboundMessage{
		Channel:   "telegram",
		AccountID: "acc-missing",
		ChatID:    "chat-x",
		Content:   "reply",
	}); err != nil {
		t.Fatalf("publish outbound: %v", err)
	}

	got := mustRecv(t, errSub.Channel)
	if got.Code != "adapter_not_found" {
		t.Fatalf("unexpected error code: %s", got.Code)
	}
}

func mustRecv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting channel event")
		var zero T
		return zero
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached before timeout")
}
