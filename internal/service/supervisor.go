package service

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const defaultStopTimeout = 5 * time.Second

// Supervisor 负责编排组件生命周期。
type Supervisor struct {
	components  []NamedComponent
	stopTimeout time.Duration

	mu      sync.RWMutex
	started bool
}

// SupervisorConfig 定义监督器配置。
type SupervisorConfig struct {
	StopTimeout time.Duration
}

// NewSupervisor 创建组件监督器。
func NewSupervisor(cfg SupervisorConfig, components ...NamedComponent) *Supervisor {
	stopTimeout := cfg.StopTimeout
	if stopTimeout <= 0 {
		stopTimeout = defaultStopTimeout
	}
	return &Supervisor{
		components:  append([]NamedComponent(nil), components...),
		stopTimeout: stopTimeout,
	}
}

// Start 按顺序 Init + Start 所有组件。
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("supervisor already started")
	}
	s.started = true
	s.mu.Unlock()

	initDone := make([]NamedComponent, 0, len(s.components))
	for _, c := range s.components {
		if c.Component == nil {
			continue
		}
		if err := c.Component.Init(ctx); err != nil {
			s.markStopped()
			return fmt.Errorf("init %s: %w", c.Name, err)
		}
		initDone = append(initDone, c)
	}

	started := make([]NamedComponent, 0, len(initDone))
	for _, c := range initDone {
		if err := c.Component.Start(ctx); err != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), s.stopTimeout)
			defer cancel()
			s.stopReverse(stopCtx, started)
			s.markStopped()
			return fmt.Errorf("start %s: %w", c.Name, err)
		}
		started = append(started, c)
	}
	return nil
}

// Run 启动所有组件并阻塞直到 ctx 取消，然后执行优雅停止。
func (s *Supervisor) Run(ctx context.Context) error {
	if err := s.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	stopCtx, cancel := context.WithTimeout(context.Background(), s.stopTimeout)
	defer cancel()
	return s.Stop(stopCtx)
}

// Stop 逆序停止组件。
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = false
	s.mu.Unlock()

	return s.stopReverse(ctx, s.components)
}

func (s *Supervisor) stopReverse(ctx context.Context, list []NamedComponent) error {
	var firstErr error
	for i := len(list) - 1; i >= 0; i-- {
		c := list[i]
		if c.Component == nil {
			continue
		}
		if err := c.Component.Stop(ctx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("stop %s: %w", c.Name, err)
		}
	}
	return firstErr
}

func (s *Supervisor) markStopped() {
	s.mu.Lock()
	s.started = false
	s.mu.Unlock()
}

// Health 聚合所有组件状态。
func (s *Supervisor) Health(ctx context.Context) HealthStatus {
	items := make([]HealthStatus, 0, len(s.components))
	overall := StatusReady
	for _, c := range s.components {
		if c.Component == nil {
			continue
		}
		h := c.Component.Health(ctx)
		if h.Name == "" {
			h.Name = c.Name
		}
		items = append(items, h)
		switch h.Status {
		case StatusDegraded:
			overall = StatusDegraded
		case StatusStopped:
			if overall != StatusDegraded {
				overall = StatusLive
			}
		}
	}
	return HealthStatus{
		Name:   "supervisor",
		Status: overall,
		Details: map[string]any{
			"components": items,
		},
	}
}

