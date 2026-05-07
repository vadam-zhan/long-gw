package session

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/gen/gateway/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
)

/*
// Package session implements the logical session layer.
//
// ── Why Session exists ────────────────────────────────────────────────────────
//
// Connection is the physical TCP socket. When a TCP connection breaks:
//   - Connection is gone
//   - But the user is still "online" — they will reconnect in milliseconds
//   - Their subscriptions (rooms, topics) should persist
//   - Their unACK'd messages (QoS-1) should be retried on the new connection
//   - Their delivery sequence should continue, not reset
//
// Session is the stable identity that survives connection churn.
//
//   Connection  1──►  Session  ◄──N  WorkerPool (delivers to Session.Submit)
//                        │
//                    LocalRouter (indexes []Session, not []Connection)
//
// ── State machine ─────────────────────────────────────────────────────────────
//
//	Authenticating ──►  Active ──►  Suspended ──►  Active (reconnect)
//	                       │               │
//	                       └───────────────┴──► Closed (explicit logout or max suspend time)
//
//	Authenticating: TCP connected, waiting for Auth handshake.
//	Active:         Authenticated, conn != nil, messages flowing.
//	Suspended:      conn == nil (network drop), subscriptions preserved.
//	                Submit() stores to OfflineStore if msg.Offline=true.
//	Closed:         Session removed from all indexes; resources freed.
//
// ── Coordination responsibilities ────────────────────────────────────────────
//
//	Upstream:   Session.SubmitUpstream(msg) → WorkerManager.SubmitUpstream(biz, session, msg)
//	Downstream: WorkerPool.downstreamWorker → router.Resolve(To) → []Session → session.Submit(msg)
//	Reconnect:  Factory calls session.AttachConn(newConn), which re-subscribes and replays
//	QoS-1:      delivery/ack Tracker 负责重试调度 (PR-2 接入)
*/

// State encodes the session lifecycle phase.
type State uint32

const (
	StateAuthenticating State = iota
	StateActive
	StateSuspended
	StateClosed
)

// subscriptionSet：跨重连持久的订阅状态
// ─────────────────────────────────────────────────────────────────────
type subscriptionSet struct {
	mu     sync.RWMutex
	rooms  map[string]struct{}
	topics map[string]struct{}
}

func newSubscriptionSet() subscriptionSet {
	return subscriptionSet{
		rooms:  make(map[string]struct{}),
		topics: make(map[string]struct{}),
	}
}

func (s *subscriptionSet) addRoom(r string)     { s.mu.Lock(); s.rooms[r] = struct{}{}; s.mu.Unlock() }
func (s *subscriptionSet) removeRoom(r string)  { s.mu.Lock(); delete(s.rooms, r); s.mu.Unlock() }
func (s *subscriptionSet) addTopic(t string)    { s.mu.Lock(); s.topics[t] = struct{}{}; s.mu.Unlock() }
func (s *subscriptionSet) removeTopic(t string) { s.mu.Lock(); delete(s.topics, t); s.mu.Unlock() }

func (s *subscriptionSet) snapshot() (rooms, topics []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for r := range s.rooms {
		rooms = append(rooms, r)
	}
	for t := range s.topics {
		topics = append(topics, t)
	}
	return
}

