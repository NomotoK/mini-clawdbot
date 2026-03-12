package router

import "mini-clawdbot/internal/bus"

// SessionRouter 负责将统一消息字段路由为 SessionKey。
//
// 规则固定为 chat+thread：
// SessionKey = channel/account/chat/thread
// 当 thread 为空时回退为 root。
//
// 注意：渠道差异应在 adapter 内归一化。router 只消费统一字段。
type SessionRouter struct{}

// NewSessionRouter 创建会话路由器。
func NewSessionRouter() *SessionRouter {
	return &SessionRouter{}
}

// RouteInbound 将入站消息映射为统一 SessionKey。
func (r *SessionRouter) RouteInbound(msg *bus.InboundMessage) bus.SessionKey {
	if msg == nil {
		return ""
	}
	return bus.NewSessionKey(msg.Channel, msg.AccountID, msg.ChatID, msg.ThreadID)
}

// RouteOutbound 将出站消息映射为统一 SessionKey。
func (r *SessionRouter) RouteOutbound(msg *bus.OutboundMessage) bus.SessionKey {
	if msg == nil {
		return ""
	}
	return bus.NewSessionKey(msg.Channel, msg.AccountID, msg.ChatID, msg.ThreadID)
}
