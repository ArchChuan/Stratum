package persistence

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestInterleaveBalancedAlternatesNegativeAndNonNegative(t *testing.T) {
	neg := func(score float64) domain.CaseSample { return domain.CaseSample{Score: &score} }
	noScore := domain.CaseSample{}
	in := []domain.CaseSample{neg(0.1), neg(0.2), noScore, neg(0.9)}
	got := interleaveBalanced(in)
	if len(got) != 4 {
		t.Fatalf("length changed: %d", len(got))
	}
	// A negative sample must lead (source order grouped negatives first).
	if !isNegative(got[0].Score) {
		t.Fatalf("expected negative first: %+v", got[0])
	}
	// Adjacent samples must alternate signs until one side is exhausted.
	for i := 1; i < len(got); i++ {
		if isNegative(got[i].Score) == isNegative(got[i-1].Score) {
			t.Fatalf("no alternation at index %d: %+v vs %+v", i, got[i-1], got[i])
		}
	}
}

func TestInterleaveBalancedEmptyAndSingle(t *testing.T) {
	if got := interleaveBalanced(nil); len(got) != 0 {
		t.Fatalf("expected empty output, got %d", len(got))
	}
	single := []domain.CaseSample{{}}
	if got := interleaveBalanced(single); len(got) != 1 {
		t.Fatalf("expected single output, got %d", len(got))
	}
}

func TestIsNegativeBoundary(t *testing.T) {
	cases := []struct {
		name  string
		score *float64
		want  bool
	}{
		{name: "missing score is not negative", score: nil, want: false},
		{name: "0.49 is negative", score: ptrf(0.49), want: true},
		{name: "0.5 is not negative", score: ptrf(0.5), want: false},
		{name: "0 is negative", score: ptrf(0), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNegative(tc.score); got != tc.want {
				t.Fatalf("isNegative(%v) = %v, want %v", tc.score, got, tc.want)
			}
		})
	}
}

func ptrf(v float64) *float64 { return &v }
