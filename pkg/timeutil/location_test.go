package timeutil

import (
	"testing"
	"time"
)

func TestShanghaiIsNotUTC(t *testing.T) {
	if Shanghai.String() != "Asia/Shanghai" {
		t.Errorf("expected Asia/Shanghai, got %q", Shanghai.String())
	}
}

func TestNowIsInShanghai(t *testing.T) {
	now := Now()
	if now.Location() != Shanghai {
		t.Errorf("expected Now() to use Shanghai location, got %v", now.Location())
	}
}

func TestNowUTCIsEightHoursBehind(t *testing.T) {
	// 极端情况：上海时区固定为 UTC+8（无夏令时），Zone() 偏移必须为整 8 小时。
	_, offset := Now().Zone()
	if offset != 8*3600 {
		t.Errorf("expected UTC+8 offset, got %d", offset)
	}
}

func TestShanghaiHandlesDSTBoundary(t *testing.T) {
	// 极端情况：Asia/Shanghai 无 DST，冬夏两季偏移恒为 +8。
	for _, tt := range []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
	} {
		_, offset := tt.In(Shanghai).Zone()
		if offset != 8*3600 {
			t.Errorf("expected +8h for %v, got %d", tt, offset)
		}
	}
}

func TestNowRoundTripFormat(t *testing.T) {
	// 极端情况：格式化再解析必须回到同一时刻（精度到秒）。
	now := Now()
	formatted := now.Format(time.RFC3339)
	parsed, err := time.Parse(time.RFC3339, formatted)
	if err != nil {
		t.Fatalf("parse %q: %v", formatted, err)
	}
	if !parsed.Equal(now.Truncate(time.Second)) {
		t.Errorf("round trip mismatch: %v vs %v", parsed, now.Truncate(time.Second))
	}
}
