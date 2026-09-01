package persistence

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

const experimentPolicyJSON = `{"stages":[5,20],"min_samples":100}`

// 12-column row as returned by getExperimentTx / Get.
func experimentRow(status string, recommendation domain.Decision, stateVersion int64) []any {
	return []any{
		"exp-1", "prompt", "r-1", "stable-1", "canary-1", "suite-1",
		status, 20, []byte(experimentPolicyJSON),
		stateVersion, recommendation, false, "",
	}
}

func newTestExperiment() domain.Experiment {
	return domain.Experiment{
		ID: "exp-1", ResourceKind: "prompt", ResourceID: "r-1",
		StableRevisionID: "stable-1", CanaryRevisionID: "canary-1", SuiteRevisionID: "suite-1",
		Status: domain.ExperimentRunning, Stage: 20, StateVersion: 4,
	}
}

func TestPgExperimentRepository_ValidatePrerequisites_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("CASE WHEN \\$2 = 'skill'").
		WithArgs("stable-1", "prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("optimization_candidates").
		WithArgs("canary-1", "prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("eval_suite_revisions").
		WithArgs("suite-1", "prompt").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("eval_runs").
		WithArgs("prompt", "r-1", "canary-1", "suite-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectCommit()

	err := repo.ValidatePrerequisites(context.Background(), "t1",
		domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "stable-1"},
		domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "canary-1"},
		"suite-1")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_ValidatePrerequisites_stableNotPublished(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("CASE WHEN \\$2 = 'skill'").
		WithArgs("stable-1", "prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	err := repo.ValidatePrerequisites(context.Background(), "t1",
		domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "stable-1"},
		domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "canary-1"},
		"suite-1")
	require.ErrorIs(t, err, domain.ErrExperimentStableNotPublished)
}

func TestPgExperimentRepository_ValidatePrerequisites_invalidCandidate(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("CASE WHEN \\$2 = 'skill'").
		WithArgs("stable-1", "prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("optimization_candidates").
		WithArgs("canary-1", "prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	err := repo.ValidatePrerequisites(context.Background(), "t1",
		domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "stable-1"},
		domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "canary-1"},
		"suite-1")
	require.ErrorIs(t, err, domain.ErrExperimentInvalidCandidate)
}

func TestPgExperimentRepository_ValidatePrerequisites_suiteNotPublished(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("CASE WHEN \\$2 = 'skill'").
		WithArgs("stable-1", "prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("optimization_candidates").
		WithArgs("canary-1", "prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("eval_suite_revisions").
		WithArgs("suite-1", "prompt").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	err := repo.ValidatePrerequisites(context.Background(), "t1",
		domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "stable-1"},
		domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "canary-1"},
		"suite-1")
	require.ErrorIs(t, err, domain.ErrExperimentSuiteNotPublished)
}

func TestPgExperimentRepository_ValidatePrerequisites_offlineRunRequired(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("CASE WHEN \\$2 = 'skill'").
		WithArgs("stable-1", "prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("optimization_candidates").
		WithArgs("canary-1", "prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("eval_suite_revisions").
		WithArgs("suite-1", "prompt").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("eval_runs").
		WithArgs("prompt", "r-1", "canary-1", "suite-1").
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	err := repo.ValidatePrerequisites(context.Background(), "t1",
		domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "stable-1"},
		domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "canary-1"},
		"suite-1")
	require.ErrorIs(t, err, domain.ErrExperimentOfflineRunRequired)
}

func TestPgExperimentRepository_ValidatePrerequisites_queryFails(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("CASE WHEN \\$2 = 'skill'").
		WithArgs("stable-1", "prompt", "r-1").
		WillReturnError(assertionErr())
	mock.ExpectRollback()

	err := repo.ValidatePrerequisites(context.Background(), "t1",
		domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "stable-1"},
		domain.ResourceRef{Kind: "prompt", ResourceID: "r-1", RevisionID: "canary-1"},
		"suite-1")
	require.ErrorContains(t, err, "validate stable revision")
}

