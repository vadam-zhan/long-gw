package downlink

import (
	"context"
	"log/slog"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/pipeline"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// File: gateway/internal/pipeline/downlink/stages.go
//
// ═══════════════════════════════════════════════════════════════════════
// 交互点五：DownlinkChain stages 与 Session / Connection 的交互
//
// 下行 Pipeline 的 Context 不持有 Connection 的直接引用。
// stages 通过 Session 接口间接操作 Connection。
//
//   DownlinkCtx {
//       Message        *proto.Message
//       TargetSessions []SessionTarget   ← RouteStage 填充
//       FanOutResult   FanOutResult      ← FanOutStage 填充
//   }
//
// 各 Stage 与 Session/Connection 的交互：
//
//   ValidateStage  → 无交互，只做消息检测
//   RouteStage     → resolver.Resolve(To) → LocalRouter → []Session
//   FilterStage    → sess.IsActive()、sess.SessionID() 过滤
//   FanOutStage    → sess.Submit(msg) → conn.Submit → writeCh → TCP
//   MetricsStage   → 无交互，只记录指标
//
// 注意：FanOutStage 调用的是 SessionTarget.Submit（接口），
// 内部实际调用 Session.Submit → conn.Submit → conn.writeCh。
// Connection 对 stages 完全不可见，由 Session 封装。
//
// ═══════════════════════════════════════════════════════════════════════

// ─────────────────────────────────────────────────────────────────────
// SessionTarget：downlink stages 对 Session 的最小接口
//
// Stage 只需要：
//   - Submit：投递消息（内部调 conn.Submit → writeCh）
//   - IsActive：过滤非活跃 Session
//   - SessionID：用于日志和回音消除
//   - UserID：用于日志
//
// ─────────────────────────────────────────────────────────────────────

// FanOutResult 汇总扇出投递的结果
type FanOutResult struct {
	Total   int // 目标 Session 总数
	Sent    int // 成功投递数（conn.Submit 返回 true）
	Dropped int // 丢弃数（writeCh 满，back-pressure）
	Offline int // 无活跃 Session（全部 Suspended/Closed）
}

// Deps 打包下行 Pipeline 的依赖
type Deps struct {
	Resolver     types.SessionResolver
	OfflineStore types.OfflineStore
}

// ─────────────────────────────────────────────────────────────────────
// DownlinkCtx：下行 Pipeline 的上下文
//
// 注意：不持有 *Connection 引用。
// Connection 隐藏在 SessionTarget.Submit 的实现里。
// ─────────────────────────────────────────────────────────────────────
type DownlinkCtx struct {
	pipeline.BaseCtx
	Message *gateway.Message

	// 目标会话由路由阶段进行填充，分发阶段向这些目标会话推送数据。
	TargetSessions []types.SessionTarget // RouteStage 填充
	FanOutResult   FanOutResult          // FanOutStage 填充
}

// BuildChain 组装下行 Pipeline
func BuildChain(deps Deps) pipeline.Chain[*DownlinkCtx] {
	return pipeline.NewChain(
		RecoverStage(),
		ValidateStage(),
		RouteStage(deps.Resolver),
		FilterStage(),
		FanOutStage(deps.OfflineStore),
		MetricsStage(),
	)
}

// ─────────────────────────────────────────────────────────────────────
// RecoverStage：捕获 consumer goroutine 中的 panic
//
// Connection 交互：无（下行 pipeline 没有直接操作 Connection 的路径）
// Session 交互：无
// ─────────────────────────────────────────────────────────────────────
func RecoverStage() pipeline.Stage[*DownlinkCtx] {
	return func(ctx *DownlinkCtx, next func()) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("downlink: panic",
					"trace", ctx.TraceID,
					"mid", safeGetMsgID(ctx.Message),
					"panic", r,
				)
				ctx.Abort("panic")
			}
		}()
		next()
	}
}

