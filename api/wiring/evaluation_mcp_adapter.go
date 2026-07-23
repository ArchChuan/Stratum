package wiring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	evalport "github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	pkgobjectstore "github.com/byteBuilderX/stratum/pkg/storage/objectstore"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

const (
	mcpMinimumEvaluationTimeout = 100 * time.Millisecond
	mcpMaximumEvaluationTimeout = 2 * time.Minute
	mcpMaximumEvaluationRetries = 5
	mcpRuntimeCleanupTimeout    = 5 * time.Second
)

type mcpRevisionService interface {
	Get(context.Context, string, evaldomain.ResourceRef) (evaldomain.ResourceRevision, []byte, bool, error)
	Create(context.Context, string, evalport.CreateRevisionInput) (evaldomain.ResourceRevision, bool, error)
	Publish(context.Context, string, evaldomain.ResourceRef) (evaldomain.ResourceRevision, error)
}

type mcpRevisionRuntime interface {
	GetServerConfig(context.Context, string) (*mcpdomain.ServerConfig, error)
	ListTools(context.Context, string) ([]*mcpdomain.Tool, error)
	ListToolsWithConfig(context.Context, *mcpdomain.ServerConfig) ([]*mcpdomain.Tool, error)
	CallToolWithConfig(context.Context, *mcpdomain.ServerConfig, string, any) (any, error)
}

type mcpRevisionSnapshot struct {
	ServerID       string                   `json:"server_id"`
	Name           string                   `json:"name"`
	RuntimeRef     pkgobjectstore.Reference `json:"runtime_ref"`
	EnabledTools   []string                 `json:"enabled_tools"`
	TimeoutMS      int64                    `json:"timeout_ms"`
	MaxRetries     int                      `json:"max_retries"`
	ToolSchemaHash string                   `json:"tool_schema_hash"`
}

type mcpEvaluationAdapter struct {
	runtime      mcpRevisionRuntime
	revisions    mcpRevisionService
	runtimeStore pkgobjectstore.Store
	actorID      string
}

func (a mcpEvaluationAdapter) CreatePublishedBaseline(
	ctx context.Context, tenantID, serverID string,
) (evaldomain.ResourceRef, error) {
	ctx, err := evaluationMCPContext(ctx, tenantID)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	if strings.TrimSpace(serverID) == "" || a.runtime == nil || a.revisions == nil || a.runtimeStore == nil {
		return evaldomain.ResourceRef{}, errors.New("evaluation MCP adapter: baseline dependencies required")
	}
	config, err := a.runtime.GetServerConfig(ctx, serverID)
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation MCP adapter: load server: %w", err)
	}
	tools, err := a.runtime.ListToolsWithConfig(ctx, config)
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation MCP adapter: discover tools: %w", err)
	}
	fingerprintPayload, contentFingerprint, err := mcpBaselineFingerprint(config, tools)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	runtimeRef, err := a.runtimeStore.Put(ctx, pkgobjectstore.Payload{
		TenantID: tenantID, Namespace: "evaluation-mcp-runtime", ID: serverID,
		Value: mcpRuntimeConfigEnvelope{TenantID: tenantID, Config: config},
	})
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation MCP adapter: store runtime config: %w", err)
	}
	snapshot, err := newMCPRevisionSnapshot(config, tools, runtimeRef)
	if err != nil {
		return evaldomain.ResourceRef{}, errors.Join(err, a.deleteRuntimeConfig(runtimeRef))
	}
	actorID := strings.TrimSpace(a.actorID)
	if actorID == "" {
		actorID = "evaluation-worker"
	}
	created, wasCreated, err := a.revisions.Create(ctx, tenantID, evalport.CreateRevisionInput{
		ResourceKind: evaldomain.ResourceKindMCP, ResourceID: serverID, CreatedBy: actorID,
		IdempotencyKey:     "mcp-baseline-" + stableTenantFingerprint(tenantID, contentFingerprint),
		FingerprintPayload: fingerprintPayload, Source: evaldomain.RevisionSourceManual,
		Payload: snapshot, SafeSummary: map[string]any{"resource_name": snapshot.Name},
	})
	if err != nil {
		return evaldomain.ResourceRef{}, errors.Join(
			fmt.Errorf("evaluation MCP adapter: create baseline: %w", err), a.deleteRuntimeConfig(runtimeRef),
		)
	}
	if !wasCreated {
		if err := a.deleteRuntimeConfig(runtimeRef); err != nil {
			return evaldomain.ResourceRef{}, err
		}
	}
	ref := evaldomain.ResourceRef{Kind: evaldomain.ResourceKindMCP, ResourceID: serverID, RevisionID: created.ID}
	if _, err := a.revisions.Publish(ctx, tenantID, ref); err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation MCP adapter: publish baseline: %w", err)
	}
	return ref, nil
}