func TestPgExperimentRepository_Create_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	experiment := newTestExperiment()
	experiment.Stage = 5
	policyJSON, err := json.Marshal(experiment.Policy)
	require.NoError(t, err)
	deployment := domain.Deployment{
		ResourceKind: "prompt", ResourceID: "r-1", StableRevisionID: "stable-1",
		CanaryRevisionID: "canary-1", CanaryPercent: 5, ExperimentID: "exp-1", PolicyVersion: 1,
	}

	expectTenantTx(mock)
	mock.ExpectExec("INSERT INTO evaluation_experiments").
		WithArgs("exp-1", "prompt", "r-1", "stable-1", "canary-1", "suite-1",
			"running", 5, string(policyJSON), int64(4), "", false, "").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO evaluation_deployments").
		WithArgs("prompt", "r-1", "stable-1", "canary-1", 5, "exp-1", 1).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Create(context.Background(), "t1", experiment, deployment, nil))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_Create_deploymentConflict(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	experiment := newTestExperiment()
	deployment := domain.Deployment{ResourceKind: "prompt", ResourceID: "r-1", ExperimentID: "exp-1"}
	policyJSON, err := json.Marshal(experiment.Policy)
	require.NoError(t, err)

	expectTenantTx(mock)
	mock.ExpectExec("INSERT INTO evaluation_experiments").
		WithArgs("exp-1", "prompt", "r-1", "stable-1", "canary-1", "suite-1",
			"running", 20, string(policyJSON), int64(4), "", false, "").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO evaluation_deployments").
		WithArgs("prompt", "r-1", "", "", 0, "exp-1", 0).
		WillReturnResult(pgxmock.NewResult("INSERT", 0))
	mock.ExpectRollback()

	err = repo.Create(context.Background(), "t1", experiment, deployment, nil)
	require.ErrorIs(t, err, domain.ErrExperimentDeploymentConflict)
}

func TestPgExperimentRepository_Create_insertFails(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	experiment := newTestExperiment()
	deployment := domain.Deployment{ResourceKind: "prompt", ResourceID: "r-1", ExperimentID: "exp-1"}
	policyJSON, err := json.Marshal(experiment.Policy)
	require.NoError(t, err)

	expectTenantTx(mock)
	mock.ExpectExec("INSERT INTO evaluation_experiments").
		WithArgs("exp-1", "prompt", "r-1", "stable-1", "canary-1", "suite-1",
			"running", 20, string(policyJSON), int64(4), "", false, "").
		WillReturnError(assertionErr())
	mock.ExpectRollback()

	err = repo.Create(context.Background(), "t1", experiment, deployment, nil)
	require.ErrorContains(t, err, "insert experiment")
}

func TestPgExperimentRepository_Get_found(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("running", domain.Decision("hold"), int64(1))...))
	mock.ExpectCommit()

	experiment, found, err := repo.Get(context.Background(), "t1", "exp-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, domain.ExperimentRunning, experiment.Status)
	require.Equal(t, domain.DecisionHold, experiment.Recommendation)
	require.Equal(t, 20, experiment.Stage)
	require.Equal(t, []int{5, 20}, experiment.Policy.Stages)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_Get_notFound(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("missing").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	_, found, err := repo.Get(context.Background(), "t1", "missing")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_Get_badPolicy(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow("exp-1", "prompt", "r-1", "stable-1", "canary-1", "suite-1",
			"running", 20, []byte(`{bad`), int64(1), domain.Decision("hold"), false, ""))
	mock.ExpectRollback()

	_, found, err := repo.Get(context.Background(), "t1", "exp-1")
	require.Error(t, err)
	require.True(t, found)
}

func TestPgExperimentRepository_SaveDecision_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	experiment := newTestExperiment()
	experiment.Recommendation = domain.DecisionAdvance
	metrics := domain.StageMetrics{Samples: 120}
	decision := domain.DecisionAdvance

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("running", domain.Decision("hold"), int64(3))...))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "key-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("UPDATE evaluation_experiments").
		WithArgs("exp-1", "running", 20, pgxmock.AnyArg(), "advance", false, int64(4), int64(3)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE evaluation_deployments SET canary_percent").
		WithArgs("prompt", "r-1", 20, "exp-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO experiment_decisions").
		WithArgs(pgxmock.AnyArg(), "exp-1", "recommend", "running", "running", "advance",
			pgxmock.AnyArg(), "automated stage evaluation", "key-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	stored, storedDecision, err := repo.SaveDecision(context.Background(), "t1", experiment, decision, metrics, "key-1", "fp-1")
	require.NoError(t, err)
	require.Equal(t, "exp-1", stored.ID)
	require.Equal(t, domain.DecisionAdvance, storedDecision)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_SaveDecision_idempotentHit(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	experiment := newTestExperiment()
	experiment.Recommendation = domain.DecisionAdvance
	priorJSON, err := json.Marshal(map[string]any{
		"fingerprint": "fp-1", "decision": domain.DecisionAdvance, "result": experiment,
	})
	require.NoError(t, err)

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("running", domain.Decision("hold"), int64(3))...))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "key-1").
		WillReturnRows(pgxmock.NewRows([]string{"metrics"}).AddRow([]byte(priorJSON)))
	mock.ExpectCommit()

	stored, storedDecision, err := repo.SaveDecision(context.Background(), "t1", experiment, domain.DecisionAdvance,
		domain.StageMetrics{}, "key-1", "fp-1")
	require.NoError(t, err)
	require.Equal(t, experiment, stored)
	require.Equal(t, domain.DecisionAdvance, storedDecision)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_SaveDecision_commandConflict(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	experiment := newTestExperiment()
	priorJSON, err := json.Marshal(map[string]any{
		"fingerprint": "other-fp", "decision": domain.DecisionHold, "result": experiment,
	})
	require.NoError(t, err)

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("running", domain.Decision("hold"), int64(3))...))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "key-1").
		WillReturnRows(pgxmock.NewRows([]string{"metrics"}).AddRow([]byte(priorJSON)))
	mock.ExpectRollback()

	_, _, err = repo.SaveDecision(context.Background(), "t1", experiment, domain.DecisionAdvance,
		domain.StageMetrics{}, "key-1", "fp-1")
	require.ErrorIs(t, err, domain.ErrExperimentCommandConflict)
}

