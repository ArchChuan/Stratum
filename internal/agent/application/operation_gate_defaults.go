package application

// Defaults for the T8 operation gate. Behaviour numbers shared across the
// agent context live in pkg/constants (OperationApprovalTTL); everything
// scoped to this package lives here.
const (
	// OperationReviewNoteMaxRunes caps reviewer notes on rejections.
	OperationReviewNoteMaxRunes = 500
	// OperationBudgetZeroLimit documents the zero-value budget semantics:
	// a MaxDailyCostUSD or MaxDailyExecutions of 0 disables that dimension
	// entirely (no daily cap), so budgets opt in per dimension.
	OperationBudgetZeroLimit = 0
)

// Gate decision reasons. These strings are part of the API contract: handlers
// surface them to clients (e.g. 202 status:"pending_approval") and the frontend
// keys off them, so they must not be reworded without a contract change.
const (
	GateReasonApprovedReplay             = "approved_replay"              // single-use replay consumed; allow
	GateReasonPolicyAllowed              = "policy_allowed"               // no approval required; allow
	GateReasonPendingApproval            = "pending_approval"             // proposal created for self-modify
	GateReasonDelegationRequiresApproval = "delegation_requires_approval" // proposal created for delegate
	GateReasonDelegationRequired         = "delegation_required"          // delegate must declare read_only/full
	GateReasonBudgetExceeded             = "budget_exceeded"              // daily budget cap hit; proposal created
	GateReasonDuplicatePending           = "duplicate_pending"            // open proposal already exists for fingerprint
	GateReasonInvalidRequest             = "invalid_request"              // request failed gate validation
	GateReasonGateError                  = "gate_error"                   // internal gate failure; fail closed
)
