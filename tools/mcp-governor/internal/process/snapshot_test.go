package process

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestBuildSnapshotReportsRegisteredOrphanAndMemory(t *testing.T) {
	capturedAt := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
	client := Identity{PID: 20, StartTicks: 200}
	processes := []Process{
		{Identity: Identity{PID: 11, StartTicks: 110}, PPID: 1, Service: "obsidian", PSSBytes: 20},
		{Identity: Identity{PID: 10, StartTicks: 100}, PPID: 1, Service: "obsidian", RSSBytes: 40, PSSBytes: 10, USSBytes: 5},
	}
	registrations := []Registration{{Identity: processes[1].Identity, Client: client, Service: "obsidian", ConnectedAt: capturedAt}}

	snapshot := BuildSnapshot(capturedAt, processes, registrations, map[Identity]bool{}, []string{"z", "a"})

	if snapshot.Version != 1 || snapshot.Mode != "observe" || !snapshot.CapturedAt.Equal(capturedAt) {
		t.Errorf("metadata = %#v", snapshot)
	}
	if len(snapshot.Processes) != 2 {
		t.Fatalf("process count = %d; want 2", len(snapshot.Processes))
	}
	if got := snapshot.Processes[0]; got.PID != 10 || !got.Registered || !got.Orphan {
		t.Errorf("registered process = %#v", got)
	}
	if got := snapshot.Processes[1]; got.PID != 11 || got.Registered || got.Orphan {
		t.Errorf("unregistered PPID 1 process = %#v", got)
	}
	wantSummary := ServiceSummary{Service: "obsidian", Processes: 2, RSSBytes: 40, PSSBytes: 30, USSBytes: 5, Orphans: 1}
	if len(snapshot.Services) != 1 || snapshot.Services[0] != wantSummary {
		t.Errorf("services = %#v; want %#v", snapshot.Services, wantSummary)
	}
	if !reflect.DeepEqual(snapshot.Warnings, []string{"a", "z"}) {
		t.Errorf("warnings = %#v", snapshot.Warnings)
	}
}

func TestBuildSnapshotUsesExactIdentities(t *testing.T) {
	now := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
	tests := []struct {
		name         string
		process      Process
		registration Registration
		live         map[Identity]bool
		registered   bool
		orphan       bool
	}{
		{"live client", Process{Identity: Identity{10, 100}, Service: "svc"}, Registration{Identity: Identity{10, 100}, Client: Identity{20, 200}, Service: "svc", ConnectedAt: now}, map[Identity]bool{{20, 200}: true}, true, false},
		{"reused client PID", Process{Identity: Identity{10, 100}, Service: "svc"}, Registration{Identity: Identity{10, 100}, Client: Identity{20, 200}, Service: "svc", ConnectedAt: now}, map[Identity]bool{{20, 201}: true}, true, true},
		{"reused child PID", Process{Identity: Identity{10, 101}, Service: "svc"}, Registration{Identity: Identity{10, 100}, Client: Identity{20, 200}, Service: "svc", ConnectedAt: now}, map[Identity]bool{}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildSnapshot(now, []Process{tt.process}, []Registration{tt.registration}, tt.live, nil).Processes[0]
			if got.Registered != tt.registered || got.Orphan != tt.orphan {
				t.Errorf("flags = registered %v orphan %v; want %v %v", got.Registered, got.Orphan, tt.registered, tt.orphan)
			}
		})
	}
}

func TestBuildSnapshotRequiresRegistrationServiceToMatchClassification(t *testing.T) {
	now := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
	identity := Identity{PID: 10, StartTicks: 100}
	process := Process{Identity: identity, Service: "actual", PSSBytes: 42}
	registration := Registration{
		Identity: identity,
		Client:   Identity{PID: 20, StartTicks: 200},
		Service:  "different",
	}

	got := BuildSnapshot(now, []Process{process}, []Registration{registration}, map[Identity]bool{}, nil)

	if got.Processes[0].Registered || got.Processes[0].Orphan {
		t.Fatalf("wrong-service registration set ownership flags: %#v", got.Processes[0])
	}
	if len(got.Services) != 1 || got.Services[0].Service != "actual" || got.Services[0].PSSBytes != 42 {
		t.Fatalf("memory was not aggregated under actual service: %#v", got.Services)
	}
}

