package domain

import "testing"

func TestBootstrapQualityDifferenceDetectsClearImprovement(t *testing.T) {
	stable := make([]float64, 100)
	canary := make([]float64, 100)
	for i := range stable {
		stable[i] = 0.4 + float64(i%5)/100
		canary[i] = 0.8 + float64(i%5)/100
	}
	improvement, significant, err := BootstrapQualityDifference(stable, canary, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if improvement < 0.3 || !significant {
		t.Fatalf("expected clear significant improvement, got improvement=%f significant=%v", improvement, significant)
	}
}

func TestBootstrapQualityDifferenceDoesNotPromoteEquivalentSamples(t *testing.T) {
	stable := []float64{0.5, 0.6, 0.4, 0.5, 0.6, 0.4}
	canary := append([]float64(nil), stable...)
	_, significant, err := BootstrapQualityDifference(stable, canary, 500)
	if err != nil {
		t.Fatal(err)
	}
	if significant {
		t.Fatal("equivalent samples must not be significant")
	}
}
