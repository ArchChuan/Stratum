package pipeline

import "time"

type Config struct {
	Enabled               bool          `mapstructure:"enabled"`
	NatsURL               string        `mapstructure:"nats_url"`
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
