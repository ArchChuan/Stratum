package constants

import "testing"

func TestDynamicRecentGroups(t *testing.T) {
	tests := []struct {
		name string
		w    int
		want int
	}{
		{name: "zero window falls back to default", w: 0, want: LoopCompactionRecentGroups},
		{name: "negative window falls back to default", w: -1, want: LoopCompactionRecentGroups},
		{name: "small window keeps 2 groups", w: 8_000, want: CompactionRecentGroupsSmall},
		{name: "window at small threshold boundary keeps 3", w: 16_000, want: LoopCompactionRecentGroups},
		{name: "mid window keeps 3 groups", w: 32_000, want: LoopCompactionRecentGroups},
		{name: "window at large threshold boundary keeps 3", w: 64_000, want: LoopCompactionRecentGroups},
		{name: "large window keeps 5 groups", w: 96_000, want: CompactionRecentGroupsLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DynamicRecentGroups(tc.w); got != tc.want {
				t.Fatalf("DynamicRecentGroups(%d) = %d, want %d", tc.w, got, tc.want)
			}
		})
	}
}

func TestDynamicSummaryReserve(t *testing.T) {
	tests := []struct {
		name   string
		budget int
		want   int
	}{
		{name: "tiny budget hits the floor", budget: 100, want: CompactionSummaryReserveFloor},
		{name: "small budget hits the floor", budget: 4_000, want: CompactionSummaryReserveFloor},
		{name: "mid budget scales at 5%", budget: 20_000, want: 1_000},
		{name: "large budget scales at 5%", budget: 100_000, want: 5_000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DynamicSummaryReserve(tc.budget); got != tc.want {
				t.Fatalf("DynamicSummaryReserve(%d) = %d, want %d", tc.budget, got, tc.want)
			}
		})
	}
}