func (a mcpEvaluationAdapter) LoadOptimizableSnapshot(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (map[string]any, error) {
	_, snapshot, err := a.loadPublished(ctx, tenantID, ref)
	if err != nil {
		return nil, err
	}
	return map[string]any{"enabled_tools": snapshot.EnabledTools, "timeout_ms": snapshot.TimeoutMS,
		"max_retries": snapshot.MaxRetries}, nil
}

func (a mcpEvaluationAdapter) CreateCandidate(
	ctx context.Context, tenantID string, baseline evaldomain.ResourceRef, patch evaldomain.CandidatePatch,
) (evaldomain.ResourceRef, error) {
	parent, snapshot, err := a.loadPublished(ctx, tenantID, baseline)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	changedFields, err := applyMCPCandidatePatch(&snapshot, patch)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	contentHash, err := canonicalHash(snapshot)
	if err != nil {
		return evaldomain.ResourceRef{}, err
	}
	actorID := strings.TrimSpace(a.actorID)
	if actorID == "" {
		actorID = "evaluation-worker"
	}
	changeTypes := make([]string, len(changedFields))
	for i := range changeTypes {
		changeTypes[i] = "modified"
	}
	stored, _, err := a.revisions.Create(ctx, tenantID, evalport.CreateRevisionInput{
		ResourceKind: evaldomain.ResourceKindMCP, ResourceID: baseline.ResourceID,
		ParentRevisionID: parent.ID, CreatedBy: actorID,
		IdempotencyKey: mcpCandidateIdempotencyKey(tenantID, baseline, contentHash),
		Source:         evaldomain.RevisionSourceOptimization, Payload: snapshot,
		SafeSummary: map[string]any{"resource_name": snapshot.Name, "changed_fields": changedFields,
			"change_types": changeTypes},
	})
	if err != nil {
		return evaldomain.ResourceRef{}, fmt.Errorf("evaluation MCP adapter: create candidate: %w", err)
	}
	return evaldomain.ResourceRef{Kind: evaldomain.ResourceKindMCP, ResourceID: baseline.ResourceID,
		RevisionID: stored.ID}, nil
}

func (a mcpEvaluationAdapter) ResolveRevision(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (evaldomain.ResourceRevision, error) {
	revision, _, err := a.loadPublished(ctx, tenantID, ref)
	return revision, err
}

func (a mcpEvaluationAdapter) SafeSummary(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (map[string]any, error) {
	revision, err := a.ResolveRevision(ctx, tenantID, ref)
	if err != nil {
		return nil, err
	}
	return revision.SafeSummary, nil
}

func (a mcpEvaluationAdapter) ExecuteRevision(
	ctx context.Context, tenantID, _ string, ref evaldomain.ResourceRef, testCase evaldomain.EvalCase,
) (evalport.ExecutionResult, error) {
	if a.runtime == nil {
		return evalport.ExecutionResult{}, errors.New("evaluation MCP adapter: runtime unavailable")
	}
	ctx, err := evaluationMCPContext(ctx, tenantID)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	_, snapshot, err := a.loadPublished(ctx, tenantID, ref)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	config, err := a.loadRuntimeConfig(ctx, tenantID, snapshot)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	tools, err := a.runtime.ListToolsWithConfig(ctx, config)
	if err != nil {
		return evalport.ExecutionResult{}, errors.New("evaluation MCP adapter: tool discovery failed")
	}
	discoveredHash, err := mcpToolSchemaHash(tools)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	contractCase, err := parseMCPContractCase(testCase)
	if err != nil {
		return evalport.ExecutionResult{}, err
	}
	result, err := mcpapp.EvaluateContract(ctx, mcpapp.ContractSnapshot{
		EnabledTools: snapshot.EnabledTools, Timeout: time.Duration(snapshot.TimeoutMS) * time.Millisecond,
		MaxRetries: snapshot.MaxRetries, ExpectedSchemaHash: snapshot.ToolSchemaHash,
		DiscoveredSchemaHash: discoveredHash,
	}, contractCase, func(callCtx context.Context, tool string, arguments map[string]any) (any, error) {
		return a.runtime.CallToolWithConfig(callCtx, config, tool, arguments)
	})
	return evalport.ExecutionResult{Output: result.Output, DurationMs: result.DurationMs}, err
}

func (a mcpEvaluationAdapter) loadPublished(
	ctx context.Context, tenantID string, ref evaldomain.ResourceRef,
) (evaldomain.ResourceRevision, mcpRevisionSnapshot, error) {
	ctx, err := evaluationMCPContext(ctx, tenantID)
	if err != nil {
		return evaldomain.ResourceRevision{}, mcpRevisionSnapshot{}, err
	}
	if a.revisions == nil {
		return evaldomain.ResourceRevision{}, mcpRevisionSnapshot{}, errors.New("evaluation MCP adapter: revisions unavailable")
	}
	if ref.Kind != evaldomain.ResourceKindMCP {
		return evaldomain.ResourceRevision{}, mcpRevisionSnapshot{},
			fmt.Errorf("evaluation MCP adapter: unsupported resource kind %q", ref.Kind)
	}
	if err := ref.Validate(); err != nil {
		return evaldomain.ResourceRevision{}, mcpRevisionSnapshot{}, err
	}
	revision, payload, found, err := a.revisions.Get(ctx, tenantID, ref)
	if err != nil {
		return evaldomain.ResourceRevision{}, mcpRevisionSnapshot{}, err
	}
	if !found {
		return evaldomain.ResourceRevision{}, mcpRevisionSnapshot{}, evalport.ErrCenterResourceNotFound
	}
	if revision.Status != evaldomain.RevisionStatusPublished {
		return evaldomain.ResourceRevision{}, mcpRevisionSnapshot{}, evaldomain.ErrRevisionNotPublished
	}
	var snapshot mcpRevisionSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return evaldomain.ResourceRevision{}, mcpRevisionSnapshot{},
			fmt.Errorf("evaluation MCP adapter: decode revision: %w", err)
	}
	if snapshot.ServerID != ref.ResourceID || strings.TrimSpace(snapshot.RuntimeRef.URI) == "" ||
		strings.TrimSpace(snapshot.RuntimeRef.SHA256) == "" {
		return evaldomain.ResourceRevision{}, mcpRevisionSnapshot{}, evalport.ErrCenterResourceNotFound
	}
	if err := validateMCPRuntimeBounds(snapshot.TimeoutMS, snapshot.MaxRetries); err != nil {
		return evaldomain.ResourceRevision{}, mcpRevisionSnapshot{}, err
	}
	return revision, snapshot, nil
}

type mcpRuntimeConfigEnvelope struct {
	TenantID string                  `json:"tenant_id"`
	Config   *mcpdomain.ServerConfig `json:"config"`
}

func (a mcpEvaluationAdapter) loadRuntimeConfig(
	ctx context.Context, tenantID string, snapshot mcpRevisionSnapshot,
) (*mcpdomain.ServerConfig, error) {
	if a.runtimeStore == nil {
		return nil, errors.New("evaluation MCP adapter: runtime store unavailable")
	}
	payload, err := a.runtimeStore.Get(ctx, snapshot.RuntimeRef)
	if err != nil {
		return nil, errors.New("evaluation MCP adapter: runtime config unavailable")
	}
	var envelope mcpRuntimeConfigEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, errors.New("evaluation MCP adapter: runtime config invalid")
	}
	if envelope.TenantID != tenantID || envelope.Config == nil || envelope.Config.ID != snapshot.ServerID {
		return nil, evalport.ErrCenterResourceNotFound
	}
	return envelope.Config, nil
}

func (a mcpEvaluationAdapter) deleteRuntimeConfig(ref pkgobjectstore.Reference) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), mcpRuntimeCleanupTimeout)
	defer cancel()
	if err := a.runtimeStore.Delete(cleanupCtx, ref); err != nil {
		return fmt.Errorf("evaluation MCP adapter: cleanup runtime config: %w", err)
	}
	return nil
}

