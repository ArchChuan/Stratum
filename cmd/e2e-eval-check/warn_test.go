package main

import (
	"reflect"
	"testing"
)

func TestHasStrongWarn(t *testing.T) {
	cases := []struct {
		name string
		warn []warning
		want bool
	}{
		{name: "empty", warn: nil, want: false},
		{name: "strong present", warn: []warning{{ID: warnRegression, Level: warnStrong, Message: "x"}}, want: true},
		{name: "only warn level", warn: []warning{{ID: warnConfigDrift, Level: warnWarn, Message: "x"}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasStrongWarn(tc.warn); got != tc.want {
				t.Fatalf("hasStrongWarn(%v) = %v, want %v", tc.warn, got, tc.want)
			}
		})
	}
}

func TestStrongWarnIDs(t *testing.T) {
	got := strongWarnIDs([]warning{
		{ID: "b", Level: warnStrong},
		{ID: "a", Level: warnStrong},
		{ID: "c", Level: warnWarn},
	})
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("strongWarnIDs = %v, want %v", got, want)
	}
}

func TestRegressionMetrics(t *testing.T) {
	cases := []struct {
		kind string
		want int
	}{
		{kind: "skill", want: 2},
		{kind: "agent", want: 2},
		{kind: "knowledge", want: 4},
		{kind: "mcp", want: 1},
		{kind: "unknown", want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			if got := len(regressionMetrics(tc.kind)); got != tc.want {
				t.Fatalf("regressionMetrics(%q) = %d metrics, want %d", tc.kind, got, tc.want)
			}
		})
	}
}

func TestCompareBaselineNilBase(t *testing.T) {
	warns, nc := compareBaseline("knowledge", fingerprint{Hash: "h"}, aggregate{}, nil, nil, "c", 0.1)
	if warns != nil || nc {
		t.Fatalf("nil baseline should yield no warnings and comparable, got warns=%v nc=%v", warns, nc)
	}
}

func TestCompareBaselineFingerprintDriftSuppressesRegressions(t *testing.T) {
	base := &baseline{
		Fingerprint: fingerprint{Hash: "old"},
		Aggregate:   aggregate{CaseCount: 1, PassRate: 0.9},
	}
	agg := aggregate{CaseCount: 1, PassRate: 0.7}
	warns, nc := compareBaseline("knowledge", fingerprint{Hash: "new"}, agg, base, nil, "c", 0.1)
	if !nc {
		t.Fatal("fingerprint drift must mark the run non-comparable")
	}
	if hasStrongWarn(warns) {
		t.Fatal("regression warnings must be suppressed when non-comparable")
	}
	if !hasWarnID(warns, warnFingerprintDrift) {
		t.Fatalf("expected fingerprint_drift warning, got %v", warns)
	}
}

func TestCompareBaselineProviderDrift(t *testing.T) {
	base := &baseline{
		Fingerprint: fingerprint{Hash: "h", ProviderHash: "p1"},
		Aggregate:   aggregate{CaseCount: 1, PassRate: 0.9},
	}
	agg := aggregate{CaseCount: 1, PassRate: 0.7}
	warns, nc := compareBaseline("knowledge", fingerprint{Hash: "h", ProviderHash: "p2"}, agg, base, nil, "c", 0.1)
	if !nc || !hasWarnID(warns, warnProviderDrift) {
		t.Fatalf("provider drift should be non-comparable with provider_drift warning, got nc=%v warns=%v", nc, warns)
	}
}

func TestCompareBaselineRegression(t *testing.T) {
	base := &baseline{
		Fingerprint: fingerprint{Hash: "h"},
		Aggregate:   aggregate{CaseCount: 1, PassRate: 0.9},
	}
	agg := aggregate{CaseCount: 1, PassRate: 0.7}
	warns, nc := compareBaseline("knowledge", fingerprint{Hash: "h"}, agg, base, nil, "c", 0.1)
	if nc {
		t.Fatal("same fingerprint must stay comparable")
	}
	if len(warns) != 1 || warns[0].ID != warnRegression || warns[0].Level != warnStrong {
		t.Fatalf("expected single strong regression warning, got %v", warns)
	}
}

func TestCompareBaselineAcceptedRegression(t *testing.T) {
	accepted := []acceptedRegression{{Metric: "pass_rate", Baseline: 0.9, Run: 0.7, Commit: "c1", Reason: "llm drift"}}
	base := &baseline{
		Fingerprint: fingerprint{Hash: "h"},
		Aggregate:   aggregate{CaseCount: 1, PassRate: 0.9},
	}
	agg := aggregate{CaseCount: 1, PassRate: 0.7}
	warns, nc := compareBaseline("knowledge", fingerprint{Hash: "h"}, agg, base, accepted, "c1", 0.1)
	if nc {
		t.Fatal("accepted regression must not mark the run non-comparable")
	}
	if len(warns) != 0 {
		t.Fatalf("explicitly accepted regression must not be re-reported, got %v", warns)
	}
}

func TestAcceptedCovers(t *testing.T) {
	acc := []acceptedRegression{{Metric: "m", Baseline: 1, Run: 0.8, Commit: "c", Reason: "r"}}
	cases := []struct {
		name   string
		metric string
		run    float64
		commit string
		want   bool
	}{
		{name: "exact run", metric: "m", run: 0.8, commit: "c", want: true},
		{name: "within tolerance", metric: "m", run: 0.8 - 0.00005, commit: "c", want: true},
		{name: "degraded beyond accepted", metric: "m", run: 0.7, commit: "c", want: false},
		{name: "different commit", metric: "m", run: 0.8, commit: "other", want: false},
		{name: "different metric", metric: "other", run: 0.8, commit: "c", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := acceptedCovers(acc, tc.metric, tc.run, tc.commit); got != tc.want {
				t.Fatalf("acceptedCovers(metric=%q run=%g commit=%q) = %v, want %v", tc.metric, tc.run, tc.commit, got, tc.want)
			}
		})
	}
}

func TestRecordBaselineGate(t *testing.T) {
	strong := []warning{{ID: warnRegression, Level: warnStrong, Message: "x"}}
	warnLevel := []warning{{ID: warnConfigDrift, Level: warnWarn, Message: "x"}}
	if err := recordBaselineGate(nil, false); err == nil {
		t.Fatal("recording without confirm must fail closed")
	}
	if err := recordBaselineGate(strong, true); err == nil {
		t.Fatal("recording with strong warnings must fail closed")
	}
	if err := recordBaselineGate(warnLevel, true); err != nil {
		t.Fatalf("warn-level only should be recordable, got %v", err)
	}
	if err := recordBaselineGate(nil, true); err != nil {
		t.Fatalf("clean run with confirm should record, got %v", err)
	}
}

func hasWarnID(warns []warning, id string) bool {
	for _, w := range warns {
		if w.ID == id {
			return true
		}
	}
	return false
}
