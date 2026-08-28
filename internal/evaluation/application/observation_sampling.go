// internal/evaluation/application/observation_sampling.go
package application

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// sampleDecision 决定一条 trace 是否进入 judge 采样。用
// sha256(resourceKind + "/" + traceID) 的确定性哈希映射到 [0,1) 桶，
// 与采样率比较：同一 (resourceKind, traceID) 幂等、采样率单调（采样集不缩）。
// sampleRate ∈ [0,1]；0 永不采样、1 全采样。
func sampleDecision(sampleRate float64, resourceKind, traceID string) bool {
	if sampleRate <= 0 {
		return false
	}
	if sampleRate >= 1 {
		return true
	}
	h := sha256.Sum256([]byte(resourceKind + "/" + traceID))
	n := binary.BigEndian.Uint64(h[:8])
	return float64(n)/float64(math.MaxUint64) < sampleRate
}
