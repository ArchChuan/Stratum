package constants

import "time"

const (
	WorkflowOutputDeltaMaxRunes = 1024
	WorkflowToolNameMaxRunes    = 128
	WorkflowToolSummaryMaxRunes = 256
	WorkflowOutputFlushInterval = 100 * time.Millisecond
	// WorkflowIdleInterval 是 workflow worker 空队列时的最小轮询间隔：
	// 没有待推进的 run 时按此间隔空转，禁止紧接重查（2026-08-21 CPU 打满事故）。
	WorkflowIdleInterval = 1 * time.Second
	// WorkflowPausePollInterval 是 executeReadyBatch 批内暂停/取消检测的轮询间隔，
	// 也是 agent 审批仍 pending 时 attempt 的 RetryAt 推进间隔（reconcile 下 tick 再判）。
	WorkflowPausePollInterval = 500 * time.Millisecond
)
