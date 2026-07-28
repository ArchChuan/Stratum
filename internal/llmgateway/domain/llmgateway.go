// Package domain holds llmgateway context entities.
package domain

type TenantSettings struct {
	TenantID, EncryptedAPIKey string
	Provider                  ProviderKind
}
