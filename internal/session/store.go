package session

import (
	"sync"

	"github.com/cloudwego/eino/schema"
	"mini-clawdbot/internal/bus"
)

// Store 定义按 SessionKey 访问会话历史的最小接口。(数据结构为 Message 切片)
type Store interface {
	Add(key bus.SessionKey, msg *schema.Message)
	Messages(key bus.SessionKey) []*schema.Message
}

// MemoryStore 是基于 MemorySession 的内存会话存储。
type MemoryStore struct {
	mu       sync.RWMutex
	sessions map[bus.SessionKey]*MemorySession
}

// NewMemoryStore 创建内存会话存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions: make(map[bus.SessionKey]*MemorySession),
	}
}

// Add 追加消息到指定会话。
func (s *MemoryStore) Add(key bus.SessionKey, msg *schema.Message) {
	if msg == nil || key == "" {
		return
	}
	s.getOrCreate(key).Add(msg)
}

// Messages 返回指定会话消息历史（浅拷贝）。
func (s *MemoryStore) Messages(key bus.SessionKey) []*schema.Message {
	if key == "" {
		return nil
	}
	return s.getOrCreate(key).Messages()
}

func (s *MemoryStore) getOrCreate(key bus.SessionKey) *MemorySession {
	s.mu.RLock()
	existing, ok := s.sessions[key]
	s.mu.RUnlock()
	if ok {
		return existing
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok = s.sessions[key]; ok {
		return existing
	}
	created := NewMemorySession()
	s.sessions[key] = created
	return created
}
