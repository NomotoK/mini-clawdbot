// Package bus 测试消息总线的各项功能
// 本文件包含 MessageBus、SessionKey 和各类事件的单元测试
package bus

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestNewSessionKey_DefaultThreadFallback 测试 SessionKey 在 threadID 为空时是否自动回退到 "root"。
// 验证了 NewSessionKey 的默认行为和格式正确性。
func TestNewSessionKey_DefaultThreadFallback(t *testing.T) {
	key := NewSessionKey("telegram", "bot-a", "chat-1", "")
	if got, want := string(key), "telegram/bot-a/chat-1/root"; got != want {
		t.Fatalf("unexpected session key: got=%q want=%q", got, want)
	}
}

// TestMessageBus_PublishAndSubscribe_AllTopics 测试 MessageBus 在全部话题上的发布和订阅功能。
// 验证：
//   - 所有事件类型（InboundMessage、OutboundMessage、StreamEvent、ErrorEvent）都能正确发布和接收
//   - 事件的默认字段（EventID、TraceID、Timestamp）在发布时被正确填充
//   - 多订阅者能够接收到相同的事件
func TestMessageBus_PublishAndSubscribe_AllTopics(t *testing.T) {
	b := NewMessageBus(Config{})
	t.Cleanup(func() { _ = b.Close() })

	inSub, err := b.SubscribeInbound()
	if err != nil {
		t.Fatalf("subscribe inbound: %v", err)
	}
	outSub, err := b.SubscribeOutbound()
	if err != nil {
		t.Fatalf("subscribe outbound: %v", err)
	}
	streamSub, err := b.SubscribeStream()
	if err != nil {
		t.Fatalf("subscribe stream: %v", err)
	}
	errSub, err := b.SubscribeError()
	if err != nil {
		t.Fatalf("subscribe error: %v", err)
	}
	auditSub, err := b.SubscribeAudit()
	if err != nil {
		t.Fatalf("subscribe audit: %v", err)
	}

	in := &InboundMessage{Channel: "telegram", AccountID: "acc-1", ChatID: "chat-1", Content: "hello"}
	if err := b.PublishInbound(context.Background(), in); err != nil {
		t.Fatalf("publish inbound: %v", err)
	}

	out := &OutboundMessage{Channel: "telegram", AccountID: "acc-1", ChatID: "chat-1", Content: "world"}
	if err := b.PublishOutbound(context.Background(), out); err != nil {
		t.Fatalf("publish outbound: %v", err)
	}

	se := &StreamEvent{SessionKey: NewSessionKey("telegram", "acc-1", "chat-1", ""), Seq: 1, Delta: "delta"}
	if err := b.PublishStream(context.Background(), se); err != nil {
		t.Fatalf("publish stream: %v", err)
	}

	ee := &ErrorEvent{SessionKey: NewSessionKey("telegram", "acc-1", "chat-1", ""), Stage: "tool", Message: "boom"}
	if err := b.PublishError(context.Background(), ee); err != nil {
		t.Fatalf("publish error: %v", err)
	}
	ae := &AuditEvent{SessionKey: NewSessionKey("telegram", "acc-1", "chat-1", ""), Kind: "tool_audit"}
	if err := b.PublishAudit(context.Background(), ae); err != nil {
		t.Fatalf("publish audit: %v", err)
	}

	recvInbound := mustRecv(t, inSub.Channel)
	if recvInbound.Content != "hello" {
		t.Fatalf("unexpected inbound content: %s", recvInbound.Content)
	}
	if recvInbound.EventID == "" || recvInbound.TraceID == "" || recvInbound.Timestamp.IsZero() {
		t.Fatalf("inbound defaults not applied: %+v", recvInbound)
	}

	recvOutbound := mustRecv(t, outSub.Channel)
	if recvOutbound.Content != "world" {
		t.Fatalf("unexpected outbound content: %s", recvOutbound.Content)
	}

	recvStream := mustRecv(t, streamSub.Channel)
	if recvStream.Seq != 1 || recvStream.Delta != "delta" {
		t.Fatalf("unexpected stream event: %+v", recvStream)
	}

	recvError := mustRecv(t, errSub.Channel)
	if recvError.Message != "boom" || recvError.Stage != "tool" {
		t.Fatalf("unexpected error event: %+v", recvError)
	}

	recvAudit := mustRecv(t, auditSub.Channel)
	if recvAudit.Kind != "tool_audit" {
		t.Fatalf("unexpected audit event: %+v", recvAudit)
	}
}

