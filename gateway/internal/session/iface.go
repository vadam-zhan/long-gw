package session

import (
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// Deps：Session 的所有依赖打包
type Deps struct {
	Worker             types.WorkerSubmitter
	LocalRouter        types.LocalRouterOps
	DistributionRouter types.DistRouterOps
	Offline            types.OfflineStore // nil = 不支持离线存储（Live 弹幕等 QoS-0 场景）

	SuspendTTL    time.Duration
	RetryInterval time.Duration
	MaxRetries    int
}

// ─────────────────────────────────────────────────────────────────────
// pendingEntry：QoS-1 重试队列的单条记录
// ─────────────────────────────────────────────────────────────────────
type pendingEntry struct {
	msg         *gateway.Message
	retryCount  int
	firstSentAt time.Time
	lastRetryAt time.Time
}
