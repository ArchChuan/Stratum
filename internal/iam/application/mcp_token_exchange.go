package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrPlatformMCPInvocationInvalid  = errors.New("platform MCP invocation token invalid")
	ErrPlatformMCPIdentityInvalid    = errors.New("platform MCP managed identity invalid")
	ErrPlatformMCPBindingMissing     = errors.New("platform MCP binding missing")
	ErrPlatformMCPInvocationReplayed = errors.New("platform MCP invocation token already consumed")
	ErrPlatformMCPRoleInsufficient   = errors.New("platform MCP role insufficient")
	ErrPlatformMCPContractInvalid    = errors.New("platform MCP tool contract invalid")
	ErrPlatformMCPApprovalInvalid    = errors.New("platform MCP approval invalid")
)

type MCPTokenExchange struct {
	Tokens    iamport.MCPTokenExchangeTokenService
	Roles     iamport.TenantRoleResolver
	Bindings  iamport.PlatformMCPBindingReader
	Approvals iamport.PlatformMCPApprovalReader
	Replay    iamport.InvocationReplayStore
	Contracts platformmcp.ContractRegistry
}

type MCPTokenExchangeRequest struct {
	InvocationToken string
	ResourceID      string
}

type verifiedMCPInvocation struct {
	claims     *platformmcp.InvocationClaims
	role       string
	contract   platformmcp.ToolContract
	resourceID string
}

func NewMCPTokenExchange(exchange MCPTokenExchange) *MCPTokenExchange {
	return &exchange
}

func (s *MCPTokenExchange) Exchange(ctx context.Context, req MCPTokenExchangeRequest) (string, error) {
	claims, err := s.verifyInvocation(req.InvocationToken)
	if err != nil {
		return "", err
	}
	if err := s.validateBinding(ctx, claims); err != nil {
		return "", err
	}
	if err := s.consumeInvocation(ctx, claims); err != nil {
		return "", err
	}
	role, err := s.resolveRole(ctx, claims)
	if err != nil {
		return "", err
	}
	contract, err := s.resolveContract(claims.ToolName, role)
	if err != nil {
		return "", err
	}
	if err := s.validateApproval(ctx, claims, contract, req.ResourceID); err != nil {
		return "", err
	}
	return s.signDelegation(verifiedMCPInvocation{
		claims: claims, role: role, contract: contract, resourceID: req.ResourceID,
	})
}

func (s *MCPTokenExchange) verifyInvocation(raw string) (*platformmcp.InvocationClaims, error) {
	claims, err := s.Tokens.VerifyInvocation(raw)
	if err != nil || !validInvocationIdentity(claims) {
		return nil, errors.Join(ErrPlatformMCPInvocationInvalid, err)
	}
	return claims, nil
}

func (s *MCPTokenExchange) validateBinding(ctx context.Context, claims *platformmcp.InvocationClaims) error {
	binding, err := s.Bindings.ReadPlatformMCPBinding(
		ctx, claims.TenantID, claims.AgentID, claims.ServerID, claims.ToolName,
	)
	if err != nil {
		return fmt.Errorf("read platform MCP binding: %w", err)
	}
	if !validManagedBinding(binding) {
		return ErrPlatformMCPIdentityInvalid
	}
	if !binding.Bound {
		return ErrPlatformMCPBindingMissing
	}
	return nil
}

func (s *MCPTokenExchange) consumeInvocation(ctx context.Context, claims *platformmcp.InvocationClaims) error {
	consumed, err := s.Replay.ConsumeInvocationJTI(ctx, claims.TenantID, claims.ID, claims.ExpiresAt.Time)
	if err != nil {
		return fmt.Errorf("consume platform MCP invocation: %w", err)
	}
	if !consumed {
		return ErrPlatformMCPInvocationReplayed
	}
	return nil
}

func (s *MCPTokenExchange) resolveRole(
	ctx context.Context,
	claims *platformmcp.InvocationClaims,
) (string, error) {
	role, err := s.Roles.CurrentRole(ctx, claims.TenantID, claims.UserID)
	if err != nil {
		return "", fmt.Errorf("resolve current platform MCP role: %w", err)
	}
	return role, nil
}

func (s *MCPTokenExchange) resolveContract(toolName, role string) (platformmcp.ToolContract, error) {
	contract, ok := s.Contracts.Lookup(toolName)
	if !ok || contract.Name != toolName || contract.Method == "" || contract.Path == "" {
		return platformmcp.ToolContract{}, ErrPlatformMCPContractInvalid
	}
	if !roleAllows(role, contract.MinimumRole) {
		return platformmcp.ToolContract{}, ErrPlatformMCPRoleInsufficient
	}
	return contract, nil
}

func (s *MCPTokenExchange) validateApproval(
	ctx context.Context,
	claims *platformmcp.InvocationClaims,
	contract platformmcp.ToolContract,
	resourceID string,
) error {
	if !contract.RequiresApproval {
		return nil
	}
	if claims.ApprovalID == "" || s.Approvals == nil {
		return ErrPlatformMCPApprovalInvalid
	}
	if err := s.Approvals.ValidatePlatformMCPApproval(
		ctx, claims.TenantID, claims.ApprovalID, claims.ToolName, resourceID,
	); err != nil {
		if errors.Is(err, domain.ErrPlatformMCPApprovalStale) {
			return errors.Join(ErrPlatformMCPApprovalInvalid, err)
		}
		return fmt.Errorf("validate platform MCP approval: %w", err)
	}
	return nil
}

func (s *MCPTokenExchange) signDelegation(invocation verifiedMCPInvocation) (string, error) {
	claims := invocation.claims
	delegation := platformmcp.APIDelegationClaims{
		TenantID: claims.TenantID, AgentID: claims.AgentID, ServerID: claims.ServerID,
		ToolName: claims.ToolName, ExecutionID: claims.ExecutionID, HTTPMethod: invocation.contract.Method,
		PathTemplate: invocation.contract.Path, ResourceID: invocation.resourceID,
		ApprovalID: claims.ApprovalID, Role: invocation.role,
		RegisteredClaims: jwt.RegisteredClaims{Subject: claims.UserID, ID: uuid.Must(uuid.NewV7()).String()},
	}
	signed, err := s.Tokens.SignAPIDelegation(delegation, constants.PlatformMCPAPIDelegationTokenTTL)
	if err != nil {
		return "", fmt.Errorf("sign platform MCP API delegation: %w", err)
	}
	return signed, nil
}

func validInvocationIdentity(claims *platformmcp.InvocationClaims) bool {
	return claims != nil && claims.ExpiresAt != nil && claims.ID != "" &&
		claims.TenantID != "" && claims.UserID != "" && claims.ExecutionID != "" &&
		claims.AgentID == platformmcp.SystemAssistantID && claims.ServerID == platformmcp.SystemServerID
}

func validManagedBinding(binding iamport.PlatformMCPBinding) bool {
	return binding.AgentSystemKey == platformmcp.SystemAssistantKey &&
		binding.ServerSystemKey == platformmcp.SystemServerKey &&
		binding.ServerManagementMode == platformmcp.ManagementPlatform
}

func roleAllows(role, minimum string) bool {
	if minimum == "" {
		return roleRank(role) > 0
	}
	return roleRank(role) >= roleRank(minimum) && roleRank(minimum) > 0
}

func roleRank(role string) int {
	switch role {
	case "member":
		return 1
	case "admin":
		return 2
	case "owner":
		return 3
	default:
		return 0
	}
}
