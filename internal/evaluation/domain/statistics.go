package domain

import (
	"errors"
	"math/rand"
	"sort"
)

func BootstrapQualityDifference(
	stable, canary []float64,
	iterations int,
) (improvement float64, significant bool, err error) {
	if len(stable) < 2 || len(canary) < 2 {
		return 0, false, errors.New("bootstrap requires at least two samples per variant")
	}
	if iterations < 100 {
		return 0, false, errors.New("bootstrap requires at least 100 iterations")
	}
	improvement = mean(canary) - mean(stable)
	rng := rand.New(rand.NewSource(1)) // #nosec G404 -- deterministic statistical resampling
	differences := make([]float64, iterations)
	for i := range iterations {
		stableMean := resampledMean(stable, rng)
		canaryMean := resampledMean(canary, rng)
		differences[i] = canaryMean - stableMean
	}
	sort.Float64s(differences)
	lowerIndex := int(float64(iterations) * 0.025)
	return improvement, differences[lowerIndex] > 0, nil
}

func resampledMean(values []float64, rng *rand.Rand) float64 {
	total := 0.0
	for range values {
		total += values[rng.Intn(len(values))]
	}
	return total / float64(len(values))
}

func mean(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}