func safeGetMsgID(msg *gateway.Message) string {
	if msg == nil {
		return "<nil>"
	}
	return msg.MsgId
}

// TODO 大量待补充
// ─────────────────────────────────────────────────────────────────────
// ValidateStage：消息合法性检测
//
// Connection 交互：无（下行没有"回错给发送方"的路径）
// Session 交互：无
//
// 关键场景：Kafka Consumer 消费延迟导致消息已过期。
// 过期消息在此静默丢弃，不进入后续 Stage。
// ─────────────────────────────────────────────────────────────────────
func ValidateStage() pipeline.Stage[*DownlinkCtx] {
	return func(ctx *DownlinkCtx, next func()) {
		msg := ctx.Message
		if msg == nil {
			ctx.Abort("nil message")
			return
		}
		if msg.To == "" {
			slog.Warn("downlink: empty To", "mid", msg.MsgId)
			ctx.Abort("empty To")
			return
		}
		if IsExpired(msg.ExpireAt) {
			// 消息过期：记录延迟信息（Kafka Consumer lag 的体现）
			lag := time.Now().UnixMilli() - msg.ExpireAt
			slog.Debug("downlink: drop expired",
				"mid", msg.MsgId,
				"expire_at", msg.ExpireAt,
				"lag_ms", lag,
				"biz", msg.BizCode,
			)
			ctx.Abort("expired")
			return
		}
		ctx.TraceID = msg.TraceId
		next()
	}
}

func IsExpired(expireAt int64) bool {
	return expireAt > 0 && time.Now().UnixMilli() > expireAt
}

// ─────────────────────────────────────────────────────────────────────
// RouteStage：★ 地址解析，与 LocalRouter（通过 SessionResolver 接口）交互
//
// 这是下行 Pipeline 中唯一与 LocalRouter 交互的 Stage。
//
// 解析规则：
//
//	msg.To = "u:{uid}"    → LocalRouter.GetUserSessions(uid)
//	msg.To = "r:{roomID}" → LocalRouter.RoomMembers(roomID)
//	msg.To = "t:{topic}"  → LocalRouter.TopicSubscribers(topic)
//	msg.To = "g:{groupID}"→ LocalRouter.TopicSubscribers("g:"+groupID)
//
// 解析结果存入 ctx.TargetSessions，供 FilterStage 和 FanOutStage 使用。
//
// 无本地 Session 时：
//
//	ctx.FanOutResult.Offline = 1（标记离线）
//	ctx.TargetSessions = nil
//	继续执行（FanOutStage 处理离线存储）
//
// ★ 跨节点路由：
//
//	当前只实现本地节点路由。
//	跨节点场景：DistributedRouter.Lookup(uid) → 找到远端节点地址
//	  → gRPC GatewayInternal.PushToConn(connID, msg) → 远端节点投递
//
// ─────────────────────────────────────────────────────────────────────
func RouteStage(resolver types.SessionResolver) pipeline.Stage[*DownlinkCtx] {
	return func(ctx *DownlinkCtx, next func()) {
		msg := ctx.Message

		// resolver 是 LocalRouter，Resolve 内部根据 To 字段的 scheme 分发：
		//   "u:" → GetUserSessions → userSessions[uid] map
		//   "r:" → RoomMembers → rooms[roomID] map
		//   "t:" → TopicSubscribers → topics[topic] map
		sessions, ok := resolver.Resolve(msg.To)

		if !ok {
			// 本地节点没有该地址的活跃 Session
			// 可能是：用户 Suspended、用户在其他节点、用户已下线
			slog.Debug("downlink: no local sessions",
				"to", msg.To,
				"mid", msg.MsgId,
				"biz", msg.BizCode,
			)
			ctx.FanOutResult.Offline = 1
			ctx.TargetSessions = nil
			// 不 abort：让 FanOutStage 决定是否离线存储
			next()
			return
		}

		ctx.TargetSessions = sessions
		next()
	}
}

