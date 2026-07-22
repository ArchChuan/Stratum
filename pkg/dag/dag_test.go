package dag

import (
	"testing"
)

func TestValidateRejectsInvalidGraphs(t *testing.T) {
	tests := []struct {
		name  string
		nodes []Node
	}{
		{name: "empty id", nodes: []Node{{ID: ""}}},
		{name: "duplicate id", nodes: []Node{{ID: "one"}, {ID: "one"}}},
		{name: "missing dependency", nodes: []Node{{ID: "one", DependsOn: []string{"missing"}}}},
		{name: "self dependency", nodes: []Node{{ID: "one", DependsOn: []string{"one"}}}},
		{name: "cycle", nodes: []Node{{ID: "one", DependsOn: []string{"two"}}, {ID: "two", DependsOn: []string{"one"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Validate(tt.nodes); err == nil {
				t.Fatal("Validate() error = nil, want non-nil")
			}
		})
	}
}

func TestReadyReturnsSortedRootsAndWaitsForFanIn(t *testing.T) {
	nodes := []Node{
		{ID: "join", DependsOn: []string{"right", "left"}},
		{ID: "right", DependsOn: []string{"root"}},
		{ID: "root"},
		{ID: "left", DependsOn: []string{"root"}},
	}

	ready, blocked, complete, err := Ready(Snapshot{Nodes: nodes})
	assertResult(t, ready, blocked, complete, err, []string{"root"}, nil, false)

	ready, blocked, complete, err = Ready(Snapshot{
		Nodes: nodes,
		Statuses: map[string]Status{
			"root": StatusSucceeded,
		},
	})
	assertResult(t, ready, blocked, complete, err, []string{"left", "right"}, nil, false)

	ready, blocked, complete, err = Ready(Snapshot{
		Nodes: nodes,
		Statuses: map[string]Status{
			"root":  StatusSucceeded,
			"left":  StatusSucceeded,
			"right": StatusSucceeded,
		},
	})
	assertResult(t, ready, blocked, complete, err, []string{"join"}, nil, false)
}

func TestReadyBlocksDescendantsOfFailedNodes(t *testing.T) {
	ready, blocked, complete, err := Ready(Snapshot{
		Nodes: []Node{
			{ID: "root"},
			{ID: "child", DependsOn: []string{"root"}},
			{ID: "grandchild", DependsOn: []string{"child"}},
		},
		Statuses: map[string]Status{"root": StatusFailed},
	})
	assertResult(t, ready, blocked, complete, err, nil, []string{"child", "grandchild"}, true)
}

func TestReadyReportsCompleteWhenEveryNodeIsTerminal(t *testing.T) {
	ready, blocked, complete, err := Ready(Snapshot{
		Nodes: []Node{{ID: "one"}, {ID: "two", DependsOn: []string{"one"}}},
		Statuses: map[string]Status{
			"one": StatusSucceeded,
			"two": StatusCancelled,
		},
	})
	assertResult(t, ready, blocked, complete, err, nil, nil, true)
}

func TestReadyRejectsUnknownStatus(t *testing.T) {
	_, _, _, err := Ready(Snapshot{
		Nodes:    []Node{{ID: "one"}},
		Statuses: map[string]Status{"one": "mystery"},
	})
	if err == nil {
		t.Fatal("Ready() error = nil, want non-nil")
	}
}

func assertResult(
	t *testing.T,
	ready, blocked []string,
	complete bool,
	err error,
	wantReady, wantBlocked []string,
	wantComplete bool,
) {
	t.Helper()
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	if !equalStrings(ready, wantReady) {
		t.Fatalf("Ready() ready = %v, want %v", ready, wantReady)
	}
	if !equalStrings(blocked, wantBlocked) {
		t.Fatalf("Ready() blocked = %v, want %v", blocked, wantBlocked)
	}
	if complete != wantComplete {
		t.Fatalf("Ready() complete = %v, want %v", complete, wantComplete)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
