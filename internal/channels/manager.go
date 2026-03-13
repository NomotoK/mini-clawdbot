package channels

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"mini-clawdbot/internal/bus"
)

var (
	// ErrAdapterAlreadyRegistered 表示重复注册同一 channel/account 的 adapter。
	ErrAdapterAlreadyRegistered = errors.New("adapter already registered")
	// ErrManagerAlreadyStarted 表示 manager 已经启动。
	ErrManagerAlreadyStarted = errors.New("channel manager already started")
	// ErrManagerNotStarted 表示 manager 尚未启动。
	ErrManagerNotStarted = errors.New("channel manager not started")
)

const defaultAccountID = "default"

// registeredAdapter 代表一个注册的 adapter 实例，包含所属 channel、accountID 和 adapter 对象。
// 字段说明：
//   - channel: 渠道名称，如 "telegram"、"slack" 等
//   - accountID: 账号标识，允许同一渠道多个账号区分，如 "bot1"、"bot2" 等
//   - adapter: 实际的 ChannelAdapter 实例
type registeredAdapter struct {
	channel   string
	accountID string
	adapter   ChannelAdapter
}

// ChannelManager 负责 adapter 生命周期与总线转发。
// 字段说明：
//   - bus: 消息总线，用于转发 inbound/outbound 消息
//   - mu: 保护 adapters 和 started 状态的互斥锁
//   - adapters: 已注册的 adapter 列表，key 为 "channel/accountID"
//   - started: 管理器是否已启动
//   - startCtx: 管理器启动时创建的上下文，用于控制 adapter 生命周期
//   - stopStart: 取消 startCtx 的函数，用于停止管理器
//   - wg: 用于等待所有 goroutine 退出的 WaitGroup
type ChannelManager struct {
	bus *bus.MessageBus

	mu        sync.RWMutex
	adapters  map[string]registeredAdapter
	started   bool
	startCtx  context.Context
	stopStart context.CancelFunc
	wg        sync.WaitGroup
}

// NewChannelManager 创建渠道管理器。
func NewChannelManager(messageBus *bus.MessageBus) *ChannelManager {
	return &ChannelManager{
		bus:      messageBus,
		adapters: make(map[string]registeredAdapter),
	}
}

// Register 注册一个 channel/account 对应的适配器。
func (m *ChannelManager) Register(channel, accountID string, adapter ChannelAdapter) error {
	if adapter == nil {
		return errors.New("adapter is nil")
	}
	ch := normalizeChannel(channel, adapter.Name())
	acc := normalizeAccountID(accountID)
	key := adapterKey(ch, acc)

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.adapters[key]; ok {
		return fmt.Errorf("%w: %s", ErrAdapterAlreadyRegistered, key)
	}
	m.adapters[key] = registeredAdapter{
		channel:   ch,
		accountID: acc,
		adapter:   adapter,
	}
	return nil
}

// Start 启动ChannelManager，启动所有 adapter，并建立 inbound/outbound 转发链路。
func (m *ChannelManager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return ErrManagerAlreadyStarted
	}

	managedCtx, cancel := context.WithCancel(ctx)
	m.startCtx = managedCtx
	m.stopStart = cancel
	m.started = true

	adapters := make([]registeredAdapter, 0, len(m.adapters))
	for _, item := range m.adapters {
		adapters = append(adapters, item)
	}
	m.mu.Unlock()

	for _, item := range adapters {
		if err := item.adapter.Start(managedCtx); err != nil {
			cancel()
			return fmt.Errorf("start adapter %s/%s: %w", item.channel, item.accountID, err)
		}
		m.wg.Add(1)
		go m.forwardInbound(managedCtx, item)
	}

	outboundSub, err := m.bus.SubscribeOutbound()//channel manager 订阅总线的 outbound 主题，准备转发消息到 adapter
	if err != nil {
		cancel()
		return fmt.Errorf("subscribe outbound bus topic: %w", err)
	}

	m.wg.Add(1)
	go m.dispatchOutbound(managedCtx, outboundSub)//启动 goroutine 转发 outbound 消息到 adapter

	return nil
}

