package session

import "time"

// 全局 AckRetrier：单 goroutine 扫描所有 Session 的 pendingAcks
type AckRetrier struct {
	interval time.Duration
	maxRetry int
}
