package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mini-clawdbot/internal/bus"
	"mini-clawdbot/internal/channels"
	"mini-clawdbot/internal/cron"
	"mini-clawdbot/internal/session"
	"mini-clawdbot/internal/toolruntime"
)

func TestGatewayHandlers_HealthAuditAndCronRun(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	messageBus := bus.NewMessageBus(bus.Config{})
	store, err := session.NewJSONLStore(session.JSONLStoreConfig{RootDir: root})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	rt, err := toolruntime.BuildDefaultRuntime(toolruntime.DefaultRuntimeConfig{WorkingDir: root}, store)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	cronSvc, err := cron.NewService(cron.Config{RootDir: root}, messageBus, rt)
	if err != nil {
		t.Fatalf("new cron: %v", err)
	}
	job := &cron.Job{
		Name:         "gateway-run",
		Enabled:      true,
		EverySeconds: 3600,
		PayloadType:  cron.PayloadTypeShellTask,
		Command:      "printf hi",
	}
	if err := cronSvc.AddJob(job); err != nil {
		t.Fatalf("add job: %v", err)
	}

	srv := NewServer(Config{}, messageBus, store, channels.NewChannelManager(messageBus), cronSvc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	srv.handleHealth(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("health code: %d", w.Code)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/tools/audit", nil)
	srv.handleToolAudit(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("tool audit code: %d", w.Code)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/cron/jobs/"+job.ID+"/run", nil)
	srv.handleCronRun(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cron run code: %d", w.Code)
	}

	audits, err := store.QueryToolAudits(context.Background(), 100)
	if err != nil {
		t.Fatalf("query audits: %v", err)
	}
	if len(audits) == 0 {
		t.Fatal("expected audit logs after cron run")
	}
}