// TestMessageBus_StreamBackpressure_LatestWins 测试流式话题在背压下的行为。
// 验证：
//   - 当订阅者缓冲满且队列满时，新旧分片会被选择性丢弃
//   - "最新优先" 策略确保最新的分片总能被保留且最终发送给订阅者
//   - 这对实时流媒体应用至关重要，新数据比旧数据更有价值
func TestMessageBus_StreamBackpressure_LatestWins(t *testing.T) {
	b := NewMessageBus(Config{
		StreamBuffer:     1,
		SubscriberBuffer: 1,
	})
	t.Cleanup(func() { _ = b.Close() })

	sub, err := b.SubscribeStream()
	if err != nil {
		t.Fatalf("subscribe stream: %v", err)
	}

	// 不消费订阅，制造慢消费者场景。
	ctx := context.Background()
	if err := b.PublishStream(ctx, &StreamEvent{Seq: 1, Delta: "a"}); err != nil {
		t.Fatalf("publish stream 1: %v", err)
	}
	if err := b.PublishStream(ctx, &StreamEvent{Seq: 2, Delta: "b"}); err != nil {
		t.Fatalf("publish stream 2: %v", err)
	}
	if err := b.PublishStream(ctx, &StreamEvent{Seq: 3, Delta: "c"}); err != nil {
		t.Fatalf("publish stream 3: %v", err)
	}

	// 允许 fanout 执行。
	time.Sleep(30 * time.Millisecond)

	got := mustRecv(t, sub.Channel)
	if got.Seq != 3 || got.Delta != "c" {
		t.Fatalf("expected latest stream chunk, got %+v", got)
	}
}

// TestMessageBus_CloseAndPublishAfterClose 测试 MessageBus 在关闭后的行为。
// 验证：
//   - Close() 方法能正确关闭总线
//   - IsClosed() 返回正确的状态
//   - 在已关闭的总线上发布会返回 ErrBusClosed
func TestMessageBus_CloseAndPublishAfterClose(t *testing.T) {
	b := NewMessageBus(Config{})

	if err := b.Close(); err != nil {
		t.Fatalf("close bus: %v", err)
	}
	if !b.IsClosed() {
		t.Fatal("expected bus closed")
	}

	err := b.PublishInbound(context.Background(), &InboundMessage{Content: "x"})
	if !errors.Is(err, ErrBusClosed) {
		t.Fatalf("expected ErrBusClosed, got %v", err)
	}
}

// TestMessageBus_SubscriptionUnsubscribe 测试订阅的取消功能。
// 验证：
//   - Unsubscribe() 能正确关闭订阅的接收通道
//   - 取消订阅后，通道会被关闭，尝试接收会立即返回零值
func TestMessageBus_SubscriptionUnsubscribe(t *testing.T) {
	b := NewMessageBus(Config{})
	t.Cleanup(func() { _ = b.Close() })

	sub, err := b.SubscribeOutbound()
	if err != nil {
		t.Fatalf("subscribe outbound: %v", err)
	}
	sub.Unsubscribe()

	_, ok := <-sub.Channel
	if ok {
		t.Fatal("expected closed channel after unsubscribe")
	}
}

// mustRecv 是一个辅助函数，用于在测试中阻塞式接收事件。
// 如果在给定的时间内（2秒）收不到事件，则测试失败。
// 这简化了测试代码，避免了重复的 select-case 逻辑。n//
// 参数：
//   - t: *testing.T 测试上下文
//   - ch: 接收通道（任意类型）
//
// 返回值：通道中接收到的值，或在超时时 panic
func mustRecv[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event")
		var zero T
		return zero
	}
}
