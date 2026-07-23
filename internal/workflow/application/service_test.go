package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/workflow/application"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/stretchr/testify/require"
)

type memoryStore struct {
	mu          sync.Mutex
	definitions map[string]*domain.Definition
	versions    map[string]*domain.Version
	runs        map[string]*domain.Run
	attempts    map[string][]domain.NodeAttempt
}

func newMemoryStore() *memoryStore {
	return &memoryStore{definitions: map[string]*domain.Definition{}, versions: map[string]*domain.Version{}, runs: map[string]*domain.Run{}, attempts: map[string][]domain.NodeAttempt{}}
}

func (s *memoryStore) CreateDefinition(_ context.Context, _ string, definition *domain.Definition) error {
	s.definitions[definition.ID] = definition
	return nil
}
func (s *memoryStore) GetDefinition(_ context.Context, _, id string) (*domain.Definition, error) {
	row := s.definitions[id]
	if row == nil {
		return nil, domain.ErrNotFound
	}
	copy := *row
	return &copy, nil
}
func (s *memoryStore) UpdateDefinition(_ context.Context, _ string, definition *domain.Definition, expected int64) error {
	if s.definitions[definition.ID].Revision != expected {
		return domain.ErrRevisionConflict
	}
	s.definitions[definition.ID] = definition
	return nil
}
func (s *memoryStore) CreateVersion(_ context.Context, _ string, version *domain.Version) error {
	s.versions[version.ID] = version
	return nil
}
func (s *memoryStore) GetVersion(_ context.Context, _, id string) (*domain.Version, error) {
	row := s.versions[id]
	if row == nil {
		return nil, domain.ErrNotFound
	}
	copy := *row
	return &copy, nil
}
func (s *memoryStore) NextVersionNumber(context.Context, string, string) (int64, error) {
	return int64(len(s.versions) + 1), nil
}
func (s *memoryStore) FindRunByIdempotency(_ context.Context, _, key string) (*domain.Run, error) {
	for _, run := range s.runs {
		if run.IdempotencyKey == key {
			copy := *run
			return &copy, nil
		}
	}
	return nil, domain.ErrNotFound
}
func (s *memoryStore) CreateRun(_ context.Context, _ string, run *domain.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *run
	s.runs[run.ID] = &copy
	return nil
}
func (s *memoryStore) GetRun(_ context.Context, _, id string) (*domain.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.runs[id]
	if row == nil {
		return nil, domain.ErrNotFound
	}
	copy := *row
	return &copy, nil
}
func (s *memoryStore) UpdateRun(_ context.Context, _ string, run *domain.Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := *run
	s.runs[run.ID] = &copy
	return nil
}
func (s *memoryStore) SaveAttempt(_ context.Context, _ string, attempt domain.NodeAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := s.attempts[attempt.RunID]
	for i := range rows {
		if rows[i].NodeID == attempt.NodeID && rows[i].AttemptNo == attempt.AttemptNo {
			rows[i] = attempt
			s.attempts[attempt.RunID] = rows
			return nil
		}
	}
	s.attempts[attempt.RunID] = append(rows, attempt)
	return nil
}
func (s *memoryStore) ListAttempts(_ context.Context, _, runID string) ([]domain.NodeAttempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.NodeAttempt(nil), s.attempts[runID]...), nil
}

type ids struct{ n int }

func (i *ids) NewID() string { i.n++; return "id-" + string(rune('0'+i.n)) }

type agentStub struct {
	calls []string
	fail  string
}

func (s *agentStub) ExecuteAgent(_ context.Context, _, agentID, input string) (string, string, error) {
	s.calls = append(s.calls, agentID+":"+input)
	if agentID == s.fail {
		return "", "trace-fail", errors.New("agent failed")
	}
	return "output-" + agentID, "trace-" + agentID, nil
}

func workflowSpec() domain.Spec {
	return domain.Spec{Nodes: []domain.Node{{ID: "one", Type: domain.NodeTypeAgent, AgentID: "agent-1"}, {ID: "two", Type: domain.NodeTypeAgent, AgentID: "agent-2"}}, Edges: []domain.Edge{{From: "one", To: "two"}}}
}

