// Package mcpnode provides node identity and multi-pod ownership helpers.
package mcpnode

import (
	"os"
	"time"
)

const (
	// HeartbeatInterval is the cadence at which the owner node refreshes
	// its heartbeat in mcp_configs.
	HeartbeatInterval = 30 * time.Second
	// FailoverTimeout is how long a heartbeat may be stale before another
	// node takes over the stdio server.
	FailoverTimeout = 90 * time.Second
)

// NodeID reads the stable node identifier from STRATUM_NODE_ID or falls
// back to os.Hostname.
func NodeID() string {
	if id := os.Getenv("STRATUM_NODE_ID"); id != "" {
		return id
	}
	host, _ := os.Hostname()
	return host
}
