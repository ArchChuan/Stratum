package wiring

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	capgateway "github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/constants"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
)

type tenantCapabilityResolver struct {
	db           *pgxpool.Pool
	aesKey       [32]byte
	cache        *llmgateway.TenantGatewayCache
	skillAdapter agentport.Adapter
	logger       *zap.Logger
}

func newTenantCapabilityResolver(
	db *pgxpool.Pool,
	aesKey [32]byte,
	cache *llmgateway.TenantGatewayCache,
	skillAdapter agentport.Adapter,
	logger *zap.Logger,
) agentport.TenantCapabilityResolver {
	return &tenantCapabilityResolver{
		db:           db,
		aesKey:       aesKey,
		cache:        cache,
		skillAdapter: skillAdapter,
		logger:       logger,
	}
}

func (r *tenantCapabilityResolver) resolveGateway(ctx context.Context, tenantID string) (*llmgateway.Gateway, map[string]string, bool) {
	if r.db == nil || r.cache == nil {
		return nil, nil, false
	}
	if gw, keys, ok := r.cache.Get(tenantID); ok {
		return gw, keys, true
	}

	var settingsJSON []byte
	if err := r.db.QueryRow(ctx,
		"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
		tenantID,
	).Scan(&settingsJSON); err != nil {
		r.logger.Warn("tenantCapabilityResolver: settings query failed",
			zap.String("tenant_id", tenantID), zap.Error(err))
		return nil, nil, false
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(settingsJSON, &settings); err != nil {
		return nil, nil, false
	}

	apiKeysRaw, ok := settings["llm_api_keys"].(map[string]interface{})
	if !ok || len(apiKeysRaw) == 0 {
		return nil, nil, false
	}

	decrypted := make(map[string]string, len(apiKeysRaw))
	for provider, enc := range apiKeysRaw {
		encStr, ok := enc.(string)
		if !ok || encStr == "" {
			continue
		}
		plain, err := pkgcrypto.Decrypt(r.aesKey, encStr)
		if err != nil {
			r.logger.Warn("tenantCapabilityResolver: decrypt failed",
				zap.String("tenant_id", tenantID), zap.String("provider", provider))
			continue
		}
		decrypted[provider] = plain
	}

	if len(decrypted) == 0 {
		return nil, nil, false
	}

	gw := llmgateway.NewGateway().WithLogger(r.logger)
	if qwenKey, ok := decrypted["qwen"]; ok {
		qwenClient := llmgateway.NewQwenClient(qwenKey, r.logger)
		gw.RegisterClient(llmgateway.ProviderQwen, qwenClient)
		gw.RegisterEmbeddingClient(llmgateway.ProviderQwen, qwenClient)
	}
	if zhipuKey, ok := decrypted["zhipu"]; ok {
		zhipuClient := llmgateway.NewZhipuClient(zhipuKey, r.logger)
		gw.RegisterClient(llmgateway.ProviderZhipu, zhipuClient)
		gw.RegisterEmbeddingClient(llmgateway.ProviderZhipu, zhipuClient)
	}
	for _, pref := range []llmgateway.ModelProvider{llmgateway.ProviderQwen, llmgateway.ProviderZhipu} {
		if _, ok := decrypted[string(pref)]; ok {
			gw.SetDefault(pref)
			break
		}
	}

	r.cache.Set(tenantID, gw, decrypted, constants.GatewayCacheTTL)
	return gw, decrypted, true
}

// Resolve returns a per-tenant CapabilityGateway and the raw API-key map.
func (r *tenantCapabilityResolver) Resolve(ctx context.Context, tenantID string) (agentport.CapabilityGateway, map[string]string, bool) {
	gw, keys, ok := r.resolveGateway(ctx, tenantID)
	if !ok {
		return nil, nil, false
	}
	llmAdapter := capgateway.NewLLMAdapter(gw, r.logger)
	capGW := capgateway.NewDefaultCapabilityGateway(llmAdapter, r.skillAdapter, r.logger)
	return capGW, keys, true
}

// InjectCompleter injects the per-tenant LLM completer into ctx for streaming.
func (r *tenantCapabilityResolver) InjectCompleter(ctx context.Context, tenantID string) context.Context {
	gw, _, ok := r.resolveGateway(ctx, tenantID)
	if !ok {
		return ctx
	}
	return llmgatewaydomain.WithCompleter(ctx, gw)
}
