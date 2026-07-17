//go:build integration

package application_test

import (
	"context"
	"errors"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/workflow/application"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	workflowpersist "github.com/byteBuilderX/stratum/internal/workflow/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestStage1CConcurrentStartAndPublishAreAtomic(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Fatal("STRATUM_TEST_POSTGRES_URL is required; this integration test must not skip")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()))
	tenantID := "workflow_" + uuid.NewString()[:8]
	require.NoError(t, postgres.ProvisionTenantSchema(ctx, pool, tenantID))
	defer func() { _, _ = pool.Exec(ctx, `DROP SCHEMA IF EXISTS "tenant_`+tenantID+`" CASCADE`) }()
	tenantCtx := postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	store := workflowpersist.NewPgStore(pool)
	definitions := application.NewDefinitionService(store, store, uuid.NewString)
	definition, err := definitions.Create(tenantCtx, tenantID, application.CreateDefinitionCommand{Name: "Concurrent", Spec: domain.Spec{Nodes: []domain.Node{{ID: "one", Type: domain.NodeTypeAgent, AgentID: "a"}}}})
	require.NoError(t, err)

	type publishResult struct {
		version *domain.Version
		err     error
	}
	published := make(chan publishResult, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := definitions.Publish(tenantCtx, tenantID, definition.ID)
			published <- publishResult{v, e}
		}()
	}
	wg.Wait()
	close(published)
	var numbers []int64
	var versionID string
	for result := range published {
		require.NoError(t, result.err)
		numbers = append(numbers, result.version.Number)
		versionID = result.version.ID
	}
	sort.Slice(numbers, func(i, j int) bool { return numbers[i] < numbers[j] })
	require.Equal(t, []int64{1, 2, 3, 4}, numbers)

	runs := application.NewRunServiceWithRegistry(store, store, integrationRegistry{}, uuid.NewString)
	type startResult struct {
		run *domain.Run
		err error
	}
	started := make(chan startResult, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, _, e := runs.Start(tenantCtx, tenantID, application.StartRunCommand{VersionID: versionID, Input: map[string]any{"same": true}, IdempotencyKey: "same-key"})
			started <- startResult{run, e}
		}()
	}
	wg.Wait()
	close(started)
	runIDs := map[string]bool{}
	for result := range started {
		require.NoError(t, result.err)
		runIDs[result.run.ID] = true
	}
	require.Len(t, runIDs, 1)

	different := make(chan error, 2)
	for _, value := range []string{"a", "b"} {
		wg.Add(1)
		go func(v string) {
			defer wg.Done()
			_, _, e := runs.Start(tenantCtx, tenantID, application.StartRunCommand{VersionID: versionID, Input: map[string]any{"value": v}, IdempotencyKey: "different-key"})
			different <- e
		}(value)
	}
	wg.Wait()
	close(different)
	successes, conflicts := 0, 0
	for e := range different {
		if e == nil {
			successes++
		} else if errors.Is(e, domain.ErrIdempotencyConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected start error: %v", e)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
}

type integrationRegistry struct{}

func (integrationRegistry) Execute(_ context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
	return port.NodeExecutionResult{Output: request.Node.ID + "-output", TraceID: "trace-" + request.Node.ID}, nil
}

func TestStage1ARealPostgresRunsTwoAgentNodesInOrder(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Fatal("STRATUM_TEST_POSTGRES_URL is required; this integration test must not skip")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()))
	tenantID := "workflow_" + uuid.NewString()[:8]
	require.NoError(t, postgres.ProvisionTenantSchema(ctx, pool, tenantID))
	defer func() { _, _ = pool.Exec(ctx, `DROP SCHEMA IF EXISTS "tenant_`+tenantID+`" CASCADE`) }()
	tenantCtx := postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	store, agents := workflowpersist.NewPgStore(pool), &agentStub{}
	newID := uuid.NewString
	definitions := application.NewDefinitionService(store, store, newID)
	definition, err := definitions.Create(tenantCtx, tenantID, application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()})
	require.NoError(t, err)
	version, err := definitions.Publish(tenantCtx, tenantID, definition.ID)
	require.NoError(t, err)
	runs := application.NewRunService(store, store, agents, newID)
	run, _, err := runs.Start(tenantCtx, tenantID, application.StartRunCommand{VersionID: version.ID, Input: map[string]any{"query": "hello"}, IdempotencyKey: "e2e"})
	require.NoError(t, err)
	require.NoError(t, runs.Execute(tenantCtx, tenantID, run.ID))
	got, attempts, err := runs.Get(tenantCtx, tenantID, run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusCompleted, got.Status)
	require.Equal(t, []string{"agent-1:{\"query\":\"hello\"}", "agent-2:output-agent-1"}, agents.calls)
	require.Len(t, attempts, 2)
	require.Equal(t, "trace-agent-2", attempts[1].TraceID)
	_, _, err = runs.Start(tenantCtx, tenantID, application.StartRunCommand{VersionID: version.ID, Input: map[string]any{"query": "different"}, IdempotencyKey: "e2e"})
	require.ErrorIs(t, err, domain.ErrIdempotencyConflict)
}

