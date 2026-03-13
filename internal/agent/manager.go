package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"
	"mini-clawdbot/internal/bus"
	"mini-clawdbot/internal/router"
	"mini-clawdbot/internal/session"
)

const (
	defaultWorkerQueueSize = 64
	defaultWorkerIdleTTL   = 2 * time.Minute
)

// ManagerConfig 定义 AgentManager 的最小配置。
type ManagerConfig struct {
	WorkerQueueSize int
	WorkerIdleTTL   time.Duration
}

func (c ManagerConfig) normalize() ManagerConfig {
	out := c
	if out.WorkerQueueSize <= 0 {
		out.WorkerQueueSize = defaultWorkerQueueSize
	}
	if out.WorkerIdleTTL <= 0 {
		out.WorkerIdleTTL = defaultWorkerIdleTTL
	}
	return out
}

// AgentManager 消费 inbound，按 session worker 串行执行并发布 outbound。
type AgentManager struct {
	bus     *bus.MessageBus
	router  *router.SessionRouter
	runner  *ReactRunner
	store   session.Store
	cfg     ManagerConfig
	ctx     context.Context
	cancel  context.CancelFunc
	started bool

	mu      sync.Mutex
	workers map[bus.SessionKey]*sessionWorker
	wg      sync.WaitGroup
}

type sessionWorker struct {
	key        bus.SessionKey
	queue      chan *bus.InboundMessage
	lastActive time.Time
	mu         sync.Mutex
	closed     bool
}

// NewAgentManager 创建 AgentManager。
func NewAgentManager(messageBus *bus.MessageBus, sessionRouter *router.SessionRouter, runner *ReactRunner, store session.Store, cfg ManagerConfig) *AgentManager {
	return &AgentManager{
		bus:     messageBus,
		router:  sessionRouter,
		runner:  runner,
		store:   store,
		cfg:     cfg.normalize(),
		workers: make(map[bus.SessionKey]*sessionWorker),
	}
}

// Start 启动 inbound 消费主循环。
func (m *AgentManager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return errors.New("agent manager already started")
	}
	if m.bus == nil || m.router == nil || m.runner == nil || m.store == nil {
		return errors.New("agent manager dependencies are not ready")
	}

	m.ctx, m.cancel = context.WithCancel(ctx)
	m.started = true

	sub, err := m.bus.SubscribeInbound()
	if err != nil {
		return fmt.Errorf("subscribe inbound: %w", err)
	}
	m.wg.Add(1)
	go m.consumeInbound(m.ctx, sub)//启动goroutine消费inbound消息
	return nil
}

// Stop 停止 manager 与全部 worker。
func (m *AgentManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return nil
	}
	m.started = false
	cancel := m.cancel
	workers := make([]*sessionWorker, 0, len(m.workers))
	for _, w := range m.workers {
		workers = append(workers, w)
	}
	m.workers = make(map[bus.SessionKey]*sessionWorker)
	m.mu.Unlock()

	cancel()
	for _, w := range workers {
		w.close()
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

// consumeInbound 是 manager 的主循环，持续消费 inbound 消息并分发到 session worker。
func (m *AgentManager) consumeInbound(ctx context.Context, sub *bus.Subscription[*bus.InboundMessage]) {
	defer m.wg.Done()
	defer sub.Unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return
		case inbound, ok := <-sub.Channel:
			if !ok {
				return
			}
			if inbound == nil {
				continue
			}
			sessionKey := m.router.RouteInbound(inbound)
			for {
				worker := m.getOrCreateWorker(sessionKey)//获取或创建对应 session 的 worker
				if worker.enqueue(ctx, inbound) {//成功入队则跳出重试循环
					break
				}
				if ctx.Err() != nil {
					return
				}
			}
		}
	}
}

func (m *AgentManager) getOrCreateWorker(key bus.SessionKey) *sessionWorker {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.workers[key]; ok {
		existing.lastActive = time.Now()
		return existing
	}

	worker := &sessionWorker{
		key:        key,
		queue:      make(chan *bus.InboundMessage, m.cfg.WorkerQueueSize),
		lastActive: time.Now(),
	}
	m.workers[key] = worker

	m.wg.Add(1)
	go m.runWorker(m.ctx, worker)
	return worker
}
// runWorker 是 session worker 的主循环，处理对应 session 的消息队列并执行 ReAct。
func (m *AgentManager) runWorker(ctx context.Context, w *sessionWorker) {
	defer m.wg.Done()

	timer := time.NewTimer(m.cfg.WorkerIdleTTL)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			m.cleanupWorker(w.key, w)
			return
		case <-timer.C:
			if m.expireWorkerIfIdle(w.key, w) {
				return
			}
			timer.Reset(m.cfg.WorkerIdleTTL)
		case inbound, ok := <-w.queue:
			if !ok {
				m.cleanupWorker(w.key, w)
				return
			}
			w.lastActive = time.Now()
			m.handleInbound(ctx, w.key, inbound)
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(m.cfg.WorkerIdleTTL)
		}
	}
}

func (m *AgentManager) cleanupWorker(key bus.SessionKey, target *sessionWorker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if current, ok := m.workers[key]; ok && current == target {
		delete(m.workers, key)
	}
}

func (m *AgentManager) expireWorkerIfIdle(key bus.SessionKey, target *sessionWorker) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.workers[key]
	if !ok || current != target {
		return true
	}
	if len(target.queue) > 0 {
		return false
	}
	target.close()
	delete(m.workers, key)
	return true
}
// handleInbound 处理单条 inbound 消息：执行 ReAct 并发布 outbound。（在此处真正触发 ReAct Agent）
func (m *AgentManager) handleInbound(ctx context.Context, key bus.SessionKey, inbound *bus.InboundMessage) {
	userMsg := schema.UserMessage(inbound.Content)
	m.store.Add(key, userMsg)

	history := m.store.Messages(key)
	finalMsg, trace, err := m.runner.RunWithTrace(ctx, history)
	if err != nil {
		_ = m.bus.PublishError(ctx, &bus.ErrorEvent{
			TraceID:    inbound.TraceID,
			SessionKey: key,
			Stage:      "agent_run",
			Code:       "react_failed",
			Message:    err.Error(),
		})
		return
	}
	m.store.Add(key, finalMsg)

	content := strings.TrimSpace(finalMsg.Content)
	if content == "" {
		content = fmt.Sprintf("role=%s content=%q", finalMsg.Role, finalMsg.Content)
	}

	metadata := map[string]any{
		"trace_events": len(trace.Events),
		"session_key":  string(key),
	}

	_ = m.bus.PublishOutbound(ctx, &bus.OutboundMessage{
		TraceID:   inbound.TraceID,
		Channel:   inbound.Channel,
		AccountID: inbound.AccountID,
		ChatID:    inbound.ChatID,
		ThreadID:  inbound.ThreadID,
		ReplyTo:   inbound.EventID,
		Content:   content,
		Metadata:  metadata,
	})
}

func (m *AgentManager) workerCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.workers)
}

func (w *sessionWorker) enqueue(ctx context.Context, msg *bus.InboundMessage) bool {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return false
	}
	ch := w.queue
	w.mu.Unlock()

	select {
	case <-ctx.Done():
		return false
	case ch <- msg:
		return true
	}
}

func (w *sessionWorker) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	close(w.queue)
}
