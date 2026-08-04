package wiring

import (
	"context"
	"time"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	collabapp "github.com/byteBuilderX/stratum/internal/collab/application"
	collabpersist "github.com/byteBuilderX/stratum/internal/collab/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
)

// Collab groups the collaboration service and its task worker.
type Collab struct {
	Service *collabapp.CollaborationService
	Worker  interface {
		Run(context.Context, time.Duration)
	}
}

// collabAgentRunner executes a step through the agent service with a
// plan-derived identity: UserID "collab:"+planID scopes memory and sessions
// to the plan, preventing cross-plan contamination. Unknown members fail
// closed on tool authorization — collab steps run pure LLM tasks.
type collabAgentRunner struct{ agents *agentapp.AgentService }

func (r collabAgentRunner) RunAgentStep(ctx context.Context, tenantID, agentID string, input map[string]any) (map[string]any, string, error) {
	planID, _ := input["plan_id"].(string)
	query, _ := input["query"].(string)
	traceID := uuid.Must(uuid.NewV7()).String()
	tenantCtx := postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	result, _, err := r.agents.Execute(tenantCtx, agentID,
		agentapp.ExecRequest{Query: query, UserID: "collab:" + planID},
		agentapp.ExecMeta{TenantID: tenantID, TraceID: traceID})
	if err != nil {
		return nil, traceID, err
	}
	if result == nil {
		return map[string]any{}, traceID, nil
	}
	// The agent surface is textual; the collab step payload is a JSON map.
	return map[string]any{"output": result.Output, "trace_id": traceID}, traceID, nil
}

// buildCollab wires the collaboration service and worker. The worker needs
// the agent service as its step runner, so this step runs after agent.
func (c *Container) buildCollab(_ context.Context) error {
	db := c.dbOrNil()
	if db == nil || c.Agent == nil || c.Agent.Service == nil {
		return nil
	}
	collabStore := collabpersist.NewPgCollaborationRepo(db)
	stepStore := collabpersist.NewPgTaskStepRepo(db)
	sharedStore := collabpersist.NewPgSharedContextRepo(db)
	newID := func() string { return uuid.Must(uuid.NewV7()).String() }
	service := collabapp.NewCollaborationService(collabStore, stepStore, c.platformMetrics(), newID)
	runner := collabAgentRunner{agents: c.Agent.Service}
	c.Collab = &Collab{
		Service: service,
		Worker: collabapp.NewWorker(
			"collab-"+newID(), collabStore, stepStore, sharedStore, runner,
			collabapp.CollabTaskLease, c.platformMetrics()),
	}
	return nil
}