func TestStage1BRealPostgresIndependentWorkerRunsDiamondAndPersistsEvents(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Fatal("STRATUM_TEST_POSTGRES_URL is required; this integration test must not skip")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()))
	tenantID := uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO tenants (id,name,slug,status) VALUES ($1,$2,$3,'active')`, tenantID, "Workflow DAG", "workflow-dag-"+tenantID[:8])
	require.NoError(t, err)
	require.NoError(t, postgres.ProvisionTenantSchema(ctx, pool, tenantID))
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	tenantCtx := postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	store, newID := workflowpersist.NewPgStore(pool), uuid.NewString
	spec := domain.Spec{Nodes: []domain.Node{{ID: "start", Type: domain.NodeTypeAgent, AgentID: "a"}, {ID: "left", Type: domain.NodeTypeAgent, AgentID: "b"}, {ID: "right", Type: domain.NodeTypeAgent, AgentID: "c"}, {ID: "join", Type: domain.NodeTypeAgent, AgentID: "d"}}, Edges: []domain.Edge{{From: "start", To: "left"}, {From: "start", To: "right"}, {From: "left", To: "join"}, {From: "right", To: "join"}}, MaxConcurrency: 2}
	definitions := application.NewDefinitionService(store, store, newID)
	definition, err := definitions.Create(tenantCtx, tenantID, application.CreateDefinitionCommand{Name: "Diamond", Spec: spec})
	require.NoError(t, err)
	version, err := definitions.Publish(tenantCtx, tenantID, definition.ID)
	require.NoError(t, err)
	runs := application.NewRunServiceWithRegistry(store, store, integrationRegistry{}, newID)
	run, _, err := runs.Start(tenantCtx, tenantID, application.StartRunCommand{VersionID: version.ID, Input: map[string]any{"query": "hello"}, IdempotencyKey: "independent-worker"})
	require.NoError(t, err)
	worker := application.NewWorker("integration-worker", store, runs, time.Minute)
	require.True(t, worker.RunOnce(ctx))
	got, attempts, err := runs.Get(tenantCtx, tenantID, run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusCompleted, got.Status)
	require.Len(t, attempts, 4)
	events, err := runs.Events(tenantCtx, tenantID, run.ID, 0, 100)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(events), 10)
	for index, event := range events {
		require.Equal(t, int64(index+1), event.SequenceNo)
	}
}

func TestStage1CPauseResumeAcrossWorkersUsesPostgresCheckpoint(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Fatal("STRATUM_TEST_POSTGRES_URL is required; this integration test must not skip")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	defer pool.Close()
	require.NoError(t, postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()))
	tenantID := uuid.NewString()
	_, err = pool.Exec(ctx, `INSERT INTO tenants (id,name,slug,status) VALUES ($1,$2,$3,'active')`, tenantID, "Workflow Resume", "workflow-resume-"+tenantID[:8])
	require.NoError(t, err)
	require.NoError(t, postgres.ProvisionTenantSchema(ctx, pool, tenantID))
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, tenantID) }()
	tenantCtx := postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	store, newID := workflowpersist.NewPgStore(pool), uuid.NewString
	definitions := application.NewDefinitionService(store, store, newID)
	definition, err := definitions.Create(tenantCtx, tenantID, application.CreateDefinitionCommand{Name: "Resume", Spec: domain.Spec{Nodes: []domain.Node{{ID: "one", Type: domain.NodeTypeAgent, AgentID: "a"}}}})
	require.NoError(t, err)
	version, err := definitions.Publish(tenantCtx, tenantID, definition.ID)
	require.NoError(t, err)
	runs := application.NewRunServiceWithRegistry(store, store, integrationRegistry{}, newID)
	controls := application.NewControlService(store, newID)
	run, _, err := runs.Start(tenantCtx, tenantID, application.StartRunCommand{VersionID: version.ID, Input: map[string]any{}, IdempotencyKey: "resume"})
	require.NoError(t, err)
	_, err = controls.Pause(tenantCtx, tenantID, run.ID, run.Generation, "admin", "maintenance")
	require.NoError(t, err)
	workerA := application.NewWorker("worker-a", store, runs, time.Minute)
	require.True(t, workerA.RunOnce(ctx))
	paused, _, err := runs.Get(tenantCtx, tenantID, run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusPaused, paused.Status)
	resumed, err := controls.Resume(tenantCtx, tenantID, run.ID, paused.Generation, "admin")
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusQueued, resumed.Status)
	workerB := application.NewWorker("worker-b", store, runs, time.Minute)
	require.True(t, workerB.RunOnce(ctx))
	completed, attempts, err := runs.Get(tenantCtx, tenantID, run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusCompleted, completed.Status)
	require.Len(t, attempts, 1)
}
