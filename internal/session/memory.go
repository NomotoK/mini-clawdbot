package session

import (
	"sync"

	"github.com/cloudwego/eino/schema"
)

// MemorySession 提供线程安全的内存消息存储。
//
// 设计目标：
// - 作为 MVP 的最小会话实现（不落盘）
// - 提供 Add/Messages 两个基础操作
// - 通过 RWMutex 保证并发读写安全
type MemorySession struct {
	// mu 保护 messages 切片，避免并发读写冲突。
	mu sync.RWMutex
	// messages 保存按时间顺序追加的消息历史。
	messages []*schema.Message
}

// NewMemorySession 创建空会话。
//
// 返回值：
// - *MemorySession: 初始容量为 8 的消息切片，减少小规模 append 频繁扩容
func NewMemorySession() *MemorySession {
	return &MemorySession{messages: make([]*schema.Message, 0, 8)}
}

// Add 向会话中追加一条消息。
//
// 参数：
// - msg: 待追加消息；nil 会被直接忽略
//
// 返回值：
// - 无
func (s *MemorySession) Add(msg *schema.Message) {
	if msg == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
}

// Messages 返回当前会话消息的浅拷贝。
//
// 返回值：
// - []*schema.Message: 新切片（元素指针与内部存储共享）
//
// 说明：
// - 返回新切片可避免调用方误改切片长度影响内部状态
// - 消息对象本身未做深拷贝，符合 MVP 低开销目标
func (s *MemorySession) Messages() []*schema.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*schema.Message, len(s.messages))
	copy(out, s.messages)
	return out
}