type Session struct {
	// 稳定身份 (跨重连不变)
	sessionID  string // sha256(userID+":"+deviceID)[:16] (32 hex chars)
	AppID      string // 应用ID
	userID     string // 用户ID - token 中
	deviceID   string // 设备ID
	DeviceType string // 设备类型：ios、android、web
	BizCode    string // primary business domain ("im", "push", "live")

	// ★ 物理连接（每次重连替换）
	// conn 是 Session 与 Connection 的唯一连接点。
	// 所有下行消息最终通过 conn.Submit → writeCh 写出。
	connMu sync.RWMutex
	conn   types.ConnSubmitter // nil 表示 Suspended 状态

	// 订阅状态（跨重连持久）
	// AttachConn 时恢复到 LocalRouter
	subs subscriptionSet

	// 序号追踪 (离线重放)
	lastDeliveredSeq atomic.Uint64

	// 状态机
	state       atomic.Uint32
	suspendedAt atomic.Int64

	// QoS-1 待确认消息（仅被动存储，定时逻辑由 Registry 的 Scanner 集中处理）
	pendingAcks sync.Map // msgID string -> *pendingEntry

	deps Deps

	// TODO(PR-5): connCount 应在 Factory 中维护，而非 Session。
	// 当前仅用于 admin stats，AttachConn/DetachConn 中未更新。
	connCount uint
	countMux  sync.Mutex

	once sync.Once
}

// pendingEntry：QoS-1 消息投递状态（Session 包私有，避免对外暴露）
type pendingEntry struct {
	msg         *gateway.Message
	retryCount  int
	firstSentAt time.Time
	lastRetryAt time.Time
}

func NewSession(sessionID, userID, deviceID, appID, deviceType, bizCode string, deps Deps) *Session {
	sess := &Session{
		sessionID:  sessionID,
		AppID:      appID,
		userID:     userID,
		deviceID:   deviceID,
		DeviceType: deviceType,
		BizCode:    bizCode,
		subs:       newSubscriptionSet(),
		deps:       deps,
	}
	sess.state.Store(uint32(StateAuthenticating))

	return sess
}

// ═══════════════════════════════════════════════════════════════════════
// 交互点③-A：Session → Connection 的连接绑定（重连路径）
//
// AttachConn 由 Factory.CreateAndRun 在以下两种情况调用：
//  1. 首次连接：Auth 握手成功后
//  2. 重连：相同 sessionID 的客户端重新连接
//
// 执行流程：
//  1. 替换物理连接（原子操作：connMu 保护）
//  2. 状态 → Active，清除 suspendedAt
//  3. 恢复订阅到 LocalRouter（JoinRoom/Subscribe）
//  4. PR-2: 由 delivery/ack Tracker 负责重发未确认消息
//
// 返回值 lastSeq：Factory 用它触发离线消息重放
//
//	Factory 收到 lastSeq 后，从 OfflineStore 拉取 lastSeq+1 以后的消息
//	并重新通过 downlink 投递
//
// ═══════════════════════════════════════════════════════════════════════
func (s *Session) AttachConn(conn types.ConnSubmitter) (lastSeq uint64) {
	s.connMu.Lock()
	oldConn := s.conn
	s.conn = conn
	s.connMu.Unlock()

	_ = oldConn // 旧连接已由 Factory 关闭，此处无需再操作

	s.state.Store(uint32(StateActive))
	s.suspendedAt.Store(0)

	// ① 恢复订阅到 LocalRouter
	// 订阅状态在 Suspended 期间保留在 s.subs 中，重连后重新注册
	rooms, topics := s.subs.snapshot()
	for _, r := range rooms {
		s.deps.LocalRouter.JoinRoom(r, s)
	}
	for _, t := range topics {
		s.deps.LocalRouter.Subscribe(t, s)
	}

	// ② 重连后重发所有未确认的 QoS-1 消息
	// 由 Session 自己 flush，不依赖外部，避免 import delivery/ack
	var flushed int
	s.pendingAcks.Range(func(k, v any) bool {
		entry := v.(*pendingEntry)
		entry.retryCount++
		entry.lastRetryAt = time.Now()

		if entry.msg.Headers == nil {
			entry.msg.Headers = make(map[string]string)
		}
		entry.msg.Headers["x-retry-count"] = strconv.Itoa(entry.retryCount)

		conn.Submit(entry.msg)
		flushed++

		// 重新注册 ACK 超时 timer
		if s.deps.Acker != nil {
			s.deps.Acker.Track(entry.msg, s)
		}
		return true
	})
	if flushed > 0 {
		slog.Info("session: flushed pending acks", "sid", s.sessionID, "count", flushed)
	}

	return s.lastDeliveredSeq.Load()
}

