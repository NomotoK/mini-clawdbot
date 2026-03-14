package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"mini-clawdbot/internal/bus"
	"mini-clawdbot/internal/channels"
	"mini-clawdbot/internal/session"
)

type historyAwareToolModel struct{}

func (m *historyAwareToolModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *historyAwareToolModel) Generate(_ context.Context, input []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	if len(input) == 0 {
		return nil, fmt.Errorf("empty input")
	}
	last := input[len(input)-1]
	userCount := 0
	for _, msg := range input {
		if msg != nil && msg.Role == schema.User {
			userCount++
		}
	}

	if last.Role == schema.Tool {
		return schema.AssistantMessage(fmt.Sprintf("users=%d; done:%s", userCount, last.Content), nil), nil
	}

	if last.Role == schema.User {
		return schema.AssistantMessage("", []schema.ToolCall{{
			ID:   "call-readme",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "read_file",
				Arguments: `{"path":"README.md"}`,
			},
		}}), nil
	}
	return schema.AssistantMessage("unhandled", nil), nil
}

func (m *historyAwareToolModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

type fakeTelegramPoller struct {
	ch chan []channels.TelegramUpdate
}

func newFakeTelegramPoller() *fakeTelegramPoller {
	return &fakeTelegramPoller{ch: make(chan []channels.TelegramUpdate, 16)}
}

func (p *fakeTelegramPoller) Poll(ctx context.Context) ([]channels.TelegramUpdate, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case updates := <-p.ch:
		return updates, nil
	}
}

func (p *fakeTelegramPoller) push(msg channels.TelegramMessage) {
	p.ch <- []channels.TelegramUpdate{{Message: &msg}}
}

type fakeFeishuWS struct {
	ch chan *channels.FeishuEvent
}

func newFakeFeishuWS() *fakeFeishuWS {
	return &fakeFeishuWS{ch: make(chan *channels.FeishuEvent, 16)}
}

func (w *fakeFeishuWS) Receive(ctx context.Context) (*channels.FeishuEvent, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case evt := <-w.ch:
		return evt, nil
	}
}

func (w *fakeFeishuWS) push(evt channels.FeishuEvent) {
	w.ch <- &evt
}

type fakeTelegramSender struct {
	mu   sync.Mutex
	logs []channels.TelegramSendRequest
	ch   chan channels.TelegramSendRequest
}

func newFakeTelegramSender() *fakeTelegramSender {
	return &fakeTelegramSender{
		logs: make([]channels.TelegramSendRequest, 0),
		ch:   make(chan channels.TelegramSendRequest, 16),
	}
}

func (s *fakeTelegramSender) Send(_ context.Context, req channels.TelegramSendRequest) error {
	s.mu.Lock()
	s.logs = append(s.logs, req)
	s.mu.Unlock()
	s.ch <- req
	return nil
}

type fakeFeishuSender struct {
	mu   sync.Mutex
	logs []channels.FeishuSendRequest
	ch   chan channels.FeishuSendRequest
}

func newFakeFeishuSender() *fakeFeishuSender {
	return &fakeFeishuSender{
		logs: make([]channels.FeishuSendRequest, 0),
		ch:   make(chan channels.FeishuSendRequest, 16),
	}
}

func (s *fakeFeishuSender) Send(_ context.Context, req channels.FeishuSendRequest) error {
	s.mu.Lock()
	s.logs = append(s.logs, req)
	s.mu.Unlock()
	s.ch <- req
	return nil
}

