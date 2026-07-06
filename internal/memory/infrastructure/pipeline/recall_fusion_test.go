package pipeline

import "testing"

func TestFuseRecallCandidatesPrefersTextHitOnTie(t *testing.T) {
	vector := []recallCandidate{{ID: "weak-vector", Entry: RecallEntry{Content: "semantic but weak"}}}
	text := []recallCandidate{{ID: "exact-text", Entry: RecallEntry{Content: "contains exact keyword"}}}

	got := fuseRecallCandidates(vector, text, 2)

	if len(got) != 2 {
		t.Fatalf("expected 2 fused results, got %d", len(got))
	}
	if got[0].Content != "contains exact keyword" {
		t.Fatalf("expected text hit to win RRF tie, got %q", got[0].Content)
	}
}

func TestFuseRecallCandidatesRanksOverlapFirst(t *testing.T) {
	vector := []recallCandidate{
		{ID: "vector-only", Entry: RecallEntry{Content: "vector only"}},
		{ID: "both", Entry: RecallEntry{Content: "overlap"}},
	}
	text := []recallCandidate{
		{ID: "both", Entry: RecallEntry{Content: "overlap"}},
		{ID: "text-only", Entry: RecallEntry{Content: "text only"}},
	}

	got := fuseRecallCandidates(vector, text, 3)

	if len(got) != 3 {
		t.Fatalf("expected 3 fused results, got %d", len(got))
	}
	if got[0].Content != "overlap" {
		t.Fatalf("expected overlap to rank first, got %q", got[0].Content)
	}
}

func TestFuseRecallCandidatesDefaultsNonPositiveLimit(t *testing.T) {
	vector := []recallCandidate{
		{ID: "one", Entry: RecallEntry{Content: "one"}},
		{ID: "two", Entry: RecallEntry{Content: "two"}},
		{ID: "three", Entry: RecallEntry{Content: "three"}},
		{ID: "four", Entry: RecallEntry{Content: "four"}},
		{ID: "five", Entry: RecallEntry{Content: "five"}},
		{ID: "six", Entry: RecallEntry{Content: "six"}},
	}

	got := fuseRecallCandidates(vector, nil, 0)

	if len(got) != 5 {
		t.Fatalf("expected non-positive limit to default to 5, got %d", len(got))
	}
}

func TestFuseRecallCandidatesUsesContentAsFallbackID(t *testing.T) {
	vector := []recallCandidate{{Entry: RecallEntry{Content: "same memory"}}}
	text := []recallCandidate{{Entry: RecallEntry{Content: "same memory", Role: "user"}}}

	got := fuseRecallCandidates(vector, text, 5)

	if len(got) != 1 {
		t.Fatalf("expected candidates with empty IDs and same content to deduplicate, got %d", len(got))
	}
	if got[0].Role != "user" {
		t.Fatalf("expected text candidate metadata to be preserved, got role %q", got[0].Role)
	}
}
