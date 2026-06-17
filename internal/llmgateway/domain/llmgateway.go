// Package domain holds llmgateway context entities.
package domain

type ProviderKind string

const (
	ProviderOpenAI    ProviderKind = "openai"
	ProviderAnthropic ProviderKind = "anthropic"
	ProviderOllama    ProviderKind = "ollama"
)

type TenantSettings struct {
	TenantID, EncryptedAPIKey string
	Provider                  ProviderKind
}
