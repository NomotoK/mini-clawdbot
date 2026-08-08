package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"mini-clawdbot/internal/bus"
	"mini-clawdbot/internal/channels"
	"mini-clawdbot/internal/cron"
	"mini-clawdbot/internal/service"
	"mini-clawdbot/internal/session"
)

// Config 定义网关配置。
type Config struct {
	Host      string
	Port      int
	AuthToken string
}

// Server 提供 HTTP + WebSocket 运维入口。
type Server struct {
	cfg    Config
	bus    *bus.MessageBus
	store  session.Store
	cron   *cron.Service
	chMgr  *channels.ChannelManager

	mu       sync.RWMutex
	server   *http.Server
	listenAddr string
	running  bool
	healthFn func(context.Context) service.HealthStatus

	upgrader websocket.Upgrader
	clients  map[*websocket.Conn]struct{}
}

// NewServer 创建网关服务。
func NewServer(cfg Config, messageBus *bus.MessageBus, store session.Store, channelMgr *channels.ChannelManager, cronSvc *cron.Service) *Server {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	return &Server{
		cfg:   cfg,
		bus:   messageBus,
		store: store,
		cron:  cronSvc,
		chMgr: channelMgr,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
		clients: make(map[*websocket.Conn]struct{}),
	}
}

// SetHealthProvider 设置服务健康聚合回调。
func (s *Server) SetHealthProvider(fn func(context.Context) service.HealthStatus) {
	s.mu.Lock()
	s.healthFn = fn
	s.mu.Unlock()
}

// Start 启动 HTTP/WS 服务。
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = true
	s.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/services", s.handleServices)
	mux.HandleFunc("/sessions/", s.handleSessionSummary)
	mux.HandleFunc("/tools/audit", s.handleToolAudit)
	mux.HandleFunc("/cron/jobs/", s.handleCronRun)
	mux.HandleFunc("/ws", s.handleWS)

	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
		return err
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.mu.Lock()
	s.server = srv
	s.listenAddr = ln.Addr().String()
	s.mu.Unlock()

	go s.broadcastLoop(ctx)
	go func() {
		<-ctx.Done()
		_ = s.Stop(context.Background())
	}()
	go func() {
		_ = srv.Serve(ln)
	}()
	return nil
}

// Stop 停止网关。
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	srv := s.server
	s.listenAddr = ""
	clients := make([]*websocket.Conn, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.clients = make(map[*websocket.Conn]struct{})
	s.mu.Unlock()

	for _, c := range clients {
		_ = c.Close()
	}
	if srv != nil {
		return srv.Shutdown(ctx)
	}
	return nil
}

// Health 返回网关状态。
func (s *Server) Health(context.Context) service.HealthStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.running {
		return service.HealthStatus{Name: "gateway", Status: service.StatusStopped}
	}
	return service.HealthStatus{
		Name:   "gateway",
		Status: service.StatusReady,
		Details: map[string]any{
			"addr": s.listenAddr,
		},
	}
}

// Addr 返回网关监听地址。
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listenAddr
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	h := s.Health(context.Background())
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleServices(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	healthFn := s.healthFn
	s.mu.RUnlock()
	if healthFn == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "unknown",
		})
		return
	}
	writeJSON(w, http.StatusOK, healthFn(context.Background()))
}

func (s *Server) handleSessionSummary(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/sessions/")
	key = strings.TrimSpace(key)
	if key == "" {
		http.Error(w, "missing session key", http.StatusBadRequest)
		return
	}
	msgs := s.store.Messages(bus.SessionKey(key))
	writeJSON(w, http.StatusOK, map[string]any{
		"session_key": key,
		"message_cnt": len(msgs),
		"messages":    msgs,
	})
}

func (s *Server) handleToolAudit(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	queryable, ok := s.store.(session.AuditQueryable)
	if !ok {
		writeJSON(w, http.StatusOK, []session.ToolAuditRecord{})
		return
	}
	records, err := queryable.QueryToolAudits(context.Background(), 100)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, records)
}

func (s *Server) handleCronRun(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/cron/jobs/")
	if !strings.HasSuffix(path, "/run") {
		http.Error(w, "invalid cron run path", http.StatusNotFound)
		return
	}
	jobID := strings.TrimSuffix(path, "/run")
	jobID = strings.Trim(jobID, "/")
	if jobID == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}
	if err := s.cron.RunJob(r.Context(), jobID, true); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID, "status": "triggered"})
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.clients[conn] = struct{}{}
	s.mu.Unlock()
	go s.consumeWS(conn)
}

func (s *Server) authorized(r *http.Request) bool {
	token := strings.TrimSpace(s.cfg.AuthToken)
	if token == "" {
		return true
	}
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		if strings.TrimSpace(header[len("Bearer "):]) == token {
			return true
		}
	}
	return strings.TrimSpace(r.Header.Get("X-Auth-Token")) == token
}

func (s *Server) consumeWS(conn *websocket.Conn) {
	defer func() {
		s.mu.Lock()
		delete(s.clients, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (s *Server) broadcastLoop(ctx context.Context) {
	if s.bus == nil {
		return
	}
	inSub, _ := s.bus.SubscribeInbound()
	outSub, _ := s.bus.SubscribeOutbound()
	streamSub, _ := s.bus.SubscribeStream()
	errSub, _ := s.bus.SubscribeError()
	auditSub, _ := s.bus.SubscribeAudit()
	defer func() {
		if inSub != nil {
			inSub.Unsubscribe()
		}
		if outSub != nil {
			outSub.Unsubscribe()
		}
		if streamSub != nil {
			streamSub.Unsubscribe()
		}
		if errSub != nil {
			errSub.Unsubscribe()
		}
		if auditSub != nil {
			auditSub.Unsubscribe()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-inSub.Channel:
			if ok && evt != nil {
				s.broadcast(map[string]any{"event_type": "inbound", "payload": evt})
			}
		case evt, ok := <-outSub.Channel:
			if ok && evt != nil {
				s.broadcast(map[string]any{"event_type": "outbound", "payload": evt})
			}
		case evt, ok := <-streamSub.Channel:
			if ok && evt != nil {
				s.broadcast(map[string]any{"event_type": "stream", "payload": evt})
			}
		case evt, ok := <-errSub.Channel:
			if ok && evt != nil {
				s.broadcast(map[string]any{"event_type": "error", "payload": evt})
			}
		case evt, ok := <-auditSub.Channel:
			if ok && evt != nil {
				s.broadcast(map[string]any{"event_type": evt.Kind, "payload": evt})
			}
		}
	}
}

func (s *Server) broadcast(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	s.mu.RLock()
	clients := make([]*websocket.Conn, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.RUnlock()

	for _, c := range clients {
		_ = c.WriteMessage(websocket.TextMessage, data)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
