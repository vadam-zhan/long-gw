package uplink

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/pipeline"
	"github.com/vadam-zhan/long-gw/gateway/internal/session"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
	"google.golang.org/protobuf/proto"
)

// File: gateway/internal/pipeline/uplink/stages.go
//
// ═══════════════════════════════════════════════════════════════════════
// 交互点二：UplinkChain stages 与 Session / Connection 的交互
//
// 每个 Stage 通过 *UplinkCtx 同时持有 Session 和 Connection 的引用：
//
//   UplinkCtx {
//       Session Session     ← 用于 SubmitUpstream（转发到 Worker）
//       Conn    *Connection ← 用于 Submit(errMsg)（直接回错给客户端）
//       Message *proto.Message
//       ReceivedAt time.Time
//   }
//
// Stage 与两者的交互规则：
//   - 向客户端回错：conn.Submit(errMsg)     → writeCh → WriteLoop → TCP
//   - 转发业务消息：sess.SubmitUpstream(msg) → Worker → Kafka
//   - 短路 pipeline：直接 return，不调用 next()
//
// 各 Stage 的交互边界：
//
//   RecoverStage  → conn.Submit(内部错误)   ← 捕获 panic
//   ValidateStage → conn.Submit(4001/4002)  ← 消息格式不合法
//   RateLimitStage→ conn.Submit(4290)       ← 超过限流阈值
//   TraceStage    → 无交互，只修改 msg.TraceID
//   MetricsStage  → 无交互，只记录指标（包裹 next()）
//   SubmitStage   → sess.SubmitUpstream(msg) ← 核心转发调用
//              on fail → conn.Submit(5001)
//
// ═══════════════════════════════════════════════════════════════════════

// Package uplink implements the uplink message processing pipeline.
//
// Pipeline stages (in execution order):
//
//	RecoverStage     — catch panics
//	FrameReadStage   — TCP read + frame decode → raw bytes
//	DecodeStage      — bytes → proto.Message struct
//	ValidateStage    — version, msg_id presence, expire check
//	SessionStage     — session lookup, auth state verification
//	RateLimitStage   — per-connection / per-user rate limit
//	TraceStage       — inject / propagate trace ID
//	MetricsStage     — record counters and latency histograms
//	DispatchStage    — route by MsgType:
//	                     Ping     → PongHandler
//	                     PushAck  → AckHandler
//	                     Data/IM/Live → MQPublishStage
//

// ─────────────────────────────────────────────────────────────────────
// UplinkCtx：stages 共享的上行上下文
//
// Session 和 Conn 同时存在于 ctx 中，每个 stage 按需选择：
//   - 需要回错给客户端：用 ctx.Conn（直接，不经 Session）
//   - 需要转发上行消息：用 ctx.Session（Session 再路由到 Worker）
//
// 为什么不直接从 conn 拿 Session？
//   → Connection 不持有 Session 引用（单向依赖，避免循环）
//   → UplinkCtx 由 Factory 注入的闭包在创建时绑定好两者
// ─────────────────────────────────────────────────────────────────────

// UplinkCtx 是上行 Pipeline 的上下文结构体。
type UplinkCtx struct {
	pipeline.BaseCtx

	// Session：用于业务层操作（SubmitUpstream）
	// 由 Factory 在闭包中绑定，stages 不知道具体的 Session 类型
	Session types.UplinkSession

	// Conn：用于立即回错（不经 Session，直接写 writeCh）
	// 注意：这里用的是 *Connection，不是 Session，因为回错是传输层操作
	Conn types.ConnSubmitter

	// Message：当前处理的消息
	Message *gateway.Message

	// ReceivedAt：消息进入 pipeline 的时间（MetricsStage 计算延迟用）
	ReceivedAt time.Time
}

// ReplyError 向客户端发送结构化错误帧。
//
// 交互路径：UplinkCtx.ReplyError → conn.Submit(errMsg) → writeCh → WriteLoop → TCP
//
// 注意：此方法直接操作 conn，不经过 Session。
// 原因：错误回复是连接层的即时响应，不需要 QoS-1 重试，也不存离线。
func (c *UplinkCtx) ReplyError(code int32, text string) {
	errMsg := &gateway.Message{
		Type: gateway.SignalType_ERROR,
	}
	errMsg.TraceId = c.TraceID
	if c.Message != nil {
		errMsg.AckId = c.Message.MsgId // 让客户端能关联到哪条消息出错了
	}

	payload := &gateway.ErrorPayload{
		Code:    code,
		Message: text,
	}
	p, _ := proto.Marshal(payload)
	errMsg.Body = &gateway.Body{
		Type:    c.Message.BizCode,
		Payload: p,
	}
	// conn.Submit 非阻塞：writeCh 满时返回 false（此时错误消息也丢弃，可接受）
	c.Conn.Submit(errMsg)
}

