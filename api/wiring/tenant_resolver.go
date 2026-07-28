package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	capgateway "github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
)

type tenantSettingsQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type tenantCapabilityResolver struct {
	db       tenantSettingsQuerier
	aesKey   [32]byte
	registry *llmgateway.ModelRegistry
	gateway  *llmgateway.Gateway
	logger   *zap.Logger
}

func (r *tenantCapabilityResolver) DiagnosticModelStatus(
	ctx context.Context, tenantID string,
) (status agentdomain.TenantModelDiagnosticStatus, err error) {
	if r.db == nil {
		return status, fmt.Errorf("tenant model diagnostics: settings unavailable")
	}
	var settingsJSON []byte
	if err := r.db.QueryRow(ctx,
		"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL", tenantID,
	).Scan(&settingsJSON); err != nil {
		return status, fmt.Errorf("tenant model diagnostics: settings read failed")
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsJSON, &settings); err != nil {
		return status, fmt.Errorf("tenant model diagnostics: settings invalid")
	}
	apiKeys, ok := settings["llm_api_keys"].(map[string]any)
	if !ok || len(apiKeys) == 0 {
		return status, nil
	}
	for _, provider := range []string{"qwen", "zhipu"} {
		encrypted, ok := apiKeys[provider].(string)
		if !ok || encrypted == "" {
			continue
		}
		if _, err := pkgcrypto.Decrypt(r.aesKey, encrypted); err != nil {
			return status, fmt.Errorf("tenant model diagnostics: credentials invalid")
		}
		status.Configured = true
		return status, nil
	}
	return status, fmt.Errorf("tenant model diagnostics: provider unsupported")
}

func newTenantCapabilityResolver(
	db *pgxpool.Pool,
	aesKey [32]byte,
	registry *llmgateway.ModelRegistry,
	gateway *llmgateway.Gateway,
	logger *zap.Logger,
) agentport.TenantCapabilityResolver {
	var settingsDB tenantSettingsQuerier
	if db != nil {
		settingsDB = db
	}
	return &tenantCapabilityResolver{
		db:       settingsDB,
		aesKey:   aesKey,
		registry: registry,
		gateway:  gateway,
		logger:   logger,
	}
}

func (r *tenantCapabilityResolver) resolveGateway(ctx context.Context, tenantID string) (*llmgateway.Gateway, bool) {
	gw, ok, _ := r.resolveGatewayResult(ctx, tenantID, false)
	return gw, ok
}

func (r *tenantCapabilityResolver) resolveGatewayResult(ctx context.Context, tenantID string, strict bool) (*llmgateway.Gateway, bool, error) {
	if r.registry == nil {
		if strict {
			return nil, false, fmt.Errorf("tenant llm: registry unavailable")
		}
		return r.gateway, r.gateway != nil, nil
	}
	if err := r.registry.WarmTenant(ctx, tenantID); err != nil {
		if strict {
			return nil, false, fmt.Errorf("tenant llm: warm: %w", err)
		}
		return r.gateway, r.gateway != nil, nil
	}
	return r.gateway, true, nil
}

// Resolve returns a per-tenant CapabilityGateway.
func (r *tenantCapabilityResolver) Resolve(ctx context.Context, tenantID string) (agentport.CapabilityGateway, bool) {
	gw, ok := r.resolveGateway(ctx, tenantID)
	if !ok {
		return nil, false
	}
	llmAdapter := newAgentLLMAdapter(gw)
	capGW := capgateway.NewDefaultCapabilityGateway(llmAdapter, r.logger)
	return capGW, true
}

// ResolveLLM returns the tenant's LLM gateway as a pipeline.LLMClient. Returns
// nil when the tenant has no provider configured. Used by the memory pipeline
// to drive enrich/summary jobs against tenant-private gateways.
func (r *tenantCapabilityResolver) ResolveLLM(ctx context.Context, tenantID string) *llmgateway.Gateway {
	gw, ok := r.resolveGateway(ctx, tenantID)
	if !ok {
		return nil
	}
	return gw
}

// ResolveWorkerLLM resolves the current tenant gateway without hiding
// infrastructure or credential failures behind the global fallback.
func (r *tenantCapabilityResolver) ResolveWorkerLLM(ctx context.Context, tenantID string) (*llmgateway.Gateway, error) {
	gw, ok, err := r.resolveGatewayResult(ctx, tenantID, true)
	if err != nil {
		return nil, err
	}
	if !ok || gw == nil {
		return nil, fmt.Errorf("tenant llm: unavailable")
	}
	return gw, nil
}

func (r *tenantCapabilityResolver) ValidateTenantChatModel(ctx context.Context, tenantID, model string) error {
	if r.registry == nil {
		return fmt.Errorf("tenant llm model validation: %w", agentdomain.ErrAssistantModelUnavailable)
	}
	names, err := r.registry.ListChatModelsByTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("tenant llm model validation: %w", err)
	}
	for _, n := range names {
		if n == model {
			return nil
		}
	}
	return agentdomain.ErrInvalidSystemAssistantModel
}

func (r *tenantCapabilityResolver) ListTenantChatModels(ctx context.Context, tenantID string) ([]string, error) {
	if r.registry == nil {
		return []string{}, nil
	}
	names, err := r.registry.ListChatModelsByTenant(ctx, tenantID)
	if err != nil {
		if errors.Is(err, agentdomain.ErrAssistantModelUnavailable) ||
			errors.Is(err, agentdomain.ErrInvalidSystemAssistantModel) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("tenant llm model catalogue: %w", err)
	}
	return names, nil
}

// InjectCompleter injects the per-tenant LLM completer into ctx for streaming.
func (r *tenantCapabilityResolver) InjectCompleter(ctx context.Context, tenantID string) context.Context {
	gw, ok := r.resolveGateway(ctx, tenantID)
	if !ok {
		return ctx
	}
	return llmgatewaydomain.WithCompleter(ctx, gw)
}
