package process

import "time"

type Identity struct {
	PID        int    `json:"pid"`
	StartTicks uint64 `json:"start_ticks"`
}

type Process struct {
	Identity
	PPID       int      `json:"ppid"`
	Command    string   `json:"command"`
	Args       []string `json:"args"`
	Service    string   `json:"service,omitempty"`
	RSSBytes   uint64   `json:"rss_bytes"`
	PSSBytes   uint64   `json:"pss_bytes"`
	USSBytes   uint64   `json:"uss_bytes"`
	Registered bool     `json:"registered"`
	Orphan     bool     `json:"orphan"`
}

type ServiceSummary struct {
	Service   string `json:"service"`
	Processes int    `json:"processes"`
	RSSBytes  uint64 `json:"rss_bytes"`
	PSSBytes  uint64 `json:"pss_bytes"`
	USSBytes  uint64 `json:"uss_bytes"`
	Orphans   int    `json:"orphans"`
}

type Snapshot struct {
	Version    int              `json:"version"`
	CapturedAt time.Time        `json:"captured_at"`
	Mode       string           `json:"mode"`
	Processes  []Process        `json:"processes"`
	Services   []ServiceSummary `json:"services"`
	Warnings   []string         `json:"warnings"`
}