// ─────────────────────────────────────────────────────────────────────────────
// Dependencies injected into the pipeline at startup
// ─────────────────────────────────────────────────────────────────────────────

// Publisher is the interface the MQPublishStage uses to forward messages.
// Implemented by the Kafka adapter in gateway/mq.
type Publisher interface {
	// Publish sends msg to the topic derived from msg.BizCode.
	// Returns quickly (async); callers must handle back-pressure via context.
	Publish(ctx context.Context, msg *gateway.Message) error
}

// RateLimiter 接口：stages 对限流器的依赖
type RateLimiter interface {
	Allow(userID, bizCode string) bool
}

// SessionRegistry looks up active sessions by connection ID.
type SessionRegistry interface {
	GetByConn(connID string) (*session.Session, bool)
}

// Deps bundles all uplink pipeline dependencies. Pass it to BuildChain.
type Deps struct {
	Publisher   Publisher
	RateLimiter RateLimiter
	Registry    SessionRegistry
}

// ─────────────────────────────────────────────────────────────────────
// BuildChain 组装上行 Pipeline。
//
// 链式调用顺序：Recover → Validate → RateLimit → Trace → Metrics → Submit
// 每个 Stage 调用 next() 继续，不调用则短路后续 Stage。
// ─────────────────────────────────────────────────────────────────────
func BuildChain(limiter RateLimiter) pipeline.Chain[*UplinkCtx] {
	return pipeline.NewChain(
		RecoverStage(),
		ValidateStage(),
		RateLimitStage(limiter),
		TraceStage(),
		MetricsStage(),
		SubmitStage(),
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// Stage implementations
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────
// RecoverStage：捕获 panic，防止单条消息异常导致 readLoop goroutine 崩溃
//
// Connection 交互：panic 时通过 ctx.ReplyError → conn.Submit(5000)
// Session 交互：无
// ─────────────────────────────────────────────────────────────────────
func RecoverStage() pipeline.Stage[*UplinkCtx] {
	return func(ctx *UplinkCtx, next func()) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("uplink: stage panic",
					"conn", ctx.Conn.GetConnID(),
					"uid", ctx.Conn.GetUserID(),
					"mid", ctx.Message.MsgId,
					"panic", r,
				)
				// 直接通过 conn 回错，不经 Session（Session 状态可能已损坏）
				ctx.ReplyError(5000, "internal error")
				ctx.Abort("panic")
				// 注意：不调用 next()，pipeline 短路
			}
		}()
		next() // 先执行后续 stages，panic 在 defer 中捕获
	}
}

// ─────────────────────────────────────────────────────────────────────
// ValidateStage：基础消息合法性校验
//
// Connection 交互：校验失败 → conn.Submit(4001/4002) → writeCh
// Session 交互：无（校验在 Session 介入前完成）
//
// 校验项：
//  1. msg_id 不能为空（客户端 ACK 关联用）
//  2. 消息未过期（expire_at 字段）
//  3. 连接必须处于 Active 状态（防止 Handshaking 状态下的业务消息）
//
// ─────────────────────────────────────────────────────────────────────
func ValidateStage() pipeline.Stage[*UplinkCtx] {
	return func(ctx *UplinkCtx, next func()) {
		msg := ctx.Message

		if msg == nil {
			ctx.Abort("nil message")
			return // 不调 next()，短路
		}

		// 校验 msg_id：客户端必须提供，网关用它关联 ACK 和错误回复
		if msg.MsgId == "" {
			ctx.ReplyError(4001, "msg_id is required")
			ctx.Abort("missing msg_id")
			return
		}

		// 校验过期：expire_at > 0 且已超时则静默丢弃
		// 用绝对时间戳而非 TTL：多节点判断结果一致
		if IsExpired(msg.ExpireAt) {
			slog.Debug("uplink: drop expired message",
				"mid", msg.MsgId,
				"expire_at", msg.ExpireAt,
				"now", time.Now().UnixMilli(),
			)
			ctx.Abort("expired")
			return
		}

		// 校验连接状态：只有 Active 状态才处理业务消息
		// Handshaking 时的业务消息视为协议错误
		if !ctx.Conn.IsActive() {
			ctx.ReplyError(4010, "connection not active")
			ctx.Abort("not active")
			return
		}

		next() // 通过所有校验，继续下一个 stage
	}
}

