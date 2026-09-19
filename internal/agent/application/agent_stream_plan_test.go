package application

import "testing"

func TestCompareStreamID(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int
	}{
		// seq 不补零："...-9" 的字典序大于 "...-10"，字符串比较会判反。
		{"sequence not zero padded", "1726483200000-9", "1726483200000-10", -1},
		{"sequence zero padded", "1726483200000-09", "1726483200000-10", -1},
		{"ms dominates", "1726483200000-99", "1726483200001-0", -1},
		{"equal", "1726483200000-37", "1726483200000-37", 0},
		{"greater", "1726483200001-0", "1726483200000-99", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CompareStreamID(tc.a, tc.b); got != tc.want {
				t.Errorf("CompareStreamID(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestPlanStream(t *testing.T) {
	cases := []struct {
		name        string
		genClient   int
		genNow      int
		lastEventID string
		wantReset   bool
		wantReason  string
		wantAfterID string
	}{
		{
			// F5：React 状态全丢，只发 generation。全量回放当前 gen，
			// 但不发 reset——气泡本来就是空的，发 reset 只会多一次清空。
			name:      "fresh reload replays from start without reset",
			genClient: 1, genNow: 1, lastEventID: "",
			wantReset: false, wantAfterID: "",
		},
		{
			// 网络抖动：内存游标存活，增量回放。
			name:      "cursor present replays incrementally",
			genClient: 1, genNow: 1, lastEventID: "1726483200000-37",
			wantReset: false, wantAfterID: "1726483200000-37",
		},
		{
			// pod 重启：generation 变了，必须清空重渲染。
			name:      "generation changed resets",
			genClient: 1, genNow: 2, lastEventID: "1726483200000-37",
			wantReset: true, wantReason: "generation_changed", wantAfterID: "",
		},
		{
			// 老客户端不传 generation：服务端 genNow ≥ 1，两侧不等 ⇒ reset
			// （fail closed）。对 F5 场景恰好正确——反正也是全量回放。
			// 原因与「分代真的变了」分开报，避免排障时把缺字段读成分代变更。
			name:      "missing generation resets",
			genClient: 0, genNow: 1, lastEventID: "",
			wantReset: true, wantReason: "generation_missing", wantAfterID: "",
		},
		{
			// 两侧同为 0：判据是相等而非「缺字段」，故不 reset。该组合生产不可达
			// （genNow 由服务端下发且列默认 1），本用例只是把实现行为钉死，
			// 防止有人把注释里的「缺 generation 一律 reset」当成代码来改。
			name:      "zero on both sides does not reset",
			genClient: 0, genNow: 0, lastEventID: "",
			wantReset: false, wantAfterID: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PlanStream(tc.genClient, tc.genNow, tc.lastEventID)
			if got.Reset != tc.wantReset || got.ResetReason != tc.wantReason || got.AfterID != tc.wantAfterID {
				t.Errorf("PlanStream = %+v, want reset=%v reason=%q after=%q",
					got, tc.wantReset, tc.wantReason, tc.wantAfterID)
			}
			if got.Generation != tc.genNow {
				t.Errorf("Generation = %d, want %d", got.Generation, tc.genNow)
			}
		})
	}
}

func TestHasReplayGap(t *testing.T) {
	cases := []struct {
		name     string
		cursor   string
		oldestID string
		want     bool
	}{
		{"no cursor is full replay not a gap", "", "1726483200000-1", false},
		{"empty stream is not a gap", "1726483200000-1", "", false},
		{"cursor before oldest is a gap", "1726483200000-1", "1726483200000-50", true},
		{"cursor at oldest is not a gap", "1726483200000-50", "1726483200000-50", false},
		{"cursor after oldest is not a gap", "1726483200000-99", "1726483200000-50", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasReplayGap(tc.cursor, tc.oldestID); got != tc.want {
				t.Errorf("HasReplayGap(%q,%q) = %v, want %v", tc.cursor, tc.oldestID, got, tc.want)
			}
		})
	}
}