func TestPgExperimentRepository_SaveDecision_badPriorSnapshot(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	experiment := newTestExperiment()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("running", domain.Decision("hold"), int64(3))...))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "key-1").
		WillReturnRows(pgxmock.NewRows([]string{"metrics"}).AddRow([]byte(`{bad`)))
	mock.ExpectRollback()

	_, _, err := repo.SaveDecision(context.Background(), "t1", experiment, domain.DecisionAdvance,
		domain.StageMetrics{}, "key-1", "fp-1")
	require.Error(t, err)
}

func TestPgExperimentRepository_SaveDecision_notAllowed(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	experiment := newTestExperiment()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("paused", domain.Decision("hold"), int64(3))...))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "key-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, _, err := repo.SaveDecision(context.Background(), "t1", experiment, domain.DecisionAdvance,
		domain.StageMetrics{}, "key-1", "fp-1")
	require.ErrorIs(t, err, domain.ErrExperimentCommandNotAllowed)
}

func TestPgExperimentRepository_SaveDecision_stateConflict(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	experiment := newTestExperiment()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("running", domain.Decision("hold"), int64(3))...))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "key-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("UPDATE evaluation_experiments").
		WithArgs("exp-1", "running", 20, pgxmock.AnyArg(), "advance", false, int64(4), int64(3)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	_, _, err := repo.SaveDecision(context.Background(), "t1", experiment, domain.DecisionAdvance,
		domain.StageMetrics{}, "key-1", "fp-1")
	require.ErrorIs(t, err, domain.ErrExperimentStateConflict)
}

func newTestCommand() domain.ExperimentCommand {
	return domain.ExperimentCommand{
		ActorType: domain.ActorTypeAdmin, ActorID: "admin-1", Reason: "reason-1",
		IdempotencyKey: "cmd-1", ExpectedStateVersion: 3,
	}
}

