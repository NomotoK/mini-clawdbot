package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mini-clawdbot/internal/bus"
	"mini-clawdbot/internal/config"
	"mini-clawdbot/internal/cron"
	"mini-clawdbot/internal/session"
)

func TestM3SupervisorCronAndToolAuditIntegration(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("m3-readme"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}

	app, err := newWithDependenciesAndService(context.Background(), &fakeToolCallingModel{}, 6, root, config.ServiceConfig{})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- app.Serve(ctx) }()
	time.Sleep(120 * time.Millisecond)

	outSub, err := app.Bus().SubscribeOutbound()
	if err != nil {
		t.Fatalf("subscribe outbound: %v", err)
	}
	defer outSub.Unsubscribe()
	if err := app.Bus().PublishInbound(context.Background(), &bus.InboundMessage{
		TraceID:   "trace-m3-audit",
		Channel:   "telegram",
		AccountID: "acc",
		ChatID:    "chat",
		ThreadID:  "th",
		Content:   "read the readme",
	}); err != nil {
		t.Fatalf("publish inbound: %v", err)
	}
	select {
	case <-outSub.Channel:
	case <-time.After(4 * time.Second):
		t.Fatal("timeout waiting outbound")
	}

	job := &cron.Job{
		Name:         "manual-shell",
		Enabled:      true,
		EverySeconds: 3600,
		PayloadType:  cron.PayloadTypeShellTask,
		Command:      "printf cron-ok",
	}
	if err := app.CronService().AddJob(job); err != nil {
		t.Fatalf("add job: %v", err)
	}
	if err := app.CronService().RunJob(context.Background(), job.ID, true); err != nil {
		t.Fatalf("run job: %v", err)
	}

	queryable, ok := app.sessionStore.(session.AuditQueryable)
	if !ok {
		t.Fatal("session store does not support audit query")
	}
	records, err := queryable.QueryToolAudits(context.Background(), 100)
	if err != nil {
		t.Fatalf("query audits: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("expected tool audit records")
	}

	health := app.Supervisor().Health(context.Background())
	if health.Status == "" {
		t.Fatal("expected supervisor health status")
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting serve stop")
	}
}

