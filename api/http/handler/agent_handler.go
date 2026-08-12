// Package handler — agent_handler.go.
//
// Transport-only seam for agent HTTP endpoints. AgentHandler binds
// requests, extracts tenant/user/trace from middleware context,
// delegates to AgentService, and renders DTOs. No SQL, no
// infrastructure imports. Wire DTOs live in agent_dto.go.
package handler

import (
	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

// AgentHandler is a transport-only façade. All business logic lives in
// agent.AgentService; this type only holds the service handle and a
// logger for transport-layer events.
type AgentHandler struct {
	svc    *agent.AgentService
	logger *zap.Logger
	// actionExecutor 执行已批准的动作（D4/D5 审批执行端点）。由 wiring 装配；
	// 为 nil 时执行端点返回 500（fail closed，不静默跳过审批）。
	actionExecutor port.ApprovalActionExecutor
}

// NewAgentHandler constructs an AgentHandler. The service handle is
// produced by api/wiring; nothing else is allowed in.
func NewAgentHandler(svc *agent.AgentService, logger *zap.Logger) *AgentHandler {
	return &AgentHandler{svc: svc, logger: logger}
}

// WithActionExecutor 装配审批动作执行器（wiring 调用，仅在执行端点使用）。
func (h *AgentHandler) WithActionExecutor(executor port.ApprovalActionExecutor) *AgentHandler {
	h.actionExecutor = executor
	return h
}
