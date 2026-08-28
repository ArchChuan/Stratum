package domain

import (
	"strings"
	"testing"
	"time"
)

func TestEvalObservationValidate(t *testing.T) {
	base := EvalObservation{
		ID: "obs-1", TraceID: "trace-1",
		Resource: ObservationResourceRef{Kind: "agent", ResourceID: "agent-1"},
		Param: ParamVersion{
			Resource: ResourceParamVersion{Ref: "r1", Version: "v3"},
			Source:   ParamSourceResource,
		},
		Signals: ObservationSignals{Judge: []JudgeSignal{
			{Dimension: "faithfulness", Score: 0.9, Confidence: 0.85},
		}},
		CostPerf:  CostPerf{LatencyMS: 1200, Tokens: 3200, CostUSD: 0.012},
		Verdict:   VerdictPass,
		CreatedAt: time.Now().UTC(),
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid observation rejected: %v", err)
	}

	cases := []struct {
		name    string
		mutate  func(*EvalObservation)
		wantSub string
	}{
		{name: "missing trace_id", mutate: func(o *EvalObservation) { o.TraceID = "" }, wantSub: "trace_id"},
		{name: "missing resource kind", mutate: func(o *EvalObservation) { o.Resource.Kind = "" }, wantSub: "resource kind"},
		{name: "missing resource id", mutate: func(o *EvalObservation) { o.Resource.ResourceID = "" }, wantSub: "resource id"},
		{name: "invalid verdict", mutate: func(o *EvalObservation) { o.Verdict = ObservationVerdict("bogus") }, wantSub: "verdict"},
		{name: "judge dimension empty", mutate: func(o *EvalObservation) { o.Signals.Judge[0].Dimension = "" }, wantSub: "dimension"},
		{name: "judge score out of range", mutate: func(o *EvalObservation) { o.Signals.Judge[0].Score = 1.5 }, wantSub: "score"},
		{name: "judge confidence out of range", mutate: func(o *EvalObservation) { o.Signals.Judge[0].Confidence = -0.1 }, wantSub: "confidence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := base
			obs.Signals.Judge = append([]JudgeSignal(nil), base.Signals.Judge...)
			tc.mutate(&obs)
			err := obs.Validate()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}