// Stop 停止 manager 与所有 adapter。
func (m *ChannelManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return ErrManagerNotStarted
	}
	m.started = false
	cancel := m.stopStart
	adapters := make([]registeredAdapter, 0, len(m.adapters))
	for _, item := range m.adapters {
		adapters = append(adapters, item)
	}
	m.mu.Unlock()

	cancel()
	for _, item := range adapters {
		if err := item.adapter.Stop(ctx); err != nil {
			return fmt.Errorf("stop adapter %s/%s: %w", item.channel, item.accountID, err)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.wg.Wait()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

// forwardInbound 从 adapter 的 Receive 通道读取消息，并转发到总线的 Inbound 主题。
func (m *ChannelManager) forwardInbound(ctx context.Context, item registeredAdapter) {
	defer m.wg.Done()

	recvCh := item.adapter.Receive()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-recvCh:
			if !ok {
				return
			}
			if msg == nil {
				continue
			}
			if msg.Channel == "" {
				msg.Channel = item.channel
			}
			if msg.AccountID == "" {
				msg.AccountID = item.accountID
			}
			if err := m.bus.PublishInbound(ctx, msg); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, bus.ErrBusClosed) {
					return
				}
			}
		}
	}
}

// dispatchOutbound 从总线的 Outbound 主题读取消息，根据 channel/accountID 路由到对应的 adapter 的 Send 方法。
func (m *ChannelManager) dispatchOutbound(ctx context.Context, sub *bus.Subscription[*bus.OutboundMessage]) {
	defer m.wg.Done()
	defer sub.Unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-sub.Channel:
			if !ok {
				return
			}
			if msg == nil {
				continue
			}
			target := m.pickAdapter(msg.Channel, msg.AccountID)
			if target == nil {
				_ = m.bus.PublishError(ctx, &bus.ErrorEvent{
					TraceID:    msg.TraceID,
					SessionKey: msg.SessionKey(),
					Stage:      "channel_dispatch",
					Code:       "adapter_not_found",
					Message:    fmt.Sprintf("no adapter for %s/%s", msg.Channel, normalizeAccountID(msg.AccountID)),
				})
				continue
			}
			if err := target.Send(ctx, msg); err != nil {
				_ = m.bus.PublishError(ctx, &bus.ErrorEvent{
					TraceID:    msg.TraceID,
					SessionKey: msg.SessionKey(),
					Stage:      "channel_send",
					Code:       "send_failed",
					Message:    err.Error(),
				})
			}
		}
	}
}

// pickAdapter 根据 channel 和 accountID 从已注册的 adapters 中选择合适的 adapter 实例。
// 选择逻辑如下：
//   1. 首先尝试精确匹配 channel/accountID 的 adapter。
//   2. 如果 accountID 为空，则尝试匹配同 channel 的默认账号（accountID = "default"）的 adapter。
//   3. 如果都没有找到，则返回 nil。
func (m *ChannelManager) pickAdapter(channel, accountID string) ChannelAdapter {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := adapterKey(normalizeChannel(channel, ""), normalizeAccountID(accountID))
	if item, ok := m.adapters[key]; ok {
		return item.adapter
	}

	// account 为空时，尝试同 channel 的默认账号。
	if accountID == "" {
		defaultKey := adapterKey(normalizeChannel(channel, ""), defaultAccountID)
		if item, ok := m.adapters[defaultKey]; ok {
			return item.adapter
		}
	}
	return nil
}

// adapterKey 生成 channel/accountID 的唯一 key，用于在 adapters map 中存储和查找。
func adapterKey(channel, accountID string) string {
	return channel + "/" + accountID
}

// normalizeChannel 确保 channel 字符串不为空，如果为空则使用 fallback 作为默认值。
func normalizeChannel(channel, fallback string) string {
	if channel != "" {
		return channel
	}
	return fallback
}

// normalizeAccountID 确保 accountID 字符串不为空，如果为空则使用 "default" 作为默认值。
func normalizeAccountID(accountID string) string {
	if accountID == "" {
		return defaultAccountID
	}
	return accountID
}