func TestBuildSnapshotHandlesDuplicatePIDsAndRegistrationsDeterministically(t *testing.T) {
	now := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
	processes := []Process{{Identity: Identity{7, 70}, Service: "svc"}, {Identity: Identity{7, 71}, Service: "svc"}}
	registrations := []Registration{
		{Identity: Identity{7, 70}, Client: Identity{1, 1}, Service: "svc", ConnectedAt: now},
		{Identity: Identity{7, 70}, Client: Identity{2, 2}, Service: "second", ConnectedAt: now},
	}
	got := BuildSnapshot(now, processes, registrations, map[Identity]bool{{1, 1}: true}, nil)
	if !got.Processes[0].Registered || got.Processes[0].Orphan || got.Processes[1].Registered {
		t.Errorf("processes = %#v", got.Processes)
	}
}

func TestBuildSnapshotFiltersUnclassifiedAndDoesNotMutateInputs(t *testing.T) {
	now := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
	processes := []Process{
		{Identity: Identity{2, 20}, Service: "b", Args: []string{"b"}, RSSBytes: 2, PSSBytes: 3, USSBytes: 4},
		{Identity: Identity{1, 10}, Args: []string{"unclassified"}},
		{Identity: Identity{3, 30}, Service: "a", Args: []string{"a"}, RSSBytes: 5, PSSBytes: 6, USSBytes: 7},
	}
	warnings := []string{"z", "a"}
	processesBefore := cloneProcesses(processes)
	warningsBefore := append([]string(nil), warnings...)
	live := map[Identity]bool{{9, 90}: true}
	liveBefore := map[Identity]bool{{9, 90}: true}

	got := BuildSnapshot(now, processes, nil, live, warnings)

	if len(got.Processes) != 2 || got.Processes[0].Service != "a" || got.Processes[1].Service != "b" {
		t.Errorf("processes = %#v", got.Processes)
	}
	if !reflect.DeepEqual(processes, processesBefore) || !reflect.DeepEqual(warnings, warningsBefore) || !reflect.DeepEqual(live, liveBefore) {
		t.Fatal("BuildSnapshot mutated its inputs")
	}
}

func TestBuildSnapshotStableJSONForReversedInputs(t *testing.T) {
	now := time.Date(2026, 7, 16, 1, 2, 3, 0, time.UTC)
	processes := []Process{{Identity: Identity{2, 20}, Service: "b"}, {Identity: Identity{3, 30}, Service: "a"}, {Identity: Identity{1, 10}, Service: "a"}}
	registrations := []Registration{{Identity: Identity{2, 20}, Client: Identity{8, 80}, Service: "b", ConnectedAt: now}, {Identity: Identity{1, 10}, Client: Identity{9, 90}, Service: "a", ConnectedAt: now}}
	warnings := []string{"z", "a"}

	forward, err := json.Marshal(BuildSnapshot(now, processes, registrations, map[Identity]bool{}, warnings))
	if err != nil {
		t.Fatal(err)
	}
	reverseProcesses(processes)
	reverseRegistrations(registrations)
	reverseStrings(warnings)
	reversed, err := json.Marshal(BuildSnapshot(now, processes, registrations, map[Identity]bool{}, warnings))
	if err != nil {
		t.Fatal(err)
	}
	if string(forward) != string(reversed) {
		t.Errorf("JSON differs:\n%s\n%s", forward, reversed)
	}
}

func cloneProcesses(in []Process) []Process {
	out := append([]Process(nil), in...)
	for i := range out {
		out[i].Args = append([]string(nil), out[i].Args...)
	}
	return out
}

func reverseProcesses(v []Process) {
	for i, j := 0, len(v)-1; i < j; i, j = i+1, j-1 {
		v[i], v[j] = v[j], v[i]
	}
}
func reverseRegistrations(v []Registration) {
	for i, j := 0, len(v)-1; i < j; i, j = i+1, j-1 {
		v[i], v[j] = v[j], v[i]
	}
}
func reverseStrings(v []string) {
	for i, j := 0, len(v)-1; i < j; i, j = i+1, j-1 {
		v[i], v[j] = v[j], v[i]
	}
}
