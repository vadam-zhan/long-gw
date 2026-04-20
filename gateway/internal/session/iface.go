package session

import (
	"context"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/connection"
)

// ─────────────────────────────────────────────────────────────────────
// Session 依赖的外部接口
//
// 这些接口定义了 Session 与其他组件的交互边界。
// Session 不直接依赖具体类型，通过接口解耦。
// ─────────────────────────────────────────────────────────────────────

// WorkerSubmitter：Session 向 Worker 提交上行消息的接口。
// 实现：WorkerManager.SubmitUpstream
type WorkerSubmitter interface {
	// SubmitUpstream 将上行消息提交到对应 BizCode 的 WorkerPool.upstreamCh。
	// 返回 ErrPoolFull 时，调用方（SubmitStage）向客户端回 5001。
	SubmitUpstream(bizCode string, sess *Session, msg *gateway.Message) error
}

// LocalRouterOps：Session 对 LocalRouter 的操作接口。
// Session 需要在 AttachConn 时恢复订阅，在 Close 时清理索引。
type LocalRouterOps interface {
	JoinRoom(roomID string, sess *Session)
	LeaveRoom(roomID string, sess *Session)
	Subscribe(topic string, sess *Session)
	Unsubscribe(topic string, sess *Session)
	UnregisterAll(sess *Session) // 关闭时清理所有 room/topic 注册
	RegisterSession(userID, deviceID string, sess *Session) (kicked *Session)
	UnregisterSession(userID, deviceID string)
}

// OfflineStorer：Session 向离线存储持久化消息的接口。
type OfflineStorer interface {
	Store(ctx context.Context, msg *gateway.Message) error
}

// Deps：Session 的所有依赖打包
type Deps struct {
	Worker      WorkerSubmitter
	LocalRouter LocalRouterOps
	// DistributionRouter *router.DistributedRouter
	ConnectionFactory *connection.Factory
	Offline           OfflineStorer // nil = 不支持离线存储（Live 弹幕等 QoS-0 场景）
	SuspendTTL        time.Duration
	RetryInterval     time.Duration
	MaxRetries        int
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
