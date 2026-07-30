package application

import (
	"context"
	"errors"
	"fmt"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
)

type TLSReadiness interface {
	Ready() bool
}

type ContractCatalog interface {
	ListTools(context.Context) []mcpdomain.Tool
}

type BackendReadiness interface {
	Check(context.Context) error
}

type Readiness struct {
	tls       TLSReadiness
	contracts ContractCatalog
	backend   BackendReadiness
}

func NewReadiness(tls TLSReadiness, contracts ContractCatalog, backend BackendReadiness) *Readiness {
	return &Readiness{tls: tls, contracts: contracts, backend: backend}
}

func (r *Readiness) Check(ctx context.Context) error {
	if r.tls == nil || !r.tls.Ready() {
		return errors.New("Platform MCP TLS is not ready")
	}
	if r.contracts == nil || !hasPhase1Contracts(r.contracts.ListTools(ctx)) {
		return errors.New("Platform MCP contracts are not ready")
	}
	if r.backend == nil {
		return errors.New("Stratum backend readiness is not configured")
	}
	if err := r.backend.Check(ctx); err != nil {
		return fmt.Errorf("check Stratum backend readiness: %w", err)
	}
	return nil
}

func hasPhase1Contracts(tools []mcpdomain.Tool) bool {
	if len(tools) != len(platformmcp.Phase1ToolNames) {
		return false
	}
	for i, name := range platformmcp.Phase1ToolNames {
		if tools[i].Name != name || tools[i].InputSchema == nil {
			return false
		}
	}
	return true
}