func TestServe_FullChainWithRestartRecovery(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello-from-jsonl"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	app1, err := NewWithDependencies(context.Background(), &historyAwareToolModel{}, 6, root)
	if err != nil {
		t.Fatalf("new app1: %v", err)
	}

	tgPoller := newFakeTelegramPoller()
	tgSender := newFakeTelegramSender()
	if err := app1.RegisterTelegramChannel("tg-acc", tgPoller, tgSender); err != nil {
		t.Fatalf("register telegram: %v", err)
	}

	fsWS := newFakeFeishuWS()
	fsSender := newFakeFeishuSender()
	if err := app1.RegisterFeishuChannel("fs-acc", fsWS, fsSender); err != nil {
		t.Fatalf("register feishu: %v", err)
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	serveDone1 := make(chan error, 1)
	go func() { serveDone1 <- app1.Serve(ctx1) }()

	time.Sleep(120 * time.Millisecond)

	tgPoller.push(channels.TelegramMessage{
		MessageID:       1,
		FromID:          11,
		ChatID:          10001,
		MessageThreadID: 77,
		Text:            "telegram first",
	})
	tgReply1 := mustRecvTelegram(t, tgSender.ch)
	if !strings.Contains(tgReply1.Text, "users=1") {
		t.Fatalf("unexpected telegram first reply: %q", tgReply1.Text)
	}
	if tgReply1.ChatID != 10001 || tgReply1.MessageThreadID != 77 {
		t.Fatalf("telegram reply not routed to origin session: %+v", tgReply1)
	}

	fsWS.push(channels.FeishuEvent{
		Message: &channels.FeishuMessage{
			MessageID: "fs-1",
			SenderID:  "u-1",
			ChatID:    "oc_test",
			RawJSON:   `{"text":"feishu first"}`,
			RootID:    "root-42",
		},
	})
	fsReply1 := mustRecvFeishu(t, fsSender.ch)
	if !strings.Contains(fsReply1.Text, "users=1") {
		t.Fatalf("unexpected feishu first reply: %q", fsReply1.Text)
	}
	if fsReply1.ChatID != "oc_test" || fsReply1.ThreadID != "root-42" {
		t.Fatalf("feishu reply not routed to origin session: %+v", fsReply1)
	}

	tgKey := bus.NewSessionKey("telegram", "tg-acc", "10001", "77")
	tgTypes, err := collectSessionEventTypes(root, tgKey)
	if err != nil {
		t.Fatalf("collect session event types: %v", err)
	}
	required := []string{
		session.EventInboundReceived,
		session.EventToolCall,
		session.EventToolResult,
		session.EventLLMAssistant,
		session.EventOutboundSent,
	}
	for _, evt := range required {
		if tgTypes[evt] == 0 {
			t.Fatalf("missing event %q in jsonl chain, got %+v", evt, tgTypes)
		}
	}

	cancel1()
	if err := <-serveDone1; err != nil {
		t.Fatalf("serve1 returned error: %v", err)
	}

	app2, err := NewWithDependencies(context.Background(), &historyAwareToolModel{}, 6, root)
	if err != nil {
		t.Fatalf("new app2: %v", err)
	}

	tgPoller2 := newFakeTelegramPoller()
	tgSender2 := newFakeTelegramSender()
	if err := app2.RegisterTelegramChannel("tg-acc", tgPoller2, tgSender2); err != nil {
		t.Fatalf("register telegram on restart: %v", err)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	serveDone2 := make(chan error, 1)
	go func() { serveDone2 <- app2.Serve(ctx2) }()
	time.Sleep(120 * time.Millisecond)

	tgPoller2.push(channels.TelegramMessage{
		MessageID:       2,
		FromID:          11,
		ChatID:          10001,
		MessageThreadID: 77,
		Text:            "telegram second",
	})
	tgReply2 := mustRecvTelegram(t, tgSender2.ch)
	if !strings.Contains(tgReply2.Text, "users=2") {
		t.Fatalf("expected recovered context users=2, got %q", tgReply2.Text)
	}

	cancel2()
	if err := <-serveDone2; err != nil {
		t.Fatalf("serve2 returned error: %v", err)
	}
}

func mustRecvTelegram(t *testing.T, ch <-chan channels.TelegramSendRequest) channels.TelegramSendRequest {
	t.Helper()
	select {
	case req := <-ch:
		return req
	case <-time.After(4 * time.Second):
		t.Fatal("timeout waiting telegram outbound")
		return channels.TelegramSendRequest{}
	}
}

func mustRecvFeishu(t *testing.T, ch <-chan channels.FeishuSendRequest) channels.FeishuSendRequest {
	t.Helper()
	select {
	case req := <-ch:
		return req
	case <-time.After(4 * time.Second):
		t.Fatal("timeout waiting feishu outbound")
		return channels.FeishuSendRequest{}
	}
}

func collectSessionEventTypes(root string, key bus.SessionKey) (map[string]int, error) {
	result := make(map[string]int)
	sessionDir := filepath.Join(root, "data", "sessions")
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(sessionDir, entry.Name())
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var rec session.EventRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				_ = f.Close()
				return nil, err
			}
			if rec.SessionKey == string(key) {
				result[rec.EventType]++
			}
		}
		if err := scanner.Err(); err != nil {
			_ = f.Close()
			return nil, err
		}
		_ = f.Close()
	}
	return result, nil
}
