package cron

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mini-clawdbot/internal/bus"
	"mini-clawdbot/internal/service"
	"mini-clawdbot/internal/toolruntime"
)

// Service 管理 cron 任务。
type Service struct {
	cfg  Config
	bus  *bus.MessageBus
	tool *toolruntime.Runtime

	mu      sync.RWMutex
	jobs    map[string]*Job
	running bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// NewService 创建 cron 服务。
func NewService(cfg Config, messageBus *bus.MessageBus, rt *toolruntime.Runtime) (*Service, error) {
	root := strings.TrimSpace(cfg.RootDir)
	if root == "" {
		root = "."
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	cfg.RootDir = root

	if err := os.MkdirAll(filepath.Join(root, "data", "cron"), 0o755); err != nil {
		return nil, fmt.Errorf("create cron dir: %w", err)
	}

	s := &Service{
		cfg:  cfg,
		bus:  messageBus,
		tool: rt,
		jobs: make(map[string]*Job),
	}
	_ = s.loadJobs()
	return s, nil
}

func (s *Service) jobsPath() string {
	return filepath.Join(s.cfg.RootDir, "data", "cron", "jobs.json")
}

func (s *Service) runsPath() string {
	return filepath.Join(s.cfg.RootDir, "data", "cron", "runs.jsonl")
}

// Start 启动调度循环。
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.running = true
	s.wg.Add(1)
	go s.loop(runCtx)
	return nil
}

// Stop 停止调度循环。
func (s *Service) Stop(context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	cancel := s.cancel
	s.mu.Unlock()
	cancel()
	s.wg.Wait()
	return nil
}

// Health 返回服务健康。
func (s *Service) Health(context.Context) service.HealthStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := service.StatusStopped
	if s.running {
		status = service.StatusReady
	}
	return service.HealthStatus{
		Name:   "cron_service",
		Status: status,
		Details: map[string]any{
			"job_count": len(s.jobs),
		},
	}
}

func (s *Service) loop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runDue(ctx, time.Now())
		}
	}
}

func (s *Service) runDue(ctx context.Context, now time.Time) {
	jobs := s.ListJobs()
	for _, job := range jobs {
		if job == nil || !job.Enabled || job.EverySeconds <= 0 {
			continue
		}
		if !job.NextRunAt.IsZero() && now.Before(job.NextRunAt) {
			continue
		}
		_ = s.RunJob(ctx, job.ID, true)
	}
}

// AddJob 添加任务。
func (s *Service) AddJob(job *Job) error {
	if job == nil {
		return fmt.Errorf("job is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(job.ID) == "" {
		job.ID = fmt.Sprintf("job-%d", time.Now().UnixNano())
	}
	if _, ok := s.jobs[job.ID]; ok {
		return fmt.Errorf("job %s already exists", job.ID)
	}
	now := time.Now()
	job.CreatedAt = now
	job.UpdatedAt = now
	if job.EverySeconds > 0 {
		job.NextRunAt = now.Add(time.Duration(job.EverySeconds) * time.Second)
	}
	s.jobs[job.ID] = job
	return s.persistJobs()
}

// UpdateJob 更新任务。
func (s *Service) UpdateJob(id string, mutate func(*Job) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("job %s not found", id)
	}
	if err := mutate(job); err != nil {
		return err
	}
	job.UpdatedAt = time.Now()
	return s.persistJobs()
}

// GetJob 查询任务。
func (s *Service) GetJob(id string) (*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[id]
	if !ok {
		return nil, fmt.Errorf("job %s not found", id)
	}
	cp := *job
	return &cp, nil
}

// ListJobs 返回任务列表。
func (s *Service) ListJobs() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		cp := *job
		out = append(out, &cp)
	}
	return out
}

// RunJob 手动触发任务。
func (s *Service) RunJob(ctx context.Context, id string, force bool) error {
	s.mu.RLock()
	job, ok := s.jobs[id]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("job %s not found", id)
	}
	if !force && !job.Enabled {
		return fmt.Errorf("job %s disabled", id)
	}

	record := RunRecord{
		RunID:     fmt.Sprintf("run-%d", time.Now().UnixNano()),
		JobID:     id,
		StartedAt: time.Now(),
		Status:    "ok",
	}

	var runErr error
	switch job.PayloadType {
	case PayloadTypeShellTask:
		result, err := s.tool.Execute(ctx, "run_shell", map[string]any{"command": job.Command})
		if err != nil {
			runErr = err
		}
		record.Output = result.Output
	case PayloadTypeAgentTurn:
		runErr = s.publishAgentInbound(ctx, job)
	default:
		runErr = fmt.Errorf("unsupported payload type %q", job.PayloadType)
	}

	record.FinishedAt = time.Now()
	if runErr != nil {
		record.Status = "error"
		record.Error = runErr.Error()
	}
	if err := s.appendRun(record); err != nil && runErr == nil {
		runErr = err
	}

	_ = s.UpdateJob(id, func(j *Job) error {
		now := time.Now()
		j.LastRunAt = now
		if j.EverySeconds > 0 {
			j.NextRunAt = now.Add(time.Duration(j.EverySeconds) * time.Second)
		}
		return nil
	})

	if s.bus != nil {
		_ = s.bus.PublishAudit(ctx, &bus.AuditEvent{
			Kind:       "cron_run",
			TraceID:    record.RunID,
			SessionKey: bus.SessionKey(job.SessionKey),
			Payload: map[string]any{
				"job_id": job.ID,
				"status": record.Status,
				"error":  record.Error,
			},
		})
	}

	return runErr
}

func (s *Service) publishAgentInbound(ctx context.Context, job *Job) error {
	parts := strings.Split(job.SessionKey, "/")
	if len(parts) < 4 {
		return fmt.Errorf("invalid session key %q", job.SessionKey)
	}
	return s.bus.PublishInbound(ctx, &bus.InboundMessage{
		TraceID:   fmt.Sprintf("cron-%d", time.Now().UnixNano()),
		Channel:   parts[0],
		AccountID: parts[1],
		ChatID:    parts[2],
		ThreadID:  parts[3],
		Content:   job.Message,
		Metadata: map[string]any{
			"source": "cron",
			"job_id": job.ID,
		},
	})
}

func (s *Service) loadJobs() error {
	data, err := os.ReadFile(s.jobsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var jobs []*Job
	if err := json.Unmarshal(data, &jobs); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range jobs {
		if job == nil || job.ID == "" {
			continue
		}
		s.jobs[job.ID] = job
	}
	return nil
}

func (s *Service) persistJobs() error {
	jobs := make([]*Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		cp := *job
		jobs = append(jobs, &cp)
	}
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.jobsPath(), data, 0o644)
}

func (s *Service) appendRun(run RunRecord) error {
	f, err := os.OpenFile(s.runsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

