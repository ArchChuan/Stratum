package constants_test

import (
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

func TestMemoryConstants(t *testing.T) {
	// Buffer
	if constants.MemoryBufferFlushSize != 5 {
		t.Errorf("expected flush size 5, got %d", constants.MemoryBufferFlushSize)
	}
	if constants.MemoryBufferFlushInterval != 2*time.Minute {
		t.Errorf("expected flush interval 2min")
	}

	// Recall
	if constants.MemoryRecallTopK != 10 {
		t.Errorf("expected recall topK 10")
	}
	if constants.MemoryFrecencyLambda != 0.05 {
		t.Errorf("expected lambda 0.05")
	}

	// GC
	if constants.MemorySoftDeleteRetention != 30*24*time.Hour {
		t.Errorf("expected soft delete retention 30 days")
	}

	// Quota
	if constants.MemoryFactQuotaPerUser != 5000 {
		t.Errorf("expected fact quota 5000")
	}
}
