package bus

import (
	"context"
	"sync"
	"time"
)

// MessageBus 提供统一事件模型与发布订阅能力。
type MessageBus struct {
	inbound  *blockingTopic[*InboundMessage]
	outbound *blockingTopic[*OutboundMessage]
	errTopic *blockingTopic[*ErrorEvent]
	stream   *streamTopic

	mu     sync.RWMutex
	closed bool
	done   chan struct{}
}

// NewMessageBus 创建消息总线。
func NewMessageBus(cfg Config) *MessageBus {
	normalized := cfg.normalize()
	b := &MessageBus{
		done: make(chan struct{}),
	}
	b.inbound = newBlockingTopic[*InboundMessage](normalized.InboundBuffer, normalized.SubscriberBuffer)
	b.outbound = newBlockingTopic[*OutboundMessage](normalized.OutboundBuffer, normalized.SubscriberBuffer)
	b.errTopic = newBlockingTopic[*ErrorEvent](normalized.ErrorBuffer, normalized.SubscriberBuffer)
	b.stream = newStreamTopic(normalized.StreamBuffer, normalized.SubscriberBuffer)
	return b
}

// PublishInbound 发布入站消息，若缓冲满会阻塞直到可写或 ctx 取消。
func (b *MessageBus) PublishInbound(ctx context.Context, msg *InboundMessage) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	ensureInboundDefaults(msg)
	return b.inbound.publish(ctx, msg, b.done)
}

// PublishOutbound 发布出站消息，若缓冲满会阻塞直到可写或 ctx 取消。
func (b *MessageBus) PublishOutbound(ctx context.Context, msg *OutboundMessage) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	ensureOutboundDefaults(msg)
	return b.outbound.publish(ctx, msg, b.done)
}

// PublishError 发布错误事件，若缓冲满会阻塞直到可写或 ctx 取消。
func (b *MessageBus) PublishError(ctx context.Context, evt *ErrorEvent) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	ensureErrorDefaults(evt)
	return b.errTopic.publish(ctx, evt, b.done)
}

// PublishStream 发布流式分片事件。队列满时会丢弃旧分片保留最新分片。
func (b *MessageBus) PublishStream(ctx context.Context, evt *StreamEvent) error {
	if err := b.ensureOpen(); err != nil {
		return err
	}
	ensureStreamDefaults(evt)
	return b.stream.publish(ctx, evt, b.done)
}

// SubscribeInbound 订阅入站消息。
func (b *MessageBus) SubscribeInbound() (*Subscription[*InboundMessage], error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	return b.inbound.subscribe(), nil
}

// SubscribeOutbound 订阅出站消息。
func (b *MessageBus) SubscribeOutbound() (*Subscription[*OutboundMessage], error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	return b.outbound.subscribe(), nil
}

// SubscribeError 订阅错误事件。
func (b *MessageBus) SubscribeError() (*Subscription[*ErrorEvent], error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	return b.errTopic.subscribe(), nil
}

// SubscribeStream 订阅流式分片事件。
func (b *MessageBus) SubscribeStream() (*Subscription[*StreamEvent], error) {
	if err := b.ensureOpen(); err != nil {
		return nil, err
	}
	return b.stream.subscribe(), nil
}

// Close 关闭消息总线并释放全部订阅者。
func (b *MessageBus) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	close(b.done)
	b.mu.Unlock()

	b.inbound.close()
	b.outbound.close()
	b.errTopic.close()
	b.stream.close()
	return nil
}

// IsClosed 返回总线是否已关闭。
func (b *MessageBus) IsClosed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.closed
}

// ensureOpen 确保总线未关闭。
func (b *MessageBus) ensureOpen() error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return ErrBusClosed
	}
	return nil
}

// ensureInboundDefaults 填充 InboundMessage 的默认字段。
func ensureInboundDefaults(msg *InboundMessage) {
	if msg == nil {
		return
	}
	if msg.EventID == "" {
		msg.EventID = nextEventID()
	}
	if msg.TraceID == "" {
		msg.TraceID = msg.EventID
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
}

// ensureOutboundDefaults 填充 OutboundMessage 的默认字段。
func ensureOutboundDefaults(msg *OutboundMessage) {
	if msg == nil {
		return
	}
	if msg.EventID == "" {
		msg.EventID = nextEventID()
	}
	if msg.TraceID == "" {
		msg.TraceID = msg.EventID
	}
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
}

// ensureStreamDefaults 填充 StreamEvent 的默认字段。
func ensureStreamDefaults(evt *StreamEvent) {
	if evt == nil {
		return
	}
	if evt.EventID == "" {
		evt.EventID = nextEventID()
	}
	if evt.TraceID == "" {
		evt.TraceID = evt.EventID
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}
}

// ensureErrorDefaults 填充 ErrorEvent 的默认字段。
func ensureErrorDefaults(evt *ErrorEvent) {
	if evt == nil {
		return
	}
	if evt.EventID == "" {
		evt.EventID = nextEventID()
	}
	if evt.TraceID == "" {
		evt.TraceID = evt.EventID
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}
}