func IsExpired(expireAt int64) bool {
	return expireAt > 0 && time.Now().UnixMilli() > expireAt
}

// ─────────────────────────────────────────────────────────────────────
// RateLimitStage：令牌桶限流
//
// Connection 交互：超限 → conn.Submit(4290) → writeCh
// Session 交互：从 ctx.Session.UserID() 取限流 key
//
// 限流维度：userID + bizCode（每用户每业务域独立限流）
// 实际实现：基于 Redis 的滑动窗口或令牌桶（limiter 接口隔离具体实现）
// ─────────────────────────────────────────────────────────────────────
func RateLimitStage(limiter RateLimiter) pipeline.Stage[*UplinkCtx] {
	return func(ctx *UplinkCtx, next func()) {
		if limiter == nil {
			next()
			return
		}

		// Session.UserID() 提供限流 key
		// 注意：此处只调用了 Session 的 UserID()，没有触发任何 Worker 操作
		if !limiter.Allow(ctx.Session.UserID(), ctx.Message.BizCode) {
			slog.Warn("uplink: rate limited",
				"uid", ctx.Session.UserID(),
				"biz", ctx.Message.BizCode,
				"mid", ctx.Message.MsgId,
			)
			// 通过 conn 直接回 429，不进入 Session 层
			ctx.ReplyError(4290, "rate limit exceeded")
			ctx.Abort("rate limited")
			return
		}

		next()
	}
}

// ─────────────────────────────────────────────────────────────────────
// TraceStage：注入/传播分布式追踪 ID
//
// Connection 交互：无
// Session 交互：无
// 职责：确保每条消息都有 TraceID，向下游（Kafka 消费者）传播
// ─────────────────────────────────────────────────────────────────────
func TraceStage() pipeline.Stage[*UplinkCtx] {
	return func(ctx *UplinkCtx, next func()) {
		msg := ctx.Message

		if msg.TraceId == "" {
			// 客户端没有携带 TraceID：网关生成一个
			// 生产环境：使用 otel trace.SpanFromContext 创建 span
			msg.TraceId = fmt.Sprintf("gw-%d-%s", time.Now().UnixNano(), msg.MsgId[:8])
		}

		// 把 TraceID 存入 BaseCtx，供后续 Stage 和 MetricsStage 使用
		ctx.TraceID = msg.TraceId

		next()
	}
}

// ─────────────────────────────────────────────────────────────────────
// MetricsStage：记录处理延迟和消息计数
//
// Connection 交互：无
// Session 交互：SessionID() 作为 metrics label
// 关键：包裹 next()，在所有后续 Stage 完成后才计算总延迟
// ─────────────────────────────────────────────────────────────────────
func MetricsStage() pipeline.Stage[*UplinkCtx] {
	return func(ctx *UplinkCtx, next func()) {
		start := time.Now()

		next() // ← 所有后续 stages（含 SubmitStage）在此执行

		elapsed := time.Since(start)

		// 生产环境：
		// metrics.UplinkLatency.WithLabelValues(ctx.Message.BizCode).
		//     Observe(elapsed.Seconds())
		// metrics.InboundMsgTotal.WithLabelValues(
		//     ctx.Message.BizCode,
		//     strconv.Itoa(int(ctx.Message.SignalType))).Inc()

		slog.Debug("uplink: pipeline done",
			"sid", ctx.Session.SessionID(),
			"uid", ctx.Session.UserID(),
			"biz", ctx.Message.BizCode,
			"mid", ctx.Message.MsgId,
			"trace", ctx.TraceID,
			"latency_us", elapsed.Microseconds(),
			"aborted", ctx.Aborted,
		)
	}
}