// ─────────────────────────────────────────────────────────────────────
// FilterStage：过滤不应接收消息的 Session
//
// Session 交互：
//   - sess.IsActive()：过滤非 Active 状态（Suspended/Closed）
//   - sess.SessionID()：回音消除（发送方不收到自己的广播）
//
// 回音消除（x-exclude-conn header）：
//
//	IM 群消息时，发送者本人不需要收到自己发的消息（客户端本地已显示）。
//	发送方在发布到 Kafka 时，在 Headers 中写入：
//	  "x-exclude-conn": "<自己的 sessionID 或 connID>"
//	FilterStage 读取此 header，跳过对应的 Session。
//
// ─────────────────────────────────────────────────────────────────────
func FilterStage() pipeline.Stage[*DownlinkCtx] {
	return func(ctx *DownlinkCtx, next func()) {
		if len(ctx.TargetSessions) == 0 {
			next()
			return
		}

		excludeID := ctx.Message.Headers["x-exclude-conn"]

		// 复用 slice 底层数组，减少内存分配
		filtered := ctx.TargetSessions[:0]
		for _, sess := range ctx.TargetSessions {
			// 过滤条件①：Session 必须处于 Active 状态
			if !sess.IsActive() {
				continue
			}
			// 过滤条件②：回音消除（发送方不收到自己的消息）
			if excludeID != "" && sess.SessionID() == excludeID {
				continue
			}
			filtered = append(filtered, sess)
		}

		ctx.TargetSessions = filtered
		next()
	}
}

// ─────────────────────────────────────────────────────────────────────
// FanOutStage：★ 核心投递 Stage，与 Session 最密集的交互点
//
// 两种场景：
//
// 场景A：正常投递（TargetSessions 非空）
//
//	for sess in TargetSessions:
//	    ok = sess.Submit(msg)        ← Session.Submit → conn.Submit → writeCh
//	    if ok: result.Sent++
//	    if !ok（writeCh 满）:
//	        result.Dropped++
//	        if msg.Offline: store.Store(msg)  ← 离线存储
//
// 场景B：全部离线（RouteStage 设置了 Offline=1）
//
//	if msg.Offline && msg.QoS >= QoSAtLeastOnce:
//	    store.Store(msg)             ← 等待客户端重连后拉取
//
// 背压传导完整链路：
//
//	Kafka 消费 → downstreamCh → downstreamWorker → FanOutStage
//	→ sess.Submit → conn.Submit → writeCh（满）
//	→ Session.handleUndelivered → OfflineStore.Store
//	（同时：writeCh 满意味着客户端读取慢，writeLoop 会阻塞，
//	  直到客户端 TCP 窗口腾出空间，形成自然背压）
//
// ★ 非阻塞设计：
//
//	sess.Submit 是非阻塞的（conn.Submit 使用 select + default）。
//	一个慢客户端（writeCh 满）不会阻塞其他客户端的投递。
//	这是大规模直播弹幕场景的关键设计——万人房间中一个慢客户端
//	的问题不会影响其他 999,999 个客户端。
//
// ─────────────────────────────────────────────────────────────────────
func FanOutStage(store types.OfflineStore) pipeline.Stage[*DownlinkCtx] {
	return func(ctx *DownlinkCtx, next func()) {
		msg := ctx.Message

		ctx1 := context.Background()

		// 场景B：全部离线（RouteStage 找不到本地 Session）
		if ctx.FanOutResult.Offline > 0 {
			if msg.Offline && msg.Qos >= gateway.QoS_AT_LEAST_ONCE && store != nil {
				if err := store.Store(ctx1, msg); err != nil {
					slog.Error("downlink: offline store failed",
						"mid", msg.MsgId,
						"to", msg.To,
						"err", err,
					)
				}
			}
			next()
			return
		}

		// 场景A：扇出投递
		result := FanOutResult{Total: len(ctx.TargetSessions)}

		for _, sess := range ctx.TargetSessions {
			// ★ sess.Submit 是 Session.Submit 的调用
			// 内部路径：Session.Submit → conn.Submit → writeCh <- msg
			// conn.Submit 使用 select default，不阻塞
			ok := sess.Submit(msg)

			if ok {
				result.Sent++
				// sess.Submit 内部已更新 lastDeliveredSeq
				// QoS-1 消息已加入 Session.pendingAcks
			} else {
				result.Dropped++
				slog.Debug("downlink: writeCh full, drop or offline",
					"sid", sess.SessionID(),
					"uid", sess.UserID(),
					"mid", msg.MsgId,
					"biz", msg.BizCode,
				)
				// writeCh 满：离线存储（仅限 QoS-1+ 且配置了 OfflineStore）
				// 注意：只存前几个 drop，避免一个慢客户端的问题导致 OfflineStore 风暴
				if msg.Offline && store != nil && result.Dropped <= 3 {
					_ = store.Store(ctx1, msg)
				}
			}
		}

		ctx.FanOutResult = result
		next()
	}
}

