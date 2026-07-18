package wiring

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	capgateway "github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/constants"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
)

type tenantSettingsQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

type tenantCapabilityResolver struct {
	db           tenantSettingsQuerier
	aesKey       [32]byte
	cache        *llmgateway.TenantGatewayCache
	fallback     *llmgateway.Gateway
	logger       *zap.Logger
	qwenBaseURL  string
	zhipuBaseURL string
}

func newTenantCapabilityResolver(
	db *pgxpool.Pool,
	aesKey [32]byte,
	cache *llmgateway.TenantGatewayCache,
	fallback *llmgateway.Gateway,
	logger *zap.Logger,
	qwenBaseURL, zhipuBaseURL string,
) agentport.TenantCapabilityResolver {
	var settingsDB tenantSettingsQuerier
	if db != nil {
		settingsDB = db
	}
	return &tenantCapabilityResolver{
		db:           settingsDB,
		aesKey:       aesKey,
		cache:        cache,
		fallback:     fallback,
		logger:       logger,
		qwenBaseURL:  qwenBaseURL,
		zhipuBaseURL: zhipuBaseURL,
	}
}

func (r *tenantCapabilityResolver) resolveGateway(ctx context.Context, tenantID string) (*llmgateway.Gateway, map[string]string, bool) {
	gw, keys, ok, _ := r.resolveGatewayResult(ctx, tenantID, false)
	return gw, keys, ok
}

func (r *tenantCapabilityResolver) resolveGatewayResult(ctx context.Context, tenantID string, strict bool) (*llmgateway.Gateway, map[string]string, bool, error) {
	if r.db == nil || r.cache == nil {
		if strict {
			return nil, nil, false, fmt.Errorf("tenant llm: database unavailable")
		}
		return r.fallback, nil, r.fallback != nil, nil
	}
	if gw, keys, ok, generation := r.cache.GetWithGeneration(tenantID); ok {
		return gw, keys, true, nil
	} else {
		return r.loadGateway(ctx, tenantID, generation, strict)
	}
}

