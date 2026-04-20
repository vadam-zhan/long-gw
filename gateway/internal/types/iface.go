// Package types 是所有跨层接口的唯一真实来源。
//
// 问题背景: 没有这个包会导致循环依赖，因为:
//
//	session  imports worker  (for SubmitUpstream)
//	worker   imports session (for SessionRef — error callbacks)
//	session  imports router  (for JoinRoom/Subscribe)
//	router   imports session (to store *Session in maps)
//	handler  imports session (for Session interface)
//	session  is imported by  handler
//
// 解决方案: 各层只导入本包获取所需接口，不导入其他层的具体包。
// 唯一导入具体包的地方是 cmd/gateway/wire.go。
//
// 依赖规则:
//
//	types  → common-protocol/v1  (仅导入wire类型)
//	others → types               (各层通过此获取接口)
//	wire   → everything          (仅做组装)
//
// 接口命名规范:
//
//	<Consumer><Role>  例如 HandlerSession (handler层视角的Session)
//
// 每个接口都是消费者实际需要的最小表面。
// 大接口按职责拆分为多个小接口（接口分离原则ISP）。
package types

import (
	"context"

	proto "github.com/vadam-zhan/long-gw/common-protocol/v1"
)

// ════════════════════════════════════════════════════════════════════════════
// Session — 不同层从各自角度看到的 Session
// ════════════════════════════════════════════════════════════════════════════

// HandlerSession 是 handler 层视角的 Session（HandlerRegistry）。
// Handler 在按 SignalType 路由后调用有状态的 session 操作。
type HandlerSession interface {
	// 身份标识
	SessionID() string
	UserID() string
	DeviceID() string

	// AckHandler 调用此方法清除 QoS-1 的 pendingAcks。
	Ack(msgID string)

	IsActive() bool

	// SubscribeHandler / UnsubscribeHandler 调用这些方法。
	// 实现需在 subscriptionSet 中持久化状态，并在 LocalRouter 中注册。
	JoinRoom(roomID string)
	LeaveRoom(roomID string)
	Subscribe(topic string)
	Unsubscribe(topic string)

	// UpstreamHandler（通过 UplinkChain.SubmitStage）调用此方法。
	// 将消息路由到 WorkerManager.upstreamCh。
	SubmitUpstream(msg *proto.Message) error

	// LogoutHandler 调用此方法关闭 session。
	Close(kick *proto.KickPayload)
}

// Session 是 uplink stages 对 Session 的最小接口。
// 只暴露 stages 实际需要的方法，避免 stages 对 Session 的过度依赖。
type UplinkSession interface {
	SubmitUpstream(msg *proto.Message) error // SubmitStage 调用
	UserID() string                          // RateLimitStage 调用（限流 key）
	SessionID() string                       // TraceStage / MetricsStage 日志
}

// SessionRef 是 Worker 视角的 Session。
// Worker 只需投递能力和身份信息 — 它不能导入 session 包。
// upstreamWorker 用此接口在 Kafka 发送失败后将错误帧发回。
type SessionRef interface {
	// Submit 通过 conn.writeCh 向客户端投递消息。非阻塞。
	// upstreamWorker 调用此方法将错误回复路由回 Session。
	Submit(msg *proto.Message) bool

	// 以下方法供 DownlinkChain 的 RouteStage 和 FanOutStage 使用
	IsActive() bool
	SessionID() string
	UserID() string
}

// SessionTarget 是下行链路（FanOutStage）和 Router 视角的 Session。
// 设计上与 SessionRef 形状相同；保留为独立类型以明确意图。
type SessionTarget interface {
	Submit(msg *proto.Message) bool
	IsActive() bool
	SessionID() string
	UserID() string
	DeviceID() string
	Close(kick *proto.KickPayload)
}

