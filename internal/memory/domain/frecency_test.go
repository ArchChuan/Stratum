package domain_test

import (
	"math"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestCalculateFrecency(t *testing.T) {
	tests := []struct {
		name            string
		importance      float64
		daysSinceAccess float64
		accessCount     int
		wantMin         float64
		wantMax         float64
	}{
		{"fresh high importance", 0.9, 0.5, 5, 1.5, 1.7},
		{"stale low importance", 0.3, 30, 1, 0.04, 0.06},
		{"never accessed", 0.8, 100, 0, 0, 0},
		{"zero importance", 0.0, 1, 10, 0, 0},
		{"frequent moderate", 0.5, 7, 50, 1.3, 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := domain.CalculateFrecency(tt.importance, tt.daysSinceAccess, tt.accessCount)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("score %.4f not in range [%.4f, %.4f]", score, tt.wantMin, tt.wantMax)
			}
			if math.IsNaN(score) || math.IsInf(score, 0) {
				t.Errorf("score should be finite, got %v", score)
			}
		})
	}
}

func TestCalculateFrecencyDecay(t *testing.T) {
	// Recent access should score higher than old access
	recent := domain.CalculateFrecency(0.8, 1, 5)
	old := domain.CalculateFrecency(0.8, 30, 5)
	if recent <= old {
		t.Errorf("recent access score (%v) should be > old access score (%v)", recent, old)
	}
}

func TestCalculateFrecencyFrequency(t *testing.T) {
	// More accesses should boost score
	frequent := domain.CalculateFrecency(0.8, 10, 10)
	infrequent := domain.CalculateFrecency(0.8, 10, 2)
	if frequent <= infrequent {
		t.Errorf("frequent access score (%v) should be > infrequent score (%v)", frequent, infrequent)
	}
}
