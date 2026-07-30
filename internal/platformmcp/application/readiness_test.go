package application

import (
	"context"
	"errors"
	"testing"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
)

func TestReadinessRequiresTLSContractsAndBackend(t *testing.T) {
	tests := []struct {
		name       string
		tlsReady   bool
		tools      []mcpdomain.Tool
		backendErr error
		wantErr    bool
	}{
		{name: "ready", tlsReady: true, tools: phase1Tools()},
		{name: "TLS unavailable", tools: phase1Tools(), wantErr: true},
		{name: "contracts unavailable", tlsReady: true, wantErr: true},
		{name: "backend unavailable", tlsReady: true, tools: phase1Tools(), backendErr: errors.New("down"), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			readiness := NewReadiness(
				tlsReadinessFake{ready: tc.tlsReady},
				toolCatalogFake{tools: tc.tools},
				backendReadinessFake{err: tc.backendErr},
			)

			err := readiness.Check(t.Context())

			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

type tlsReadinessFake struct {
	ready bool
}

func (f tlsReadinessFake) Ready() bool {
	return f.ready
}

type toolCatalogFake struct {
	tools []mcpdomain.Tool
}

func (f toolCatalogFake) ListTools(context.Context) []mcpdomain.Tool {
	return f.tools
}

type backendReadinessFake struct {
	err error
}

func (f backendReadinessFake) Check(context.Context) error {
	return f.err
}