// ═══════════════════════════════════════════════════════════════════════
// 交互点③-B：Session ← Connection 的断线通知（断线路径）
//
// DetachConn 由 Factory.onClose 调用（Connection.Run 的 defer）。
// 此时 Connection 已关闭，conn.Submit 已无法使用。
//
// 执行流程：
//  1. 置 conn = nil（Suspended 状态的标志）
//  2. 从 LocalRouter 清理 room/topic 索引
//		（注意：subscriptionSet 中的记录保留，用于重连时恢复）
//  3. 状态 → Suspended，记录 suspendedAt
//
// Suspended 状态下：
//   - Submit(msg) 会走 handleUndelivered：QoS-1 消息存 OfflineStore
//   - PR-3: suspendWatchdog 由 timer 统一调度，不再 per-session goroutine
//
// ═══════════════════════════════════════════════════════════════════════
func (s *Session) DetachConn() {
	s.connMu.Lock()
	old := s.conn
	s.conn = nil
	s.connMu.Unlock()

	if old != nil {
		// 从 LocalRouter 的 room/topic 索引中移除当前连接
		// 注意：s.subs 中的记录不删除，重连时恢复用
		s.deps.LocalRouter.UnregisterAll(s)
	}

	s.state.Store(uint32(StateSuspended))
	s.suspendedAt.Store(time.Now().UnixMilli())

	// TODO(PR-3): 接入 Timer 做 Suspend TTL 超时检测
	// if s.deps.Timer != nil && s.deps.SuspendTTL > 0 {
	//     s.deps.Timer.Schedule(TaskSessionSuspend, ...)
	// }

	slog.Info("session: suspended",
		"sid", s.sessionID,
		"uid", s.UserID,
	)
}

// ═══════════════════════════════════════════════════════════════════════
// 交互点③-C：Session → Worker（上行路径）
//
// SubmitUpstream 是上行链路中 Session 与 Worker 的交互点。
// 调用方：SubmitStage
// 被调方：WorkerManager.SubmitUpstream
//
// 数据流：
//
//	SubmitStage.ctx.Session.SubmitUpstream(msg)
//	  → s.deps.Worker.SubmitUpstream(biz, s, msg)
//	    → manager.getPool(biz).upstreamCh <- UpstreamJob{Sess: s, Msg: msg}
//	      → upstreamWorker.Send(msg) → Kafka
//
// Worker 持有 sess 引用（UpstreamJob.Sess）的目的：
//
//	当 Kafka 发送失败时，upstreamWorker 调用 sess.Submit(errMsg)，
//	通过 Session 把错误路由回客户端（Session → conn → writeCh）
//
// ═══════════════════════════════════════════════════════════════════════
func (s *Session) SubmitUpstream(msg *gateway.Message) error {
	biz := msg.BizCode
	if biz == "" {
		biz = s.BizCode // 消息没有指定 bizCode，使用 Session 的默认业务域
	}
	if biz == "" {
		return fmt.Errorf("session: empty biz_code")
	}
	// 委托给 WorkerManager，WorkerManager 按 bizCode 路由到对应 WorkerPool
	return s.deps.Worker.SubmitUpstream(biz, s, msg)
}

