package bus

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// ErrBusClosed 表示消息总线已关闭。
var ErrBusClosed = errors.New("message bus is closed")

// defaultTopicBuffer 定义了话题内部消息队列的默认缓冲大小，控制发布者和分发器之间的背压程度。
// defaultSubscriberBuffer 定义了每个订阅者通道的默认缓冲大小，控制分发器和订阅者之间的背压程度。
const (
	defaultTopicBuffer      = 128
	defaultSubscriberBuffer = 32
)

var eventSeq uint64// eventSeq 是一个全局递增的事件序列号，用于生成唯一的事件标识符。

// nextEventID 生成全局递增的事件标识符，格式: "evt-{timestamp}-{sequence}"。
func nextEventID() string {
	seq := atomic.AddUint64(&eventSeq, 1)
	return fmt.Sprintf("evt-%d-%d", time.Now().UnixNano(), seq)
}
