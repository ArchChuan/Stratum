package application

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// newChangeAudit builds the audit event for a business write. before/after are
// safe projections (no credentials); nil means `{}`. A system actor in ctx
// (evaluation worker) is recorded with actor_type=system, source=optimization
// and overrides the caller-provided actor.
func newChangeAudit(ctx context.Context, kind, resourceID, op, actorID string, before, after any) (*auditdomain.ResourceChangeAuditEvent, error) {
	ev := &auditdomain.ResourceChangeAuditEvent{
		ResourceKind: kind, ResourceID: resourceID, Operation: op,
		ActorID: actorID, ActorType: auditdomain.ChangeActorUser,
	}
	if sysActor := reqctx.SystemActorFromContext(ctx); sysActor != "" {
		ev.ActorID = sysActor
		ev.ActorType = auditdomain.ChangeActorSystem
		ev.Source = auditdomain.ChangeSourceOptimization
	} else {
		source, proposalID := reqctx.ChangeSourceFromContext(ctx)
		ev.Source = source
		if ev.Source == "" {
			ev.Source = auditdomain.ChangeSourceAPI
		}
		ev.ProposalID = proposalID
	}
	var err error
	if before != nil {
		ev.Before, err = json.Marshal(before)
		if err != nil {
			return nil, fmt.Errorf("change audit: marshal before: %w", err)
		}
	}
	if after != nil {
		ev.After, err = json.Marshal(after)
		if err != nil {
			return nil, fmt.Errorf("change audit: marshal after: %w", err)
		}
	}
	return ev, nil
}

// mcpSafeProjection is the credential-free projection of an MCP server
// config. auth, env and headers are never included; URLs are scrubbed of
// embedded credentials.
func MCPSafeProjection(value *domain.ServerConfig) map[string]any {
	return MCPSafeProjectionWithEditors(value, nil)
}

// MCPSafeProjectionWithEditors adds the granted editor set to the safe
// projection when editors is non-nil; nil keeps the field absent so plain
// business writes do not mask editor changes in audit diffs.
func MCPSafeProjectionWithEditors(value *domain.ServerConfig, editors []string) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	proj := map[string]any{
		"id": value.ID, "name": value.Name, "version": value.Version, "transport": value.Transport,
		"command": value.Command, "args": value.Args, "url": MCPSafeURL(value.URL), "capabilities": value.Capabilities,
		"timeoutMs": value.Timeout.Milliseconds(), "retry": value.Retry,
	}
	if editors != nil {
		proj["editors"] = editors
	}
	return proj
}

// mcpSafeURL strips userinfo and credential-looking query parameters so URLs
// can be stored in audit projections.
func MCPSafeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
		for _, marker := range []string{"token", "apikey", "authorization", "password", "secret", "credential"} {
			if strings.Contains(normalized, marker) {
				query.Del(key)
				break
			}
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
