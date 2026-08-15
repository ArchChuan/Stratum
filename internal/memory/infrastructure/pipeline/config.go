package pipeline

import "time"

// DynamicConfig 是运行期可热更新的 pipeline 调度参数。
// 由 Nacos listener 经 wiring 桥接后原子替换；零值字段回退静态 Config。
type DynamicConfig struct {
	PollInterval time.Duration
	BatchSize    int
}

// Config 不含 NATS 地址：连接由 wiring 注入（平台共享连接），
// pipeline 只使用不建立连接。LLM 内容设置（模型/温度/提示词/摘要阈值）
// 已收口到平台参数（ScopePlatform memory.* key），运行态解析，不入 Config。
type Config struct {
	Enabled       bool          `mapstructure:"enabled"`
	PollInterval  time.Duration `mapstructure:"poll_interval"`
	BatchSize     int           `mapstructure:"batch_size"`
	EmbedWorkers  int           `mapstructure:"embed_workers"`
	EnrichWorkers int           `mapstructure:"enrich_workers"`
	EmbedAckWait  time.Duration `mapstructure:"embed_ack_wait"`
	EnrichAckWait time.Duration `mapstructure:"enrich_ack_wait"`
	MaxDeliver    int           `mapstructure:"max_deliver"`
}