// ─────────────────────────────────────────────────────────────────────
// SubmitStage：★ 核心交互 Stage
//
// 这是 Stage 与 Session 和 Worker 交互最密集的地方。
//
// 交互链：
//
//	SubmitStage
//	  → 消息元数据注入（连接维度信息写入 Headers）
//	  → sess.SubmitUpstream(msg)        ← Session 接口调用
//	      → mgr.SubmitUpstream(biz, sess, msg)  ← Worker 接口调用
//	          → pool.upstreamCh <- UpstreamJob   ← channel 投递
//	              → upstreamWorker → sender.Send  ← 异步执行
//	                  → on fail: sess.Submit(errMsg) ← Worker→Session 回调
//	                      → conn.Submit(errMsg)       ← Session→Connection
//
// Connection 交互：消息注入（写 Headers），失败时 conn.Submit(5001)
// Session 交互：sess.SubmitUpstream(msg)，这是上行的核心调用点
// ─────────────────────────────────────────────────────────────────────
func SubmitStage() pipeline.Stage[*UplinkCtx] {
	return func(ctx *UplinkCtx, next func()) {
		msg := ctx.Message

		// ① 注入网关维度的元数据（从 Connection 读取，写入消息 Headers）
		// 业务后端消费时可通过 Headers 获取这些信息
		if msg.Headers == nil {
			msg.Headers = make(map[string]string)
		}
		msg.Headers["x-conn-id"] = ctx.Conn.GetConnID()
		msg.Headers["x-device-type"] = ctx.Conn.GetDeviceType()
		msg.Headers["x-gw-recv-ts"] = strconv.FormatInt(ctx.ReceivedAt.UnixMilli(), 10)
		msg.Headers["x-trace-id"] = ctx.TraceID

		// ② 填充 From 字段（从 Connection.UserID 读取）
		// 格式："{bizCode}:{userID}"，业务后端用于识别发送方
		if msg.From == "" {
			msg.From = msg.BizCode + ":" + ctx.Conn.GetUserID()
		}

		// ③ 核心调用：将消息转交给 Session，Session 再路由到 Worker
		//
		// sess.SubmitUpstream 内部：
		//   biz = msg.BizCode || sess.BizCode
		//   return mgr.SubmitUpstream(biz, sess, msg)
		//
		// mgr.SubmitUpstream 内部：
		//   pool = pools[biz]
		//   select pool.upstreamCh <- UpstreamJob{Sess: sess, Msg: msg}
		//   default: return ErrPoolFull
		err := ctx.Session.SubmitUpstream(msg)

		if err != nil {
			// upstreamCh 满（ErrPoolFull）或业务域不存在
			// → 通知客户端消息未能投递（不重试，让 SDK 决策）
			slog.Error("uplink: submit failed",
				"mid", msg.MsgId,
				"biz", msg.BizCode,
				"err", err,
				"trace", ctx.TraceID,
			)
			// 通过 conn 直接回错（不经 Session，避免额外的 QoS 处理）
			ctx.ReplyError(5001, "upstream unavailable: "+err.Error())
			ctx.Abort("submit failed")
			return
		}

		// ④ 提交成功后继续执行后续 Stage（MetricsStage 的 next() 返回点）
		// 此时消息已进入 upstreamCh，后续处理由 upstreamWorker 异步完成
		next()
	}
}

// ─────────────────────────────────────────────────────────────────────
// ChainAdapter：将 pipeline.Chain 适配为 handler.UplinkChain 接口
//
// Handler 层期望的接口：
//
//	type UplinkChain interface {
//	    Run(sess Session, conn *Connection, msg *Message)
//	}
//
// UpstreamHandler.Handle 调用 chain.Run(sess, conn, msg)，
// ChainAdapter 把三个参数组装成 UplinkCtx，然后运行 pipeline。
// ─────────────────────────────────────────────────────────────────────
type ChainAdapter struct {
	chain pipeline.Chain[*UplinkCtx]
}

func NewChainAdapter(chain pipeline.Chain[*UplinkCtx]) *ChainAdapter {
	return &ChainAdapter{chain: chain}
}

// Run 是 UpstreamHandler 调用的方法。
// 这里完成了 Handler 层 → Pipeline 层 → Stage 的接口适配。
func (a *ChainAdapter) Run(sess types.UplinkSession, conn types.ConnSubmitter, msg *gateway.Message) {
	ctx := &UplinkCtx{
		BaseCtx: pipeline.BaseCtx{
			ReceivedAt: time.Now(),
			Values:     make(map[string]any),
		},
		Session: sess, // Session 接口：stages 通过它调用 SubmitUpstream
		Conn:    conn, // Connection 指针：stages 通过它调用 Submit(errMsg)
		Message: msg,
	}
	a.chain.Run(ctx)
}
