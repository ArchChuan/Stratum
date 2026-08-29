// internal/evaluation/application/observation_sampling_test.go
package application

import "testing"

func TestSampleDecisionBoundaries(t *testing.T) {
	if sampleDecision(0, "agent", "trace-1") {
		t.Fatal("sampleRate 0 must never sample")
	}
	if !sampleDecision(1, "agent", "trace-1") {
		t.Fatal("sampleRate 1 must always sample")
	}
	if sampleDecision(0.0, "agent", "trace-1") {
		t.Fatal("zero rate must reject")
	}
}

func TestSampleDecisionDeterministic(t *testing.T) {
	// trace-sample 的 sha256("agent/trace-sample") 前 8 字节映射到 [0,1) 得
	// ~0.3326，在 0.5 采样率下必须被采样（实现前已用脚本验证，非魔数侥幸）。
	if !sampleDecision(0.5, "agent", "trace-sample") {
		t.Fatal("expected sample for trace-sample")
	}
	if sampleDecision(0.5, "agent", "trace-sample") != sampleDecision(0.5, "agent", "trace-sample") { //nolint:staticcheck // 有意断言确定性：同一 trace 两次调用必须一致（SA4000 对纯函数误报）
		t.Fatal("same trace must be idempotent")
	}
}

func TestSampleDecisionRateMonotonic(t *testing.T) {
	// 采样率提高时，被采样的 trace 集合单调不缩（同 trace 从拒到采，绝不反转）。
	lower := map[string]bool{}
	for i := 0; i < 500; i++ {
		tid := string(rune('a'+i%26)) + string(rune('0'+i/26))
		lower[tid] = sampleDecision(0.2, "agent", tid)
	}
	for tid, sampled := range lower {
		if sampled && !sampleDecision(0.9, "agent", tid) {
			t.Fatalf("trace %s sampled at 0.2 but rejected at 0.9", tid)
		}
	}
}
