// Package application defines the chat-store port consumed by the agent
// runtime. The Postgres adapter lives in
// internal/agent/infrastructure/persistence (PgChatStore). Application
// imports nothing below this layer.

package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

// Type aliases keep `application.ChatConversation` /
// `application.ChatMessage` / `application.ErrNotFound` source-compatible
// after the canonical types were hoisted into domain.
type (
	ChatConversation = domain.ChatConversation
	ChatMessage      = domain.ChatMessage
)

// Sentinel aliases. Centralized in domain/agent.go.
var (
	ErrNotFound     = domain.ErrNotFound
	ErrNameConflict = domain.ErrNameConflict
	ErrInvalidSkill = domain.ErrInvalidSkill
)

// ChatStore persists chat conversations and messages in the per-tenant schema.
type ChatStore interface {
	CreateConversation(ctx context.Context, tenantID, agentID, userID, name string) (*ChatConversation, error)
	ListConversations(ctx context.Context, tenantID, agentID, userID string) ([]*ChatConversation, error)
	RenameConversation(ctx context.Context, tenantID, convID, userID, name string) error
	DeleteConversation(ctx context.Context, tenantID, convID, userID string) error
	AddMessage(ctx context.Context, tenantID string, msg *ChatMessage) error
	ListMessages(ctx context.Context, tenantID, convID, userID string) ([]*ChatMessage, error)
	CleanupExpired(ctx context.Context, tenantID string) error
}
