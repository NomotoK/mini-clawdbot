package bus

// Config 定义 MessageBus 的缓冲配置。
type Config struct {
	InboundBuffer    int
	OutboundBuffer   int
	StreamBuffer     int
	ErrorBuffer      int
	SubscriberBuffer int
}

func (c Config) normalize() Config {
	cfg := c
	if cfg.InboundBuffer <= 0 {
		cfg.InboundBuffer = defaultTopicBuffer
	}
	if cfg.OutboundBuffer <= 0 {
		cfg.OutboundBuffer = defaultTopicBuffer
	}
	if cfg.StreamBuffer <= 0 {
		cfg.StreamBuffer = defaultTopicBuffer
	}
	if cfg.ErrorBuffer <= 0 {
		cfg.ErrorBuffer = defaultTopicBuffer
	}
	if cfg.SubscriberBuffer <= 0 {
		cfg.SubscriberBuffer = defaultSubscriberBuffer
	}
	return cfg
}