// FactorySession 是 ConnectionFactory 视角的 Session。
// 在 HandlerSession 基础上扩展了生命周期方法（AttachConn、DetachConn），
// 这些方法只有 Factory 才应该调用。
type FactorySession interface {
	HandlerSession

	// AttachConn 将新的 Connection 绑定到 Session。
	// 首次连接和重连时都会调用。
	// 返回 lastDeliveredSeq 用于触发离线消息重放。
	AttachConn(conn ConnSubmitter) (lastDeliveredSeq uint64)

	// DetachConn 将 Session 转为 Suspended 状态（conn=nil）。
	// 在 Connection.Run 退出后的 onClose 中调用。
	DetachConn()

	Submit(msg *proto.Message) bool
}

// ConnSubmitter 是 Session 视角的 Connection。
// Session 只需调用 Submit（向客户端投递消息）。
// 这打破了潜在的循环依赖: session ← connection ← session。
//
// session 导入 types.ConnSubmitter
// connection 实现 types.ConnSubmitter（但 session 不导入 connection）
type ConnSubmitter interface {
	Submit(msg *proto.Message) bool
	IsActive() bool
	Close(kick *proto.KickPayload)
	GetConnID() string
	GetUserID() string
	GetDeviceType() string
}

// ════════════════════════════════════════════════════════════════════════════
// Router 接口
// ════════════════════════════════════════════════════════════════════════════

// LocalRouterOps 是 Session 视角的 LocalRouter。
// Session 在调用 JoinRoom/Subscribe/DetachConn 时调用这些方法注册/注销房间和主题索引。
// session 导入 types.LocalRouterOps，而非 router.LocalRouter。
type LocalRouterOps interface {
	JoinRoom(roomID string, sess SessionTarget)
	LeaveRoom(roomID string, sess SessionTarget)
	Subscribe(topic string, sess SessionTarget)
	Unsubscribe(topic string, sess SessionTarget)
	// UnregisterAll 从所有房间/主题中移除 sess。DetachConn 时调用。
	UnregisterAll(sess SessionTarget)

	RegisterSession(userID, deviceID string, sess SessionTarget) (kicked SessionTarget)
	UnregisterSession(userID, deviceID string)
}

// ─────────────────────────────────────────────────────────────────────
// SessionResolver：RouteStage 对 LocalRouter 的依赖接口
//
// 实现方：LocalRouter.Resolve
// LocalRouter 维护三个索引：
//
//	"u:{uid}"    → GetUserSessions(uid)    → userSessions[uid]
//	"r:{roomID}" → RoomMembers(roomID)     → rooms[roomID]
//	"t:{topic}"  → TopicSubscribers(topic) → topics[topic]
//
// SessionResolver 是下行链路和 Worker 视角的 LocalRouter。
// RouteStage 调用 Resolve 获取 To 地址对应的目标 Session。
// worker 和 pipeline/downlink 导入 types.SessionResolver，而非 router.LocalRouter。
type SessionResolver interface {
	// Resolve 解析 To 地址（如 "u:uid"、"r:roomID"、"t:topic"）
	// 并返回本节点上匹配的活动 Session。
	Resolve(to string) ([]SessionTarget, bool)
}

// SessionRegistrar 是 Factory 视角的 LocalRouter。
// Factory 在认证后注册 Session，关闭时注销。
// 与 LocalRouterOps 分离以强制每个消费者使用最小接口。
type SessionRegistrar interface {
	// RegisterSession 将 sess 插入用户扇出索引。
	// 如果同一 deviceID 已存在其他 Session，返回被踢出的 FactorySession。
	RegisterSession(userID, deviceID string, sess FactorySession) (kicked FactorySession)

	// UnregisterSession 从用户扇出索引中移除 sess。
	// 当 Session 永久关闭（非仅 Suspended）时调用。
	UnregisterSession(userID, deviceID string)

	// UnregisterAll 从所有房间/主题索引中移除。连接关闭时调用。
	UnregisterAll(sess FactorySession)
}

// DistRouterOps 是 Factory 视角的 DistributedRouter。
type DistRouterOps interface {
	RegisterUser(ctx context.Context, userID, deviceID string) error
	UnregisterUser(ctx context.Context, userID, deviceID string) error
	Refresh(ctx context.Context, userID, deviceID string) error
}

