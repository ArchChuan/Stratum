// Package handler — agent_handler.go.
//
// Transport-only seam for agent HTTP endpoints. AgentHandler binds
// requests, extracts tenant/user/trace from middleware context,
// delegates to AgentService, and renders DTOs. No SQL, no
// infrastructure imports. Wire DTOs live in agent_dto.go.
package handler

import (
	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"go.uber.org/zap"
)

// AgentHandler is a transport-only façade. All business logic lives in
// agent.AgentService; this type only holds the service handle and a
// logger for transport-layer events.
type AgentHandler struct {
	svc    *agent.AgentService
	logger *zap.Logger
}

// NewAgentHandler constructs an AgentHandler. The service handle is
// produced by api/wiring; nothing else is allowed in.
func NewAgentHandler(svc *agent.AgentService, logger *zap.Logger) *AgentHandler {
	return &AgentHandler{svc: svc, logger: logger}
}
