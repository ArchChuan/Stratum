package constants

import "time"

// Collab worker polling bounds.
const (
	// CollabIdleInterval 是 collab worker 空队列时的最小轮询间隔：
	// 没有可认领的 step 时按此间隔空转，禁止紧接重查（2026-08-21 CPU 打满事故）。
	CollabIdleInterval = 1 * time.Second
)
