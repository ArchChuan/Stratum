package domain

import (
	"math"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// CalculateFrecency computes a combined score from importance, decay, and access frequency.
// Formula: importance × exp(-λ × days) × log(1 + accessCount)
// λ = 0.05 gives ~14-day half-life
func CalculateFrecency(importance, daysSinceAccess float64, accessCount int) float64 {
	decay := math.Exp(-constants.MemoryFrecencyLambda * daysSinceAccess)
	frequency := math.Log(1 + float64(accessCount))
	return importance * decay * frequency
}
