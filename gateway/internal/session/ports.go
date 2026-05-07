package session

import (
	"time"

	"github.com/vadam-zhan/long-gw/gateway/internal/delivery/retry"
	"github.com/vadam-zhan/long-gw/gateway/internal/timer"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// Deps：Session 的所有依赖打包
type Deps struct {
	Timer              timer.Scheduler
	Worker             types.WorkerSubmitter
	LocalRouter        types.LocalRouterOps
	DistributionRouter types.DistRouterOps
	Offline            types.OfflineStore // nil = 不支持离线存储（Live 弹幕等 QoS-0 场景）
	Acker              types.Acker        // nil = 不支持 QoS-1 追踪

	SuspendTTL    time.Duration
	RetryInterval time.Duration
	MaxRetries    int
	RetryPolicy   retry.Policy
}
