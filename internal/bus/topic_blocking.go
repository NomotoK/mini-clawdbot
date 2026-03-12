package bus

import (
	"context"
	"sync"
)

// blockingTopic 是阻塞式话题，队列满时发布端挂起等待。用于：InboundMessage、OutboundMessage、ErrorEvent
// blockingTopic 由 fanout goroutine 负责将消息分发给所有订阅者，确保消息不丢失。
// 
// 字段说明：
//   - queue: 内部消息队列，发布者将事件发送到此处，fanout goroutine 从中读取并分发
//   - subs: 活跃订阅者列表，键为订阅ID，值为对应的事件通道
//   - subBuf: 每个订阅者通道的缓冲大小，控制背压程度
//   - subMu: 保护 subs 的读写锁，确保订阅者添加和移除的线程安全
//   - closeMu: 确保话题只被关闭一次的互斥锁
type blockingTopic[T any] struct {
	queue   chan T
	subs    map[string]chan T
	subBuf  int
	subMu   sync.RWMutex
	closeMu sync.Once
}

// newBlockingTopic 创建一个新的阻塞式话题实例。
// queueSize 定义内部消息队列的容量，subBuffer 定义每个订阅者通道的缓冲大小。
// 返回值为初始化完成的 blockingTopic 实例。
func newBlockingTopic[T any](queueSize, subBuffer int) *blockingTopic[T] {
	t := &blockingTopic[T]{
		queue:  make(chan T, queueSize),
		subs:   make(map[string]chan T),
		subBuf: subBuffer,
	}
	go t.fanout()
	return t
}

// publish 向话题发布一个事件，若队列满会阻塞直到可写或 ctx 取消。
// item 是要发布的事件，ctx 用于控制发布的超时或取消，done 通道用于检测总线关闭。
// 返回值为发布操作的结果，可能是 nil（成功）、ErrBusClosed（总线已关闭）或 ctx.Err()（上下文错误）。
// 队列满时，发布者会被阻塞，直到有空间可写或上下文取消，确保消息不会丢失。
func (t *blockingTopic[T]) publish(ctx context.Context, item T, done <-chan struct{}) error {
	select {
	case t.queue <- item:
		return nil
	case <-done:
		return ErrBusClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// subscribe 创建一个新的订阅，返回一个 Subscription 实例，包含订阅ID和事件通道。
// 订阅者可以通过 Subscription.Channel 接收事件，调用 Subscription.Unsubscribe() 取消订阅。
// 每个订阅者都会获得一个独立的事件通道，发布的事件会被分发到所有活跃订阅者。
func (t *blockingTopic[T]) subscribe() *Subscription[T] {
	id := nextEventID()
	ch := make(chan T, t.subBuf)

	t.subMu.Lock()
	t.subs[id] = ch
	t.subMu.Unlock()

	return &Subscription[T]{
		ID:      id,
		Channel: ch,
		cancel: func() {
			t.subMu.Lock()
			sub, ok := t.subs[id]
			if ok {
				delete(t.subs, id)
				close(sub)
			}
			t.subMu.Unlock()
		},
	}
}

// fanout 是一个后台 goroutine，负责从 queue 中读取事件并分发给所有订阅者的通道。
// 当 queue 关闭时，fanout 会清理所有订阅者并关闭它们的通道，确保资源正确释放。
// 该方法持续运行，直到 queue 被关闭，期间会处理所有发布的事件并分发给订阅者。
// 策略说明：当发布者发送事件时，如果某个订阅者的通道已满，fanout 不会丢弃事件，而是会阻塞等待。
func (t *blockingTopic[T]) fanout() {
	for item := range t.queue {
		t.subMu.RLock()
		snapshot := make([]chan T, 0, len(t.subs))
		for _, ch := range t.subs {
			snapshot = append(snapshot, ch)
		}
		t.subMu.RUnlock()

		for _, ch := range snapshot {
			ch <- item
		}
	}

	t.subMu.Lock()
	for id, ch := range t.subs {
		delete(t.subs, id)
		close(ch)
	}
	t.subMu.Unlock()
}

// close 关闭话题，停止 fanout 并释放所有订阅者资源。调用后，发布将返回 ErrBusClosed，订阅者通道将被关闭。
func (t *blockingTopic[T]) close() {
	t.closeMu.Do(func() {
		close(t.queue)
	})
}
