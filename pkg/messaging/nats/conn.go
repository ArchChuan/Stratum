// Package nats wraps NATS connection setup with safe defaults.
package nats

import (
	"time"

	"github.com/nats-io/nats.go"
)

// Connect dials a NATS server with safe reconnect/timeout defaults.
// Caller-supplied opts are appended last and override the defaults.
func Connect(url string, opts ...nats.Option) (*nats.Conn, error) {
	defaults := []nats.Option{
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.Timeout(10 * time.Second),
	}
	return nats.Connect(url, append(defaults, opts...)...)
}
