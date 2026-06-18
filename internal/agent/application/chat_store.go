// Package application defines the chat-store port consumed by the agent
// runtime. The Postgres adapter lives in
// internal/agent/infrastructure/persistence (PgChatStore). Application
// imports nothing below this layer.

package application

import (
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
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

// ChatStore is an alias for port.ChatRepo. Canonical definition lives in
// internal/agent/domain/port/repository.go.
type ChatStore = port.ChatRepo
