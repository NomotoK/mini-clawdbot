package bus

import "sync"

// Subscription 表示一次 topic 订阅，持有只读事件通道。
// 订阅者通过 Subscription.Channel 接收事件，调用 Subscription.Unsubscribe() 取消订阅。
// 字段说明：
//   - ID: 订阅的唯一标识符，格式为 "evt-{timestamp}-{seq}"
//   - Channel: 只读事件通道，订阅者通过该通道接收事件
//   - once: 确保 Unsubscribe 方法幂等执行的 sync.Once
//   - cancel: 取消订阅的函数，调用后会关闭事件通道并清理资源
type Subscription[T any] struct {
	ID      string
	Channel <-chan T
	once    sync.Once
	cancel  func()
}

// Unsubscribe 取消订阅，幂等，多次调用安全。
// 调用后，事件通道将被关闭，订阅者将不再接收事件。该方法使用 sync.Once 确保无论调用多少次，取消逻辑只会执行一次，避免重复关闭通道导致的 panic。
func (s *Subscription[T]) Unsubscribe() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
}
