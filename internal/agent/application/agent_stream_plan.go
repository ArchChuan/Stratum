// Package application — agent_stream_plan.go.
//
// 订阅计划的纯函数层：把「客户端游标 + 服务端现状」翻译成订阅动作。
// 零 IO、零状态，因此可以穷举边界——游标判定是整个续传协议最容易出错的地方。
package application

import (
	"strconv"
	"strings"
)

// reset 原因。reset 由订阅侧合成，不写入流——它是订阅者对「游标与当前
// generation 不匹配」的翻译，不是 run 的产出（spec §6.6）。
const (
	ResetReasonGenerationChanged = "generation_changed"
	ResetReasonStreamLost        = "stream_lost"
)

// StreamPlan 是一次订阅的行动计划。
type StreamPlan struct {
	// Generation 是本次订阅要读的流分代。
	Generation int
	// Reset 为 true 时订阅侧必须先下发 reset 帧，前端据此清空当前气泡。
	Reset bool
	// ResetReason 见 ResetReason* 常量，Reset 为 false 时为空。
	ResetReason string
	// AfterID 是增量回放的起点（排他）；空串表示从流的开头全量回放。
	AfterID string
}

// PlanStream 把客户端游标与服务端现状翻译成订阅计划（spec §6.4/§6.5）。
//
//	genClient != genNow → reset + 全量回放当前 generation
//	genClient == genNow → 有游标则增量回放，无游标则全量回放（F5 场景）
//
// 缺 generation（genClient == 0）一律 reset：连续性上 fail closed。老客户端
// 恰好落在 F5 场景，全量回放正是它需要的（spec §10）。
func PlanStream(genClient, genNow int, lastEventID string) StreamPlan {
	if genClient != genNow {
		return StreamPlan{Generation: genNow, Reset: true, ResetReason: ResetReasonGenerationChanged}
	}
	return StreamPlan{Generation: genNow, AfterID: lastEventID}
}

// HasReplayGap 判定游标是否早于流现存最老条目——即 MAXLEN 裁剪已经把游标
// 之后应有的内容剪掉了。有缺口时增量回放会给出半截答案，必须 reset 重来，
// 而不是把带洞的文本交给用户（spec §6.6）。
func HasReplayGap(cursor, oldestID string) bool {
	if cursor == "" || oldestID == "" {
		return false
	}
	return CompareStreamID(cursor, oldestID) < 0
}

// CompareStreamID 按 Redis Stream ID 的数值语义比较两个 entry ID
// （形如 "<ms>-<seq>"），返回 -1 / 0 / 1。
//
// 不能退化成字符串比较：seq 段不补零，"...-9" 的字典序大于 "...-10"，
// 会让缺口判定把「游标落后」误判成「游标领先」，从而漏掉一次本该发生的
// reset。缺失或畸形的段按 0 处理——退化方向是「判为无缺口」，与
// HasReplayGap 的空串语义一致。
func CompareStreamID(a, b string) int {
	aMs, aSeq := splitStreamID(a)
	bMs, bSeq := splitStreamID(b)
	if aMs != bMs {
		return cmpInt64(aMs, bMs)
	}
	return cmpInt64(aSeq, bSeq)
}

func splitStreamID(id string) (int64, int64) {
	ms, seq, found := strings.Cut(id, "-")
	if !found {
		return parseStreamIDPart(ms), 0
	}
	return parseStreamIDPart(ms), parseStreamIDPart(seq)
}

func parseStreamIDPart(s string) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