func (r *tenantCapabilityResolver) loadGateway(ctx context.Context, tenantID string, generation uint64, strict bool) (*llmgateway.Gateway, map[string]string, bool, error) {
	defer r.cache.ReleaseLoad(tenantID, generation)

	var settingsJSON []byte
	if err := r.db.QueryRow(ctx,
		"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
		tenantID,
	).Scan(&settingsJSON); err != nil {
		r.logger.Warn("tenantCapabilityResolver: settings query failed",
			zap.String("tenant_id", tenantID), zap.Error(err))
		if strict {
			return nil, nil, false, fmt.Errorf("tenant llm: settings query: %w", err)
		}
		return r.fallback, nil, r.fallback != nil, nil
	}

	var settings map[string]any
	if err := json.Unmarshal(settingsJSON, &settings); err != nil {
		if strict {
			return nil, nil, false, fmt.Errorf("tenant llm: decode settings: %w", err)
		}
		return r.fallback, nil, r.fallback != nil, nil
	}

	apiKeysRaw, ok := settings["llm_api_keys"].(map[string]any)
	if !ok || len(apiKeysRaw) == 0 {
		if r.fallback != nil {
			return r.fallback, nil, true, nil
		}
		if strict {
			return nil, nil, false, fmt.Errorf("tenant llm: no provider configured")
		}
		return nil, nil, false, nil
	}

	decrypted := make(map[string]string, len(apiKeysRaw))
	var decryptErr error
	for provider, enc := range apiKeysRaw {
		encStr, ok := enc.(string)
		if !ok || encStr == "" {
			continue
		}
		plain, err := pkgcrypto.Decrypt(r.aesKey, encStr)
		if err != nil {
			decryptErr = err
			r.logger.Warn("tenantCapabilityResolver: decrypt failed",
				zap.String("tenant_id", tenantID), zap.String("provider", provider))
			continue
		}
		decrypted[provider] = plain
	}

	if len(decrypted) == 0 {
		if strict && decryptErr != nil {
			return nil, nil, false, fmt.Errorf("tenant llm: decrypt credentials: %w", decryptErr)
		}
		if r.fallback != nil {
			return r.fallback, nil, true, nil
		}
		if strict {
			return nil, nil, false, fmt.Errorf("tenant llm: no usable provider configured")
		}
		return nil, nil, false, nil
	}

	gw := llmgateway.NewGateway().WithLogger(r.logger)
	registered := false
	if qwenKey, ok := decrypted["qwen"]; ok {
		qwenClient := llmgateway.NewQwenClient(qwenKey, r.logger)
		if r.qwenBaseURL != "" {
			qwenClient = llmgateway.NewQwenClientWithBase(qwenKey, r.qwenBaseURL, r.logger)
		}
		gw.RegisterClient(llmgateway.ProviderQwen, qwenClient)
		gw.RegisterEmbeddingClient(llmgateway.ProviderQwen, qwenClient)
		registered = true
	}
	if zhipuKey, ok := decrypted["zhipu"]; ok {
		zhipuClient := llmgateway.NewZhipuClient(zhipuKey, r.logger)
		if r.zhipuBaseURL != "" {
			zhipuClient = llmgateway.NewZhipuClientWithBase(zhipuKey, r.zhipuBaseURL, r.logger)
		}
		gw.RegisterClient(llmgateway.ProviderZhipu, zhipuClient)
		gw.RegisterEmbeddingClient(llmgateway.ProviderZhipu, zhipuClient)
		registered = true
	}
	if !registered {
		if strict {
			return nil, nil, false, fmt.Errorf("tenant llm: no supported provider configured")
		}
		return r.fallback, nil, r.fallback != nil, nil
	}
	for _, pref := range []llmgateway.ModelProvider{llmgateway.ProviderQwen, llmgateway.ProviderZhipu} {
		if _, ok := decrypted[string(pref)]; ok {
			gw.SetDefault(pref)
			break
		}
	}

	if !r.cache.SetIfGeneration(tenantID, gw, decrypted, constants.GatewayCacheTTL, generation) {
		if strict {
			return nil, nil, false, fmt.Errorf("tenant llm: configuration changed during resolve")
		}
		return r.fallback, nil, r.fallback != nil, nil
	}
	return gw, decrypted, true, nil
}

// Resolve returns a per-tenant CapabilityGateway and the raw API-key map.
func (r *tenantCapabilityResolver) Resolve(ctx context.Context, tenantID string) (agentport.CapabilityGateway, map[string]string, bool) {
	gw, keys, ok := r.resolveGateway(ctx, tenantID)
	if !ok {
		return nil, nil, false
	}
	llmAdapter := capgateway.NewLLMAdapter(gw, r.logger)
	capGW := capgateway.NewDefaultCapabilityGateway(llmAdapter, r.logger)
	return capGW, keys, true
}

// ResolveLLM returns the tenant's LLM gateway as a pipeline.LLMClient. Returns
// nil when the tenant has no provider configured. Used by the memory pipeline
// to drive enrich/summary jobs against tenant-private gateways.
func (r *tenantCapabilityResolver) ResolveLLM(ctx context.Context, tenantID string) *llmgateway.Gateway {
	gw, _, ok := r.resolveGateway(ctx, tenantID)
	if !ok {
		return nil
	}
	return gw
}

// ResolveWorkerLLM resolves the current tenant gateway without hiding
// infrastructure or credential failures behind the global fallback.
func (r *tenantCapabilityResolver) ResolveWorkerLLM(ctx context.Context, tenantID string) (*llmgateway.Gateway, error) {
	gw, _, ok, err := r.resolveGatewayResult(ctx, tenantID, true)
	if err != nil {
		return nil, err
	}
	if !ok || gw == nil {
		return nil, fmt.Errorf("tenant llm: unavailable")
	}
	return gw, nil
}

// InjectCompleter injects the per-tenant LLM completer into ctx for streaming.
func (r *tenantCapabilityResolver) InjectCompleter(ctx context.Context, tenantID string) context.Context {
	gw, _, ok := r.resolveGateway(ctx, tenantID)
	if !ok {
		return ctx
	}
	return llmgatewaydomain.WithCompleter(ctx, gw)
}