func newMCPRevisionSnapshot(
	config *mcpdomain.ServerConfig, tools []*mcpdomain.Tool, runtimeRef pkgobjectstore.Reference,
) (mcpRevisionSnapshot, error) {
	if config == nil || strings.TrimSpace(config.ID) == "" || strings.TrimSpace(config.Name) == "" {
		return mcpRevisionSnapshot{}, errors.New("evaluation MCP adapter: invalid server config")
	}
	timeoutMS := config.Timeout.Milliseconds()
	maxRetries := 0
	if config.Retry != nil && config.Retry.Enabled {
		maxRetries = config.Retry.MaxRetries
	}
	if err := validateMCPRuntimeBounds(timeoutMS, maxRetries); err != nil {
		return mcpRevisionSnapshot{}, err
	}
	schemaHash, err := mcpToolSchemaHash(tools)
	if err != nil {
		return mcpRevisionSnapshot{}, err
	}
	enabled := make([]string, 0, len(tools))
	for _, tool := range tools {
		if tool == nil || strings.TrimSpace(tool.Name) == "" {
			return mcpRevisionSnapshot{}, errors.New("evaluation MCP adapter: discovered tool name required")
		}
		enabled = append(enabled, tool.Name)
	}
	sort.Strings(enabled)
	return mcpRevisionSnapshot{ServerID: config.ID, Name: config.Name,
		RuntimeRef: runtimeRef, EnabledTools: enabled,
		TimeoutMS: timeoutMS, MaxRetries: maxRetries, ToolSchemaHash: schemaHash}, nil
}

