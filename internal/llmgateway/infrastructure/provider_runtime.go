package infrastructure

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

// ProviderRuntime adapts provider domain data to infrastructure protocols.
type ProviderRuntime struct {
	chatProtos map[domain.ProviderKind]ChatProtocol
}

// NewProviderRuntime creates a provider runtime backed by protocol adapters.
func NewProviderRuntime(chatProtos map[domain.ProviderKind]ChatProtocol) *ProviderRuntime {
	return &ProviderRuntime{chatProtos: chatProtos}
}

func (r *ProviderRuntime) protocol(provider domain.Provider) (ChatProtocol, ProviderConfig, error) {
	proto, ok := r.chatProtos[provider.Kind]
	if !ok {
		return nil, ProviderConfig{}, fmt.Errorf("no protocol for kind %q", provider.Kind)
	}
	return proto, ProviderConfig{
		Name:         provider.Name,
		BaseURL:      provider.BaseURL,
		APIKey:       provider.APIKey,
		HealthModel:  provider.DefaultModel,
		ModelCatalog: ZhipuModelCatalog(provider.BaseURL),
		ExtraHeaders: provider.ExtraHeaders,
	}, nil
}

// ListModels lists models exposed by the configured provider.
func (r *ProviderRuntime) ListModels(ctx context.Context, provider domain.Provider) ([]port.DiscoveredModel, error) {
	proto, cfg, err := r.protocol(provider)
	if err != nil {
		return nil, err
	}
	infraModels, err := proto.ListModels(ctx, cfg)
	if err != nil {
		return nil, err
	}
	out := make([]port.DiscoveredModel, len(infraModels))
	for i, m := range infraModels {
		out[i] = port.DiscoveredModel{
			Name:            m.Name,
			ContextWindow:   m.ContextWindow,
			MaxOutputTokens: m.MaxOutputTokens,
		}
	}
	return out, nil
}

// Health checks whether the configured provider is reachable.
func (r *ProviderRuntime) Health(ctx context.Context, provider domain.Provider) error {
	proto, cfg, err := r.protocol(provider)
	if err != nil {
		return err
	}
	return proto.Health(ctx, cfg)
}
