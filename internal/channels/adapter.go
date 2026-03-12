package channels

import (
	"context"

	"mini-clawdbot/internal/bus"
)

// ChannelAdapter 定义渠道适配器最小接口。
// 字段说明：
//   - Name: 渠道名称，如 "telegram"、"slack" 等
//   - Start: 启动适配器，建立连接并开始接收消息
//   - Stop: 停止适配器，关闭连接并清理资源
//   - Send: 发送消息到渠道，通常由 MessageBus 调用
//   - Receive: 返回一个只读通道，用于接收来自渠道的 InboundMessage
type ChannelAdapter interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Send(ctx context.Context, msg *bus.OutboundMessage) error
	Receive() <-chan *bus.InboundMessage
}
