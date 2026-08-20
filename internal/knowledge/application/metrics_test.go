package application

import (
	"math"
	"testing"
)

func TestRecallAtK(t *testing.T) {
	cases := []struct {
		name      string
		retrieved []string
		relevant  []string
		k         int
		want      float64
	}{
		{"all relevant in top k", []string{"a", "b", "c", "d"}, []string{"a", "b"}, 5, 1},
		{"partial recall", []string{"a", "x", "b"}, []string{"a", "b"}, 3, 1},
		{"k smaller than relevant set", []string{"a", "b"}, []string{"a", "b", "c"}, 2, 2.0 / 3.0},
		{"no hits", []string{"x", "y"}, []string{"a"}, 2, 0},
		{"empty relevant", []string{"a"}, nil, 2, 0},
		{"empty retrieved", nil, []string{"a"}, 2, 0},
		{"k beyond slice length", []string{"a"}, []string{"a", "b"}, 5, 0.5},
		{"duplicate ids count once", []string{"a", "a", "a", "a", "a"}, []string{"a"}, 5, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RecallAtK(tc.retrieved, tc.relevant, tc.k)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("RecallAtK() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPrecisionAtK(t *testing.T) {
	cases := []struct {
		name      string
		retrieved []string
		relevant  []string
		k         int
		want      float64
	}{
		{"all top k relevant", []string{"a", "b", "x"}, []string{"a", "b"}, 2, 1},
		{"half precision", []string{"a", "x"}, []string{"a"}, 2, 0.5},
		{"k beyond slice length", []string{"a"}, []string{"a"}, 5, 1},
		{"k zero", []string{"a"}, []string{"a"}, 0, 0},
		{"no hits", []string{"x"}, []string{"a"}, 1, 0},
		{"duplicate ids count once", []string{"a", "a", "a", "a", "a"}, []string{"a"}, 5, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PrecisionAtK(tc.retrieved, tc.relevant, tc.k)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("PrecisionAtK() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMRR(t *testing.T) {
	cases := []struct {
		name      string
		retrieved []string
		relevant  []string
		want      float64
	}{
		{"first result relevant", []string{"a", "b"}, []string{"a"}, 1},
		{"second result relevant", []string{"x", "b"}, []string{"b"}, 0.5},
		{"no relevant result", []string{"x", "y"}, []string{"a"}, 0},
		{"empty retrieved", nil, []string{"a"}, 0},
		{"duplicate ids count once", []string{"a", "a", "a", "a"}, []string{"a"}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MRR(tc.retrieved, tc.relevant)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("MRR() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNDCGAtK(t *testing.T) {
	// gains: rank1 = 1/log2(2) = 1, rank2 = 1/log2(3) ≈ 0.6309
	log2_3 := 1 / math.Log2(3)
	cases := []struct {
		name      string
		retrieved []string
		relevant  []string
		k         int
		want      float64
	}{
		{"perfect ordering", []string{"a", "b"}, []string{"a", "b"}, 2, 1},
		{"irrelevant first", []string{"x", "a"}, []string{"a"}, 2, log2_3},
		{"k zero", []string{"a"}, []string{"a"}, 0, 0},
		{"no relevant", []string{"x"}, []string{"a"}, 1, 0},
		{"empty retrieved", nil, []string{"a"}, 1, 0},
		{"duplicate ids count once", []string{"a", "a", "a", "a", "a"}, []string{"a"}, 5, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NDCGAtK(tc.retrieved, tc.relevant, tc.k)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("NDCGAtK() = %v, want %v", got, tc.want)
			}
		})
	}
}
