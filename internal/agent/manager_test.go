package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"mini-clawdbot/internal/bus"
	"mini-clawdbot/internal/router"
	"mini-clawdbot/internal/session"
	"mini-clawdbot/internal/tools"
)

type echoModel struct{}

func (m *echoModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *echoModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("empty input")
	}
	last := input[len(input)-1]
	return schema.AssistantMessage("echo:"+last.Content, nil), nil
}

func (m *echoModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func newTestRunner(t *testing.T) *ReactRunner {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
	toolList, err := tools.BuildMVPTools(root)
	if err != nil {
		t.Fatalf("build tools: %v", err)
	}
	r, err := NewReactRunner(context.Background(), &echoModel{}, toolList, 6)
	if err != nil {
		t.Fatalf("new react runner: %v", err)
	}
	return r
}

func TestAgentManager_ProcessInboundToOutbound(t *testing.T) {
	messageBus := bus.NewMessageBus(bus.Config{})
	t.Cleanup(func() { _ = messageBus.Close() })

	mgr := NewAgentManager(
		messageBus,
		router.NewSessionRouter(),
		newTestRunner(t),
		session.NewMemoryStore(),
		ManagerConfig{WorkerQueueSize: 8, WorkerIdleTTL: time.Second},
	)

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

	outSub, err := messageBus.SubscribeOutbound()
	if err != nil {
		t.Fatalf("subscribe outbound: %v", err)
	}

	inbound := &bus.InboundMessage{
		EventID:   "in-1",
		TraceID:   "trace-1",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "chat-1",
		ThreadID:  "thread-7",
		Content:   "ping",
	}
	if err := messageBus.PublishInbound(context.Background(), inbound); err != nil {
		t.Fatalf("publish inbound: %v", err)
	}

	out := mustRecvOutbound(t, outSub.Channel)
	if out.Channel != inbound.Channel || out.AccountID != inbound.AccountID || out.ChatID != inbound.ChatID || out.ThreadID != inbound.ThreadID {
		t.Fatalf("outbound routing mismatch: %+v", out)
	}
	if out.ReplyTo != inbound.EventID {
		t.Fatalf("unexpected reply_to: %q", out.ReplyTo)
	}
	if !strings.Contains(out.Content, "echo:ping") {
		t.Fatalf("unexpected outbound content: %q", out.Content)
	}
}

func TestAgentManager_WorkerIdleTTLReclaim(t *testing.T) {
	messageBus := bus.NewMessageBus(bus.Config{})
	t.Cleanup(func() { _ = messageBus.Close() })

	mgr := NewAgentManager(
		messageBus,
		router.NewSessionRouter(),
		newTestRunner(t),
		session.NewMemoryStore(),
		ManagerConfig{WorkerQueueSize: 8, WorkerIdleTTL: 60 * time.Millisecond},
	)

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

	if err := messageBus.PublishInbound(context.Background(), &bus.InboundMessage{
		EventID:   "in-ttl",
		Channel:   "telegram",
		AccountID: "acc-1",
		ChatID:    "chat-ttl",
		Content:   "hello",
	}); err != nil {
		t.Fatalf("publish inbound: %v", err)
	}

	waitUntil(t, time.Second, func() bool {
		return mgr.workerCount() >= 1
	})
	waitUntil(t, 2*time.Second, func() bool {
		return mgr.workerCount() == 0
	})
}

func mustRecvOutbound(t *testing.T, ch <-chan *bus.OutboundMessage) *bus.OutboundMessage {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting outbound")
		return nil
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