func mcpToolSchemaHash(tools []*mcpdomain.Tool) (string, error) {
	canonical := make([]mcpdomain.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool != nil {
			canonical = append(canonical, mcpdomain.Tool{
				Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema,
			})
		}
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Name < canonical[j].Name })
	return canonicalHash(canonical)
}

func canonicalHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("evaluation MCP adapter: canonical encoding: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

type mcpBaselineFingerprintPayload struct {
	Config         *mcpdomain.ServerConfig `json:"config"`
	ToolSchemaHash string                  `json:"tool_schema_hash"`
}

func mcpBaselineFingerprint(
	config *mcpdomain.ServerConfig, tools []*mcpdomain.Tool,
) (mcpBaselineFingerprintPayload, string, error) {
	if config == nil {
		return mcpBaselineFingerprintPayload{}, "", errors.New("evaluation MCP adapter: runtime config required")
	}
	toolHash, err := mcpToolSchemaHash(tools)
	if err != nil {
		return mcpBaselineFingerprintPayload{}, "", err
	}
	payload := mcpBaselineFingerprintPayload{Config: config, ToolSchemaHash: toolHash}
	hash, err := canonicalHash(payload)
	return payload, hash, err
}

func stableTenantFingerprint(tenantID, contentFingerprint string) string {
	hash := sha256.Sum256([]byte(tenantID + "\x00" + contentFingerprint))
	return hex.EncodeToString(hash[:])
}

func applyMCPCandidatePatch(snapshot *mcpRevisionSnapshot, patch evaldomain.CandidatePatch) ([]string, error) {
	if len(patch.PromptPatch) != 0 {
		return nil, errors.New("evaluation MCP adapter: prompt is not optimizable")
	}
	changed := make([]string, 0, len(patch.ParameterPatch))
	for key, value := range patch.ParameterPatch {
		switch key {
		case "enabled_tools":
			enabled, err := parseExistingMCPTools(value, snapshot.EnabledTools)
			if err != nil {
				return nil, err
			}
			snapshot.EnabledTools = enabled
		case "timeout_ms":
			parsed, ok := integer(value)
			if !ok || validateMCPRuntimeBounds(int64(parsed), snapshot.MaxRetries) != nil {
				return nil, errors.New("evaluation MCP adapter: timeout_ms out of bounds")
			}
			snapshot.TimeoutMS = int64(parsed)
		case "max_retries":
			parsed, ok := integer(value)
			if !ok || validateMCPRuntimeBounds(snapshot.TimeoutMS, parsed) != nil {
				return nil, errors.New("evaluation MCP adapter: max_retries out of bounds")
			}
			snapshot.MaxRetries = parsed
		default:
			return nil, fmt.Errorf("evaluation MCP adapter: field is not optimizable: %s", key)
		}
		changed = append(changed, key)
	}
	if len(changed) == 0 {
		return nil, errors.New("evaluation MCP adapter: candidate patch required")
	}
	sort.Strings(changed)
	return changed, nil
}

func parseExistingMCPTools(value any, baseline []string) ([]string, error) {
	var requested []string
	switch values := value.(type) {
	case []string:
		requested = append(requested, values...)
	case []any:
		for _, item := range values {
			name, ok := item.(string)
			if !ok {
				return nil, errors.New("evaluation MCP adapter: enabled_tools must contain strings")
			}
			requested = append(requested, name)
		}
	default:
		return nil, errors.New("evaluation MCP adapter: enabled_tools must be an array")
	}
	allowed := make(map[string]struct{}, len(baseline))
	for _, name := range baseline {
		allowed[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(requested))
	for _, name := range requested {
		if _, ok := allowed[name]; !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("evaluation MCP adapter: tool is not in baseline: %s", name)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("evaluation MCP adapter: duplicate tool: %s", name)
		}
		seen[name] = struct{}{}
	}
	sort.Strings(requested)
	return requested, nil
}

func validateMCPRuntimeBounds(timeoutMS int64, maxRetries int) error {
	timeout := time.Duration(timeoutMS) * time.Millisecond
	if timeout < mcpMinimumEvaluationTimeout || timeout > mcpMaximumEvaluationTimeout {
		return errors.New("evaluation MCP adapter: timeout out of bounds")
	}
	if maxRetries < 0 || maxRetries > mcpMaximumEvaluationRetries {
		return errors.New("evaluation MCP adapter: retries out of bounds")
	}
	return nil
}

func parseMCPContractCase(testCase evaldomain.EvalCase) (mcpapp.ContractCase, error) {
	input, ok := testCase.Input.(map[string]any)
	if !ok {
		return mcpapp.ContractCase{}, errors.New("evaluation MCP adapter: case input must be an object")
	}
	tool, ok := input["tool"].(string)
	if !ok || strings.TrimSpace(tool) == "" {
		return mcpapp.ContractCase{}, errors.New("evaluation MCP adapter: case tool required")
	}
	arguments, ok := input["arguments"].(map[string]any)
	if !ok {
		return mcpapp.ContractCase{}, errors.New("evaluation MCP adapter: case arguments must be an object")
	}
	return mcpapp.ContractCase{ToolName: strings.TrimSpace(tool), Arguments: arguments}, nil
}

func evaluationMCPContext(ctx context.Context, tenantID string) (context.Context, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, errors.New("evaluation MCP adapter: tenant ID required")
	}
	return postgres.WithTenant(ctx, &postgres.TenantContext{
		TenantID: tenantID, UserID: "evaluation-worker", Role: postgres.RoleTenantAdmin,
	}), nil
}

func mcpCandidateIdempotencyKey(tenantID string, baseline evaldomain.ResourceRef, contentHash string) string {
	hash := sha256.Sum256([]byte(tenantID + "\x00" + baseline.ResourceID + "\x00" + baseline.RevisionID + "\x00" + contentHash))
	return "mcp-candidate-" + hex.EncodeToString(hash[:])
}

var _ evalport.ResourceAdapter = mcpEvaluationAdapter{}
var _ evalport.CandidateCreator = mcpEvaluationAdapter{}
