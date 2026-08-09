package graph

// MergeReActWave folds one wave's node results into the running state. A
// single-node wave adopts that node's state outright, identical to the old
// serial engine. Parallel waves currently contain only plan slots, which
// append one outcome each and never rewrite shared history, so append-only
// fields take the tail delta beyond the pre-wave base, scalars sum their
// deltas, and the remaining fields last-write-wins. Callers must not combine
// nodes that rewrite Messages or observations in one wave.
func MergeReActWave(base ReActState, results []WaveResult[ReActState]) (ReActState, error) {
	if len(results) == 0 {
		return base, nil
	}
	if len(results) == 1 {
		return results[0].State, nil
	}
	merged := base
	for _, r := range results {
		merged.Steps += r.State.Steps - base.Steps
		merged.TotalTokens += r.State.TotalTokens - base.TotalTokens
		merged.TotalCostUSD += r.State.TotalCostUSD - base.TotalCostUSD
		merged.Messages = appendDelta(merged.Messages, base.Messages, r.State.Messages)
		merged.AllToolCalls = appendDelta(merged.AllToolCalls, base.AllToolCalls, r.State.AllToolCalls)
		merged.ToolObservations = appendDelta(merged.ToolObservations, base.ToolObservations, r.State.ToolObservations)
		merged.TraceEvents = appendDelta(merged.TraceEvents, base.TraceEvents, r.State.TraceEvents)
		merged.AssistantToolArtifacts = appendDelta(merged.AssistantToolArtifacts, base.AssistantToolArtifacts, r.State.AssistantToolArtifacts)
		merged.ModelRoutedVia = appendDelta(merged.ModelRoutedVia, base.ModelRoutedVia, r.State.ModelRoutedVia)
		merged.StepResults = appendDelta(merged.StepResults, base.StepResults, r.State.StepResults)
		merged.PlanWaveOutcomes = appendDelta(merged.PlanWaveOutcomes, base.PlanWaveOutcomes, r.State.PlanWaveOutcomes)
	}
	last := results[len(results)-1].State
	merged.Output = last.Output
	merged.ModelResolved = last.ModelResolved
	merged.LastEstimatedTokens = last.LastEstimatedTokens
	merged.TokenCorrection = last.TokenCorrection
	merged.ActivePlan = last.ActivePlan
	merged.PlanCheckpointIdentity = last.PlanCheckpointIdentity
	merged.PlanWavePending = last.PlanWavePending
	merged.PlanContinueCallID = last.PlanContinueCallID
	return merged, nil
}

// appendDelta appends the tail of candidate beyond orig to merged; a node
// that did not extend orig contributes nothing. Parallel waves must not
// rewrite shared history (see MergeReActWave).
func appendDelta[T any](merged, orig, candidate []T) []T {
	if len(candidate) <= len(orig) {
		return merged
	}
	return append(merged, candidate[len(orig):]...)
}