// ════════════════════════════════════════════════════════════════════════════
// Worker 接口
// ════════════════════════════════════════════════════════════════════════════

// WorkerSubmitter 是 Session 视角的 WorkerManager。
// Session.SubmitUpstream 调用此方法将消息路由到正确的 WorkerPool。
// session 导入 types.WorkerSubmitter，而非 worker.Manager。
type WorkerSubmitter interface {
	// SubmitUpstream 将 msg 路由到 pool[bizCode].upstreamCh。
	// sess (SessionRef) 包含在 UpstreamJob 中，以便 upstreamWorker
	// 在 Kafka 发送失败时调用 sess.Submit(errMsg)。
	// SubmitUpstream 上行提交(Handler -> Pipeline -> WorkerManager)
	SubmitUpstream(bizCode string, sess SessionRef, msg *proto.Message) error
}

// DownstreamSubmitter 是 Kafka 消费者视角的 WorkerManager。
// KafkaDownstreamConsumer 调用此方法将消息移交给 Worker。
type DownstreamSubmitter interface {
	SubmitDownstream(bizCode string, msg *proto.Message) bool
}

// ════════════════════════════════════════════════════════════════════════════
// Storage 接口
// ════════════════════════════════════════════════════════════════════════════

// OfflineStore 由 Session 和 FanOutStage 使用。
// OfflineStore：FanOutStage 存储无法投递消息的接口
// 当目标处于 Suspended 状态或 writeCh 满时持久化 QoS-1 消息。
type OfflineStore interface {
	Store(ctx context.Context, msg *proto.Message) error
	Fetch(ctx context.Context, userID string, afterSeq uint64) ([]*proto.Message, error)
	Delete(ctx context.Context, msgID string) error
}

// ════════════════════════════════════════════════════════════════════════════
// Auth
// ════════════════════════════════════════════════════════════════════════════

// AuthVerifier 由 ConnectionFactory 在握手期间使用。
type AuthVerifier interface {
	Verify(token string) (userID, connID string, err error)
}

// ════════════════════════════════════════════════════════════════════════════
// Pipeline chain 接口
// ════════════════════════════════════════════════════════════════════════════

// UplinkChain 是 UpstreamHandler 视角的上行链路管道。
// handler 导入 types.UplinkChain，而非 pipeline/uplink。
type UplinkChain interface {
	Run(sess UplinkSession, conn ConnSubmitter, msg *proto.Message)
}

// ════════════════════════════════════════════════════════════════════════════
// Registry 接口
// ════════════════════════════════════════════════════════════════════════════

// ConnRegistry 由 Factory 使用。
type ConnRegistry interface {
	RegisterConn(connID string, conn ConnSubmitter) error
	UnregisterConn(connID string)
	GetConn(connID string) (ConnSubmitter, bool)
}

// SessionRegistry 由 Factory 使用。
type SessionRegistry interface {
	// GetOrCreate 重连时返回 (existing, false)，首次连接时返回 (new, true)。
	GetOrCreate(userID, deviceID, appID, deviceType, bizCode string) (FactorySession, bool)

	// GetAllByUser 返回用户的所有 Session，用于管理员踢人操作。
	GetAllByUser(userID string) []FactorySession

	Count() int
}

// HandlerRegistry 由 Factory 使用。
type HandlerRegistry interface {
	Dispatch(sess HandlerSession, conn ConnSubmitter, msg *proto.Message) error
	AuthVerifier() AuthVerifier
}

type MsgHandler interface {
	Handle(sess HandlerSession, conn ConnSubmitter, msg *proto.Message) error
}

// ─────────────────────────────────────────────────────────────────────
// UpstreamSender：对 Kafka/gRPC 的抽象
// ─────────────────────────────────────────────────────────────────────
type UpstreamSender interface {
	Send(ctx context.Context, msg *proto.Message) error
}