func TestDefinitionServicePublishesVersion(t *testing.T) {
	store, idgen := newMemoryStore(), &ids{}
	svc := application.NewDefinitionService(store, store, idgen.NewID)
	def, err := svc.Create(context.Background(), "tenant-1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()})
	require.NoError(t, err)
	version, err := svc.Publish(context.Background(), "tenant-1", def.ID)
	require.NoError(t, err)
	require.Equal(t, def.ID, version.DefinitionID)
	require.Equal(t, int64(1), version.Number)
}

func TestRunServiceIdempotencyAndSequentialExecution(t *testing.T) {
	store, idgen, agents := newMemoryStore(), &ids{}, &agentStub{}
	defs := application.NewDefinitionService(store, store, idgen.NewID)
	def, err := defs.Create(context.Background(), "tenant-1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()})
	require.NoError(t, err)
	version, err := defs.Publish(context.Background(), "tenant-1", def.ID)
	require.NoError(t, err)
	runs := application.NewRunService(store, store, agents, idgen.NewID)

	run, created, err := runs.Start(context.Background(), "tenant-1", application.StartRunCommand{VersionID: version.ID, Input: map[string]any{"task": "hello"}, IdempotencyKey: "same-key"})
	require.NoError(t, err)
	require.True(t, created)
	same, created, err := runs.Start(context.Background(), "tenant-1", application.StartRunCommand{VersionID: version.ID, Input: map[string]any{"task": "hello"}, IdempotencyKey: "same-key"})
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, run.ID, same.ID)
	_, _, err = runs.Start(context.Background(), "tenant-1", application.StartRunCommand{VersionID: version.ID, Input: map[string]any{"task": "different"}, IdempotencyKey: "same-key"})
	require.ErrorIs(t, err, domain.ErrIdempotencyConflict)

	require.NoError(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusCompleted, got.Status)
	require.Equal(t, "output-agent-2", got.Output)
	require.Equal(t, []string{"agent-1:{\"task\":\"hello\"}", "agent-2:output-agent-1"}, agents.calls)
	require.Len(t, attempts, 2)
	require.Equal(t, "trace-agent-2", attempts[1].TraceID)
}

func TestRunServiceStopsAfterUpstreamFailure(t *testing.T) {
	store, idgen, agents := newMemoryStore(), &ids{}, &agentStub{fail: "agent-1"}
	defs := application.NewDefinitionService(store, store, idgen.NewID)
	def, _ := defs.Create(context.Background(), "tenant-1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()})
	version, _ := defs.Publish(context.Background(), "tenant-1", def.ID)
	runs := application.NewRunService(store, store, agents, idgen.NewID)
	run, _, err := runs.Start(context.Background(), "tenant-1", application.StartRunCommand{VersionID: version.ID, Input: map[string]any{"task": "hello"}, IdempotencyKey: "failure"})
	require.NoError(t, err)
	require.Error(t, runs.Execute(context.Background(), "tenant-1", run.ID))
	got, attempts, err := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusFailed, got.Status)
	require.Len(t, attempts, 1)
	require.Len(t, agents.calls, 1)
}

func TestRunServiceStartAsyncOnlyPersistsQueuedRun(t *testing.T) {
	store, idgen, agents := newMemoryStore(), &ids{}, &agentStub{}
	defs := application.NewDefinitionService(store, store, idgen.NewID)
	def, _ := defs.Create(context.Background(), "tenant-1", application.CreateDefinitionCommand{Name: "Research", Spec: workflowSpec()})
	version, _ := defs.Publish(context.Background(), "tenant-1", def.ID)
	runs := application.NewRunService(store, store, agents, idgen.NewID)

	run, created, err := runs.StartAsync(context.Background(), "tenant-1", application.StartRunCommand{VersionID: version.ID, Input: map[string]any{"task": "hello"}, IdempotencyKey: "async", CreatedBy: "user-a"})
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "user-a", run.CreatedBy)
	time.Sleep(30 * time.Millisecond)
	got, _, getErr := runs.Get(context.Background(), "tenant-1", run.ID)
	require.NoError(t, getErr)
	require.Equal(t, domain.RunStatusQueued, got.Status)
	require.Empty(t, agents.calls)
}
