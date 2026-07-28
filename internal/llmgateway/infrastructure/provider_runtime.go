package infrastructure

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
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
		Name:        provider.Name,
		BaseURL:     provider.BaseURL,
		APIKey:      provider.APIKey,
		HealthModel: provider.DefaultModel,
	}, nil
}

// ListModels lists models exposed by the configured provider.
func (r *ProviderRuntime) ListModels(ctx context.Context, provider domain.Provider) ([]string, error) {
	proto, cfg, err := r.protocol(provider)
	if err != nil {
		return nil, err
	}
	return proto.ListModels(ctx, cfg)
}

// Health checks whether the configured provider is reachable.
func (r *ProviderRuntime) Health(ctx context.Context, provider domain.Provider) error {
	proto, cfg, err := r.protocol(provider)
	if err != nil {
		return err
	}
	return proto.Health(ctx, cfg)
}
