package pipeline

import "time"

// DynamicConfig 是运行期可热更新的 pipeline 调度参数。
// 由 Nacos listener 经 wiring 桥接后原子替换；零值字段回退静态 Config。
type DynamicConfig struct {
	PollInterval time.Duration
	BatchSize    int
}

// Config 不含 NATS 地址：连接由 wiring 注入（平台共享连接），
// pipeline 只使用不建立连接。
type Config struct {
	Enabled               bool          `mapstructure:"enabled"`
	PollInterval          time.Duration `mapstructure:"poll_interval"`
	BatchSize             int           `mapstructure:"batch_size"`
	EmbedWorkers          int           `mapstructure:"embed_workers"`
	EnrichWorkers         int           `mapstructure:"enrich_workers"`
	EmbedAckWait          time.Duration `mapstructure:"embed_ack_wait"`
	EnrichAckWait         time.Duration `mapstructure:"enrich_ack_wait"`
	MaxDeliver            int           `mapstructure:"max_deliver"`
	EnrichModel           string        `mapstructure:"enrich_model"`
	SummaryModel          string        `mapstructure:"summary_model"`
	SummaryTokenThreshold int           `mapstructure:"summary_token_threshold"`
	EnrichmentPrompt      string        `mapstructure:"enrichment_prompt"`
	SummaryPrompt         string        `mapstructure:"summary_prompt"`
}