// ─────────────────────────────────────────────────────────────────────
// MetricsStage：记录下行投递指标
//
// Connection 交互：无
// Session 交互：无（只读 ctx.FanOutResult）
// ─────────────────────────────────────────────────────────────────────
func MetricsStage() pipeline.Stage[*DownlinkCtx] {
	return func(ctx *DownlinkCtx, next func()) {
		next() // 先执行后续（但 MetricsStage 是最后一个，next() 是空操作）
		r := ctx.FanOutResult

		if r.Total > 0 || r.Offline > 0 {
			slog.Debug("downlink: fanout complete",
				"to", ctx.Message.To,
				"mid", ctx.Message.MsgId,
				"biz", ctx.Message.BizCode,
				"total", r.Total,
				"sent", r.Sent,
				"dropped", r.Dropped,
				"offline", r.Offline,
			)
		}

		// 生产环境：
		// metrics.OutboundMsgTotal.WithLabelValues(msg.BizCode).Add(float64(r.Sent))
		// metrics.MsgDropTotal.WithLabelValues(msg.BizCode, "queue_full").
		//     Add(float64(r.Dropped))
		// metrics.MsgDropTotal.WithLabelValues(msg.BizCode, "offline").
		//     Add(float64(r.Offline))
	}
}

// ─────────────────────────────────────────────────────────────────────
// LocalRouter.Resolve 的实现（在 router 包，此处展示逻辑）
//
// RouteStage 调用 resolver.Resolve(to)，LocalRouter 实现了 SessionResolver。
//
// Resolve 的地址解析逻辑：
// ─────────────────────────────────────────────────────────────────────

// resolveLogic 展示 LocalRouter.Resolve 内部的地址解析逻辑（伪代码注释）
//
//   func (r *LocalRouter) Resolve(to string) ([]SessionTarget, bool) {
//       idx := strings.Index(to, ":")
//       scheme, id := to[:idx], to[idx+1:]
//
//       switch scheme {
//       case "u":
//           // 单用户：返回该用户所有设备的 Session
//           sessions := r.getUserSessions(id) // userSessions[id] map
//           // 多端：iOS、Android、Web 同时在线时各自收到
//           return toTargets(sessions), len(sessions) > 0
//
//       case "r":
//           // 房间广播：直播弹幕、群发通知
//           members := r.getRoomMembers(id) // rooms[id] map
//           return toTargets(members), len(members) > 0
//
//       case "t":
//           // Pub/Sub Topic：股票行情、系统公告
//           subs := r.getTopicSubs(id) // topics[id] map
//           return toTargets(subs), len(subs) > 0
//
//       case "g":
//           // 群组：作为特殊 topic "g:{groupID}" 处理
//           subs := r.getTopicSubs("g:" + id)
//           return toTargets(subs), len(subs) > 0
//       }
//   }