// ═══════════════════════════════════════════════════════════════════════
// 交互点③-D：Session → Connection（下行路径）
//
// Submit 是下行链路中 Session 与 Connection 的交互点。
// 调用方：
//   - downlink.FanOutStage：sess.Submit(msg)
//   - Worker.upstreamWorker：job.Sess.Submit(errMsg) [Kafka 失败时]
//
// 数据流（成功路径）：
//
//	sess.Submit(msg)
//	  → conn = s.conn（读取当前物理连接）
//	  → conn.Submit(msg)（非阻塞）
//	    → writeCh <- msg（成功）
//	      → Connection.writeLoop → Encode → tp.Write → TCP
//
// 数据流（失败路径）：
//
//	① conn == nil（Suspended）：走 handleUndelivered
//	② conn.Submit 返回 false（writeCh 满）：走 handleUndelivered
//	handleUndelivered：
//	  - QoS-0：丢弃
//	  - QoS-1（msg.Offline=true）：OfflineStore.Store
//
// QoS-1 追踪：
//
//	成功投递后，PR-2 将由 delivery/ack Tracker 接管 pending ACK 管理。
//	等待客户端发送 MessageAck → AckHandler.Handle → sess.Ack(msgID)。
//
// ═══════════════════════════════════════════════════════════════════════
func (s *Session) Submit(msg *gateway.Message) bool {
	// ① 读取当前物理连接（RLock 保证与 AttachConn/DetachConn 的并发安全）
	s.connMu.RLock()
	conn := s.conn
	s.connMu.RUnlock()

	// ② 连接不可用（Suspended 或 Closed）
	if conn == nil || !conn.IsActive() {
		s.handleUndelivered(msg)
		return false
	}

	// ③ 投递到 Connection.writeCh（非阻塞）
	ok := conn.Submit(msg)

	if ok {
		// 投递成功
		s.lastDeliveredSeq.Store(msg.SeqId)

		// QoS-1 追踪：记录 pending ack，由 Scanner 集中调度超时重试
		if s.deps.Acker != nil && msg.Delivery != nil && msg.Delivery.Qos == gateway.QosClass_AT_LEAST_ONCE {
			s.pendingAcks.Store(msg.MsgId, &pendingEntry{
				msg:         msg,
				firstSentAt: time.Now(),
				lastRetryAt: time.Now(),
			})
			s.deps.Acker.Track(msg, s)
		}
	} else {
		// 投递失败（writeCh 满，背压）
		s.handleUndelivered(msg)
	}

	return ok
}

// ═══════════════════════════════════════════════════════════════════════
// 交互点③-E：AckHandler → Session（ACK 处理路径）
//
// 客户端收到消息后发送 MessageAck。
// 调用链：Connection.readLoop → HandlerRegistry → AckHandler.Handle → sess.Ack(msgID)
//
// PR-2: Ack 将委托给 delivery/ack Tracker 取消超时任务。
// ═══════════════════════════════════════════════════════════════════════
func (s *Session) Ack(msgID string) {
	s.pendingAcks.Delete(msgID)
	if s.deps.Acker != nil {
		s.deps.Acker.Done(msgID, s.sessionID)
	}
}

// ─────────────────────────────────────────────────────────────────────
// 订阅管理：Handler 层 → Session → LocalRouter
//
// 调用链：SubscribeHandler.Handle(sess, conn, msg) → sess.JoinRoom(roomID)
//
//	→ subs.addRoom(roomID)（持久化到 subscriptionSet）
//	→ localRouter.JoinRoom(roomID, s)（注册到路由索引）
//
// ─────────────────────────────────────────────────────────────────────
func (s *Session) JoinRoom(roomID string) {
	s.subs.addRoom(roomID) // 持久化，重连后可恢复
	s.withConn(func(_ types.ConnSubmitter) {
		s.deps.LocalRouter.JoinRoom(roomID, s)
	})
}

func (s *Session) LeaveRoom(roomID string) {
	s.subs.removeRoom(roomID)
	s.withConn(func(_ types.ConnSubmitter) {
		s.deps.LocalRouter.LeaveRoom(roomID, s)
	})
}

func (s *Session) Subscribe(topic string) {
	s.subs.addTopic(topic)
	s.withConn(func(_ types.ConnSubmitter) {
		s.deps.LocalRouter.Subscribe(topic, s)
	})
}

func (s *Session) Unsubscribe(topic string) {
	s.subs.removeTopic(topic)
	s.withConn(func(_ types.ConnSubmitter) {
		s.deps.LocalRouter.Unsubscribe(topic, s)
	})
}

