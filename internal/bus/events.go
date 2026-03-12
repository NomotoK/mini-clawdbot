// Package bus 提供统一的事件模型和消息总线实现，支持多渠道、多主题的发布订阅功能。
//
// 该包定义了系统中的核心事件类型（InboundMessage、OutboundMessage、StreamEvent、ErrorEvent）
// 以及统一的会话标识（SessionKey）。MessageBus 实现了高性能的多渠道事件分发，支持流式背压处理
// 和错误事件追踪。
package bus

import (
	"fmt"
	"strings"
	"time"
)

// SessionKey 是跨渠道统一的会话主键，格式为 channel/account/chat/thread。
// SessionKey 用于关联来自不同渠道的同一用户会话，支持线程级别的隔离。
// 空的 threadID 会自动回退到 "root"，确保会话键的有效性。
type SessionKey string

const defaultThreadID = "root"

// NewSessionKey 生成统一会话键。
//
// 参数：
//   - channel: 消息渠道标识，如 "telegram"、"slack" 等
//   - accountID: 渠道账户ID，表示机器人或应用在该渠道的身份
//   - chatID: 聊天/群组ID，标识具体的对话场景
//   - threadID: 线程ID，用于支持线程化对话；为空时自动使用默认值 "root"
//
// 返回值：
//
//	SessionKey 格式为 "channel/accountID/chatID/threadID"，例如 "telegram/bot-1/chat-123/thread-45"
//
// 示例：
//
//	key := NewSessionKey("telegram", "bot-1", "chat-123", "")
//	// 返回: "telegram/bot-1/chat-123/root"
func NewSessionKey(channel, accountID, chatID, threadID string) SessionKey {
	thread := strings.TrimSpace(threadID)
	if thread == "" {
		thread = defaultThreadID
	}
	return SessionKey(fmt.Sprintf("%s/%s/%s/%s", channel, accountID, chatID, thread))
}

// InboundMessage 表示从渠道进入系统的消息事件。
// InboundMessage 由各渠道适配器生成，封装了用户的输入，并包含追踪和会话信息。
//
// 字段说明：
//   - EventID: 事件的唯一标识符，由系统自动生成（格式: evt-{timestamp}-{seq}）
//   - TraceID: 用于关联整条请求链路的追踪ID，默认值为 EventID
//   - Channel: 消息来源渠道，如 "telegram"、"slack"、"wechat" 等
//   - AccountID: 接收消息的账户ID（机器人或应用在该渠道的身份）
//   - ChatID: 聊天/群组ID，标识消息的来自哪个对话
//   - ThreadID: 线程ID，支持线程化对话；为空时默认为 "root"
//   - SenderID: 消息发送者的ID，用于追踪消息来源
//   - Content: 消息内容，可以是文本、JSON或其他格式
//   - Metadata: 附加元数据，用于存储渠道特定的信息
//   - Timestamp: 消息进入系统的时间戳
type InboundMessage struct {
	EventID   string
	TraceID   string
	Channel   string
	AccountID string
	ChatID    string
	ThreadID  string
	SenderID  string
	Content   string
	Metadata  map[string]any
	Timestamp time.Time
}

// SessionKey 返回该消息对应的统一会话键。
// 返回值为 "channel/accountID/chatID/threadID" 格式，用于会话级别的操作和追踪。
func (m *InboundMessage) SessionKey() SessionKey {
	if m == nil {
		return ""
	}
	return NewSessionKey(m.Channel, m.AccountID, m.ChatID, m.ThreadID)
}

// OutboundMessage 表示系统发往渠道的消息事件。
// OutboundMessage 由系统处理流程生成，用于向渠道发送响应或通知。
//
// 字段说明：
//   - EventID: 事件的唯一标识符，由系统自动生成
//   - TraceID: 用于关联整条请求链路的追踪ID
//   - Channel: 目标渠道，如 "telegram"、"slack" 等
//   - AccountID: 发送方账户ID
//   - ChatID: 目标对话/群组ID
//   - ThreadID: 线程ID，支持回复到具体的线程
//   - ReplyTo: 所回复的消息ID，若非空表示这是一条回复消息
//   - Content: 消息内容
//   - Metadata: 附加元数据，可用于渠道特定的配置（如回复选项、格式等）
//   - Timestamp: 消息发出的时间戳
type OutboundMessage struct {
	EventID   string
	TraceID   string
	Channel   string
	AccountID string
	ChatID    string
	ThreadID  string
	ReplyTo   string
	Content   string
	Metadata  map[string]any
	Timestamp time.Time
}

// SessionKey 返回该消息对应的统一会话键。
// 返回值为 "channel/accountID/chatID/threadID" 格式，用于会话级别的操作。
func (m *OutboundMessage) SessionKey() SessionKey {
	if m == nil {
		return ""
	}
	return NewSessionKey(m.Channel, m.AccountID, m.ChatID, m.ThreadID)
}

// StreamEvent 表示流式输出分片事件。
// StreamEvent 用于传输从 LLM 或其他处理流生成的流式数据片段。
// MessageBus 对 StreamEvent 采用 "最新优先" 策略处理背压，确保不会阻塞发布者。
//
// 字段说明：
//   - EventID: 事件的唯一标识符
//   - TraceID: 用于关联整条请求链路的追踪ID
//   - SessionKey: 所属会话的统一键
//   - Seq: 分片的序列号，用于排序和检测缺失
//   - Delta: 本分片的增量内容，通常是文本块或 JSON 片段
//   - IsFinal: 标记这是否为最后一个分片
//   - Timestamp: 分片生成的时间戳
type StreamEvent struct {
	EventID    string
	TraceID    string
	SessionKey SessionKey
	Seq        int
	Delta      string
	IsFinal    bool
	Timestamp  time.Time
}

// ErrorEvent 表示链路中的错误事件。
// ErrorEvent 用于记录处理流程中发生的异常，支持错误追踪和分析。
//
// 字段说明：
//   - EventID: 事件的唯一标识符
//   - TraceID: 用于关联整条请求链路的追踪ID
//   - SessionKey: 出错所属会话的统一键
//   - Stage: 错误所在的处理阶段，如 "input"、"tool"、"inference" 等
//   - Code: 错误代码，用于程序化区分错误类型
//   - Message: 面向用户的错误描述
//   - Cause: 面向开发者的原因说明，通常包含技术细节
//   - Timestamp: 错误发生的时间戳
type ErrorEvent struct {
	EventID    string
	TraceID    string
	SessionKey SessionKey
	Stage      string
	Code       string
	Message    string
	Cause      string
	Timestamp  time.Time
}