func TestPgExperimentRepository_ApplyCommand_promote_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	command := newTestCommand()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("running", domain.Decision("promote"), int64(3))...))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "cmd-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("UPDATE evaluation_experiments").
		WithArgs("exp-1", "completed", int64(4)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE resource_revisions SET status='published'").
		WithArgs("canary-1", "prompt", "r-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE optimization_candidates c SET status='promoted'").
		WithArgs("canary-1", "prompt", "r-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO resource_change_audits").
		WithArgs(pgxmock.AnyArg(), "t1", "evaluation", "exp-1", "promote", "admin-1", "user", "api", "",
			json.RawMessage(`{"resource_id":"r-1","resource_kind":"prompt","status":"running"}`),
			json.RawMessage(`{"resource_id":"r-1","resource_kind":"prompt","status":"running"}`)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("UPDATE evaluation_deployments\n\t\t\tSET stable_revision_id").
		WithArgs("exp-1", "canary-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO experiment_decisions").
		WithArgs(pgxmock.AnyArg(), "exp-1", "promote", string(command.ActorType), "admin-1",
			"running", "completed", pgxmock.AnyArg(), "reason-1", "cmd-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	updated, err := repo.ApplyCommand(context.Background(), "t1", "exp-1", domain.CommandPromote, command)
	require.NoError(t, err)
	require.Equal(t, domain.ExperimentCompleted, updated.Status)
	require.Equal(t, int64(4), updated.StateVersion)
	require.Equal(t, domain.DecisionHold, updated.Recommendation)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_ApplyCommand_pause_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	command := newTestCommand()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("running", domain.Decision("hold"), int64(3))...))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "cmd-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("UPDATE evaluation_experiments").
		WithArgs("exp-1", "paused", int64(4)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE evaluation_deployments SET canary_percent=0").
		WithArgs("exp-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO experiment_decisions").
		WithArgs(pgxmock.AnyArg(), "exp-1", "pause", string(command.ActorType), "admin-1",
			"running", "paused", pgxmock.AnyArg(), "reason-1", "cmd-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO resource_change_audits").
		WithArgs(pgxmock.AnyArg(), "t1", "evaluation", "exp-1", "pause", "admin-1", "user", "api", "",
			json.RawMessage(`{"resource_id":"r-1","resource_kind":"prompt","status":"running"}`),
			json.RawMessage(`{"resource_id":"r-1","resource_kind":"prompt","status":"paused"}`)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	updated, err := repo.ApplyCommand(context.Background(), "t1", "exp-1", domain.CommandPause, command)
	require.NoError(t, err)
	require.Equal(t, domain.ExperimentPaused, updated.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_ApplyCommand_rollback_success(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	command := newTestCommand()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("paused", domain.Decision("hold"), int64(3))...))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "cmd-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("UPDATE evaluation_experiments").
		WithArgs("exp-1", "rolled_back", int64(4)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE evaluation_deployments\n\t\t\tSET canary_revision_id=NULL").
		WithArgs("exp-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO experiment_decisions").
		WithArgs(pgxmock.AnyArg(), "exp-1", "rollback", string(command.ActorType), "admin-1",
			"paused", "rolled_back", pgxmock.AnyArg(), "reason-1", "cmd-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("INSERT INTO resource_change_audits").
		WithArgs(pgxmock.AnyArg(), "t1", "evaluation", "exp-1", "rollback", "admin-1", "user", "api", "",
			json.RawMessage(`{"resource_id":"r-1","resource_kind":"prompt","status":"paused"}`),
			json.RawMessage(`{"resource_id":"r-1","resource_kind":"prompt","status":"rolled_back"}`)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	updated, err := repo.ApplyCommand(context.Background(), "t1", "exp-1", domain.CommandRollback, command)
	require.NoError(t, err)
	require.Equal(t, domain.ExperimentRolledBack, updated.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_ApplyCommand_promoteSkill(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	command := newTestCommand()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow("exp-1", "skill", "skill-1", "stable-1", "canary-1", "suite-1",
			"running", 20, []byte(experimentPolicyJSON), int64(3), domain.Decision("promote"), false, ""))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "cmd-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectExec("UPDATE evaluation_experiments").
		WithArgs("exp-1", "completed", int64(4)).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE skill_revisions SET status='deprecated'").
		WithArgs("skill-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE skill_revisions\n\t\t\tSET status='published'").
		WithArgs("skill-1", "canary-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE skills SET status='published'").
		WithArgs("skill-1", "canary-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE optimization_candidates c SET status='promoted'").
		WithArgs("canary-1", "skill", "skill-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO resource_change_audits").
		WithArgs(pgxmock.AnyArg(), "t1", "evaluation", "exp-1", "promote", "admin-1", "user", "api", "",
			json.RawMessage(`{"resource_id":"skill-1","resource_kind":"skill","status":"running"}`),
			json.RawMessage(`{"resource_id":"skill-1","resource_kind":"skill","status":"running"}`)).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("UPDATE evaluation_deployments\n\t\t\tSET stable_revision_id").
		WithArgs("exp-1", "canary-1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("INSERT INTO experiment_decisions").
		WithArgs(pgxmock.AnyArg(), "exp-1", "promote", string(command.ActorType), "admin-1",
			"running", "completed", pgxmock.AnyArg(), "reason-1", "cmd-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	updated, err := repo.ApplyCommand(context.Background(), "t1", "exp-1", domain.CommandPromote, command)
	require.NoError(t, err)
	require.Equal(t, domain.ExperimentCompleted, updated.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_ApplyCommand_stateConflict(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	command := newTestCommand()
	command.ExpectedStateVersion = 99

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("running", domain.Decision("promote"), int64(3))...))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "cmd-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.ApplyCommand(context.Background(), "t1", "exp-1", domain.CommandPromote, command)
	require.ErrorIs(t, err, domain.ErrExperimentStateConflict)
}

func TestPgExperimentRepository_ApplyCommand_notAllowed(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	command := newTestCommand()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("paused", domain.Decision("hold"), int64(3))...))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "cmd-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.ApplyCommand(context.Background(), "t1", "exp-1", domain.CommandPromote, command)
	require.ErrorIs(t, err, domain.ErrExperimentCommandNotAllowed)
}

func TestPgExperimentRepository_ApplyCommand_promoteNotRecommended(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	command := newTestCommand()

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("running", domain.Decision("hold"), int64(3))...))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "cmd-1").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.ApplyCommand(context.Background(), "t1", "exp-1", domain.CommandPromote, command)
	require.ErrorIs(t, err, domain.ErrExperimentCommandNotAllowed)
}

func TestPgExperimentRepository_ApplyCommand_notFound(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("missing").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	_, err := repo.ApplyCommand(context.Background(), "t1", "missing", domain.CommandPause, newTestCommand())
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestPgExperimentRepository_ApplyCommand_idempotentHit(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}
	command := newTestCommand()
	priorExperiment := newTestExperiment()
	priorExperiment.Status = domain.ExperimentCompleted
	priorJSON, err := json.Marshal(map[string]any{
		"fingerprint": command.Fingerprint(domain.CommandPause), "result": priorExperiment,
	})
	require.NoError(t, err)

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT id, resource_kind").
		WithArgs("exp-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "resource_kind", "resource_id", "stable_revision_id", "canary_revision_id", "suite_revision_id",
			"status", "stage_percent", "policy", "state_version", "recommendation", "safety_stopped", "created_by",
		}).AddRow(experimentRow("running", domain.Decision("hold"), int64(3))...))
	mock.ExpectQuery("SELECT metrics FROM experiment_decisions").
		WithArgs("exp-1", "cmd-1").
		WillReturnRows(pgxmock.NewRows([]string{"metrics"}).AddRow([]byte(priorJSON)))
	mock.ExpectCommit()

	updated, err := repo.ApplyCommand(context.Background(), "t1", "exp-1", domain.CommandPause, command)
	require.NoError(t, err)
	require.Equal(t, domain.ExperimentCompleted, updated.Status)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_ResolveDeployment_found(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT resource_kind, resource_id, stable_revision_id").
		WithArgs("prompt", "r-1").
		WillReturnRows(pgxmock.NewRows([]string{
			"resource_kind", "resource_id", "stable_revision_id", "canary_revision_id",
			"canary_percent", "experiment_id", "policy_version",
		}).AddRow("prompt", "r-1", "stable-1", "canary-1", 5, "exp-1", 3))
	mock.ExpectCommit()

	deployment, found, err := repo.ResolveDeployment(context.Background(), "t1", "prompt", "r-1")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "stable-1", deployment.StableRevisionID)
	require.Equal(t, 5, deployment.CanaryPercent)
	require.Equal(t, "exp-1", deployment.ExperimentID)
	require.Equal(t, 3, deployment.PolicyVersion)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgExperimentRepository_ResolveDeployment_notFound(t *testing.T) {
	mock := newMockRepo(t)
	repo := &PgExperimentRepository{pool: mock}

	expectTenantTx(mock)
	mock.ExpectQuery("SELECT resource_kind, resource_id, stable_revision_id").
		WithArgs("prompt", "missing").
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectCommit()

	_, found, err := repo.ResolveDeployment(context.Background(), "t1", "prompt", "missing")
	require.NoError(t, err)
	require.False(t, found)
	require.NoError(t, mock.ExpectationsWereMet())
}