// withConn 在持有 RLock 的情况下执行 fn。
// 只在 Active 状态（conn != nil）时执行，Suspended 时跳过。
func (s *Session) withConn(fn func(types.ConnSubmitter)) {
	s.connMu.RLock()
	c := s.conn
	s.connMu.RUnlock()
	if c != nil {
		fn(c)
	}
}

func (s *Session) GetLocalRouter() types.LocalRouterOps {
	return s.deps.LocalRouter
}

// Close 永久关闭 Session（登出或 Suspend TTL 超时）。
func (s *Session) Close(kick *gateway.KickRequest) {
	s.once.Do(func() {
		s.state.Store(uint32(StateClosed))

		// TODO(PR-3): 取消可能存在的 suspend timer
		// if s.deps.Timer != nil {
		//     s.deps.Timer.Cancel("suspend:" + s.sessionID)
		// }

		s.connMu.RLock()
		conn := s.conn
		s.connMu.RUnlock()

		if conn != nil {
			conn.Close(kick) // 通知客户端并关闭 TCP 连接
			s.deps.LocalRouter.UnregisterAll(s)
		}
		s.deps.LocalRouter.UnregisterSession(s.userID, s.deviceID)

		// 清理 ACK Scanner 中的 pending timer
		if s.deps.Acker != nil {
			s.deps.Acker.CancelAll(s.sessionID)
		}
		// 清空本地 pendingAcks（Session 即将被 GC）
		s.pendingAcks = sync.Map{}

		slog.Info("session: closed", "sid", s.sessionID, "uid", s.UserID)
	})
}

func (s *Session) SessionID() string { return s.sessionID }
func (s *Session) UserID() string    { return s.userID }
func (s *Session) DeviceID() string  { return s.deviceID }
func (s *Session) State() State      { return State(s.state.Load()) }
func (s *Session) IsActive() bool    { return s.State() == StateActive }

// GetConnCount returns current connection count.
// TODO(PR-5): 当前未在 AttachConn/DetachConn 中维护，始终返回 0。
// 应在 Factory 层维护或正确更新。
func (s *Session) GetConnCount() uint {
	s.countMux.Lock()
	defer s.countMux.Unlock()
	return s.connCount
}

// PendingAckSnapshot 返回当前所有待确认消息的副本。
// 由 Registry 的 Scanner 回调在重试调度时读取。
func (s *Session) PendingAckSnapshot() map[string]*pendingEntry {
	result := make(map[string]*pendingEntry)
	s.pendingAcks.Range(func(k, v any) bool {
		result[k.(string)] = v.(*pendingEntry)
		return true
	})
	return result
}

// GetPendingAck 获取指定 msgID 的 pending entry。
func (s *Session) GetPendingAck(msgID string) (*pendingEntry, bool) {
	if v, ok := s.pendingAcks.Load(msgID); ok {
		return v.(*pendingEntry), true
	}
	return nil, false
}

// RemovePendingAck 删除指定 msgID 的 pending entry。
func (s *Session) RemovePendingAck(msgID string) {
	s.pendingAcks.Delete(msgID)
}

func (s *Session) pendingAckCount() int {
	n := 0
	s.pendingAcks.Range(func(_, _ any) bool { n++; return true })
	return n
}

// handleUndelivered 处理无法即时投递的消息（Suspended 或 writeCh 满）。
func (s *Session) handleUndelivered(msg *gateway.Message) {
	if !msg.Delivery.Offline || s.deps.Offline == nil {
		return // QoS-0 或未配置 OfflineStore：直接丢弃
	}
	if msg.Delivery.Qos == gateway.QosClass_AT_MOST_ONCE {
		return // QoS-0：不存离线
	}
	if err := s.deps.Offline.Store(context.Background(), msg); err != nil {
		slog.Error("session: offline store failed",
			"mid", msg.MsgId,
			"to", msg.To,
			"err", err,
		)
	}
}
