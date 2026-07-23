package report

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/observe"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/process"
)

func TestAccumulatorStreamsEventsAndRejectsSnapshotOverflow(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	acc, err := NewAccumulator(start, start.Add(7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10000; i++ {
		if err := acc.AddEvent(tool(start.Add(time.Hour), "codex", "svc", "search", "same-session",
			observe.OutcomeSuccess, true, 1, 1, 1)); err != nil {
			t.Fatal(err)
		}
	}
	if got := acc.Report().Tools[0].Calls; got != 10000 {
		t.Fatalf("calls = %d", got)
	}
	key := toolKey{client: "codex", service: "svc", tool: "search"}
	if got := len(acc.tools[key].durations); got != 1 {
		t.Fatalf("stored duration buckets = %d, want 1 independent of event count", got)
	}
	bad := process.Snapshot{Version: 1, Mode: "observe", CapturedAt: start.Add(time.Hour), Processes: []process.Process{
		{Identity: process.Identity{PID: 1, StartTicks: 1}, Service: "svc", PSSBytes: math.MaxUint64},
		{Identity: process.Identity{PID: 2, StartTicks: 2}, Service: "svc", PSSBytes: 1},
	}}
	if err := acc.AddSnapshot(bad); err == nil {
		t.Fatal("AddSnapshot accepted PSS overflow")
	}
}

func TestAccumulatorRejectsDuplicateSnapshotIdentitiesAndServices(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for _, snapshot := range []process.Snapshot{
		{Version: 1, Mode: "observe", CapturedAt: start, Processes: []process.Process{
			{Identity: process.Identity{PID: 1, StartTicks: 1}, Service: "svc"},
			{Identity: process.Identity{PID: 1, StartTicks: 1}, Service: "svc"},
		}},
		{Version: 1, Mode: "observe", CapturedAt: start, Services: []process.ServiceSummary{
			{Service: "svc"}, {Service: "svc"},
		}},
		{Version: 1, Mode: "observe", CapturedAt: start.Add(-24 * time.Hour), Services: []process.ServiceSummary{
			{Service: ""},
		}},
	} {
		acc, _ := NewAccumulator(start, start.Add(time.Hour))
		if err := acc.AddSnapshot(snapshot); err == nil {
			t.Fatal("AddSnapshot accepted duplicate nested records")
		}
	}
}

func TestAggregateUsageAndResources(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	events := []observe.Event{
		tool(start.Add(time.Hour), "vscode", "beta", "z", "s1", observe.OutcomeError, false, 10, 100, 1),
		tool(start.Add(24*time.Hour), "codex", "alpha", "search", "s1", observe.OutcomeSuccess, true, 20, 200, 2),
		tool(start.Add(48*time.Hour), "codex", "alpha", "search", "s2", observe.OutcomeCancelled, false, 30, 300, 3),
		tool(start.Add(72*time.Hour), "codex", "alpha", "search", "s1", observe.OutcomeTimeout, false, 40, 400, 2),
		tool(start.Add(96*time.Hour), "codex", "alpha", "search", "s3", observe.OutcomeSuccess, true, 50, 500, 1),
		tool(start.Add(97*time.Hour), "codex", "alpha", "health", "s3", observe.OutcomeSuccess, true, 1, 1, 9),
		ready(start.Add(time.Hour), "codex", "alpha", "s1", 100),
		ready(start.Add(2*time.Hour), "codex", "alpha", "s2", 300),
		ready(start.Add(3*time.Hour), "codex", "alpha", "s3", 200),
		tool(start.Add(-time.Nanosecond), "codex", "alpha", "old", "s", observe.OutcomeSuccess, true, 1, 1, 1),
		tool(start.Add(7*24*time.Hour), "codex", "alpha", "new", "s", observe.OutcomeSuccess, true, 1, 1, 1),
	}
	snapshots := []process.Snapshot{
		{Version: 1, Mode: "observe", CapturedAt: start.Add(time.Hour), Processes: []process.Process{
			{Identity: process.Identity{PID: 1, StartTicks: 10}, Service: "alpha", PSSBytes: 10, USSBytes: 5},
			{Identity: process.Identity{PID: 2, StartTicks: 20}, Service: "alpha", PSSBytes: 20, USSBytes: 7},
		}},
		{Version: 1, Mode: "observe", CapturedAt: start.Add(2 * time.Hour), Processes: []process.Process{
			{Identity: process.Identity{PID: 1, StartTicks: 10}, Service: "alpha", PSSBytes: 40, USSBytes: 12},
			{Identity: process.Identity{PID: 3, StartTicks: 30}, Service: "beta", PSSBytes: 50, USSBytes: 25},
		}},
	}

	got, err := Aggregate(start, start.Add(7*24*time.Hour), events, snapshots)
	if err != nil {
		t.Fatal(err)
	}
	wantTools := []ToolRow{
		{Client: "codex", Service: "alpha", Tool: "search", Calls: 4, EffectiveHits: 2, ActiveDays: 4,
			DistinctSessions: 3, SuccessRate: .5, P50DurationMS: 30, P95DurationMS: 50, P95ResponseBytes: 500},
		{Client: "vscode", Service: "beta", Tool: "z", Calls: 1, ActiveDays: 1, DistinctSessions: 1,
			P50DurationMS: 10, P95DurationMS: 10, P95ResponseBytes: 100},
	}
	if !reflect.DeepEqual(got.Tools, wantTools) {
		t.Fatalf("tools = %#v, want %#v", got.Tools, wantTools)
	}
	wantServices := []ServiceRow{
		{Service: "alpha", ProcessStarts: 2, PeakPSSBytes: 40, PeakUSSBytes: 12, MaxConcurrentCalls: 3,
			P50ColdStartMS: 200, P95ColdStartMS: 300},
		{Service: "beta", ProcessStarts: 1, PeakPSSBytes: 50, PeakUSSBytes: 25, MaxConcurrentCalls: 1},
	}
	if !reflect.DeepEqual(got.Services, wantServices) {
		t.Fatalf("services = %#v, want %#v", got.Services, wantServices)
	}
}

func TestAggregateRejectsInvalidWindowAndEvents(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if _, err := Aggregate(start, start, nil, nil); err == nil {
		t.Fatal("Aggregate accepted empty window")
	}
	bad := tool(start, "codex", "svc", "tool", "session", observe.OutcomeSuccess, true, 1, 1, 1)
	bad.Version = 0
	if _, err := Aggregate(start, start.Add(time.Hour), []observe.Event{bad}, nil); err == nil {
		t.Fatal("Aggregate accepted invalid event")
	}
}

func TestAggregateBucketsActiveDaysInWindowTimezone(t *testing.T) {
	zone := time.FixedZone("UTC+8", 8*60*60)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, zone)
	events := []observe.Event{
		tool(time.Date(2026, 7, 1, 0, 30, 0, 0, zone), "codex", "svc", "search", "s1",
			observe.OutcomeSuccess, true, 1, 1, 1),
		tool(time.Date(2026, 7, 1, 23, 30, 0, 0, zone), "codex", "svc", "search", "s2",
			observe.OutcomeSuccess, true, 1, 1, 1),
	}

	got, err := Aggregate(start, start.Add(24*time.Hour), events, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tools[0].ActiveDays != 1 {
		t.Fatalf("active days = %d, want 1 for one window-local calendar date", got.Tools[0].ActiveDays)
	}
}

func tool(at time.Time, client, service, name, session string, outcome observe.Outcome, effective bool,
	duration int64, bytes, concurrent int,
) observe.Event {
	return observe.Event{Version: 1, Kind: observe.KindToolCall, At: at, Client: client, Service: service, Tool: name,
		SessionHash: session, Outcome: outcome, Effective: effective, DurationMS: duration, ResponseBytes: bytes,
		ConcurrentCalls: concurrent}
}

func ready(at time.Time, client, service, session string, duration int64) observe.Event {
	return observe.Event{Version: 1, Kind: observe.KindSessionReady, At: at, Client: client, Service: service,
		SessionHash: session, DurationMS: duration}
}
