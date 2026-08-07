package reqctx

import "context"

type changeSourceKey struct{}

type changeSource struct {
	Source     string
	ProposalID string
}

// WithChangeSource marks ctx as originating from a proposal apply (or another
// audited origin). Source is e.g. "proposal_apply"; proposalID is empty for
// non-proposal sources. Change audits read these values so the recorded
// source matches where the write really came from.
func WithChangeSource(ctx context.Context, source, proposalID string) context.Context {
	return context.WithValue(ctx, changeSourceKey{}, changeSource{Source: source, ProposalID: proposalID})
}

// ChangeSourceFromContext returns the recorded change source and proposal ID
// (both empty when not set).
func ChangeSourceFromContext(ctx context.Context) (string, string) {
	cs, _ := ctx.Value(changeSourceKey{}).(changeSource)
	return cs.Source, cs.ProposalID
}

type systemActorKey struct{}

// WithSystemActor marks ctx as carrying a system actor (e.g. the evaluation
// worker) that bypasses ownership checks but is still audited with
// actor_type=system, source=optimization.
func WithSystemActor(ctx context.Context, actorID string) context.Context {
	return context.WithValue(ctx, systemActorKey{}, actorID)
}

// SystemActorFromContext returns the system actor ID, empty when not set.
func SystemActorFromContext(ctx context.Context) string {
	v, _ := ctx.Value(systemActorKey{}).(string)
	return v
}
