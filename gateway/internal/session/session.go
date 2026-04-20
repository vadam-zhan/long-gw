package session

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/connection"
	"github.com/vadam-zhan/long-gw/gateway/internal/svc"
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
//	QoS-1:      Session.retryLoop() retransmits pendingAcks entries on a ticker
*/

// State encodes the session lifecycle phase.
type State uint32

const (
	StateAuthenticating State = iota
	StateActive
	StateSuspended
	StateClosed
)

type SessionDeps struct {
	// LocalRouter        *router.LocalRouter
	// DistributionRouter *router.DistributedRouter
	ConnectionFactory *connection.Factory
	OfflineStore      types.OfflineStore
	AckRetrier        AckRetrier // 全局重试器，per-gateway 而非 per-session
}

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
	ctx    context.Context
	cancel context.CancelFunc

	// 稳定身份 (跨重连不变)
	sessionID  string // sha256(userID+":"+deviceID)[:8]
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

	// QoS-1 重试队列
	// 存储已发送但未收到 ACK 的消息。
	// retryLoop 定期扫描并通过当前 conn 重发。
	pendingAcks sync.Map // msgID string → *pendingEntry

	// 订阅状态（跨重连持久）
	// AttachConn 时恢复到 LocalRouter
	subs subscriptionSet

	// 序号追踪 (离线重放)
	lastDeliveredSeq atomic.Uint64

	// 状态机
	state       atomic.Uint32
	suspendedAt atomic.Int64

	deps Deps

	connCount uint
	countMux  sync.Mutex

	svc *svc.ServiceContext

	once sync.Once
}

func NewSession(svc *svc.ServiceContext, userID, deviceID, appID, deviceType, bizCode string, deps Deps) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	sess := &Session{
		svc:        svc,
		AppID:      appID,
		userID:     userID,
		deviceID:   deviceID,
		DeviceType: deviceType,
		BizCode:    bizCode,
		subs:       newSubscriptionSet(),
		deps:       deps,
		ctx:        ctx,
		cancel:     cancel,
	}
	sess.state.Store(uint32(StateAuthenticating))
	go sess.retryLoop()       // QoS-1 重试后台任务
	go sess.suspendWatchdog() // Suspended 超时关闭后台任务

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
//  4. 将 pendingAcks 中所有 QoS-1 消息刷入新连接（重发未确认消息）
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

	// ② 刷入 pendingAcks：将所有未 ACK 的 QoS-1 消息发到新连接
	// 这确保了断线期间未确认的消息在重连后能继续投递
	var flushed int
	s.pendingAcks.Range(func(k, v any) bool {
		entry := v.(*pendingEntry)
		entry.retryCount++
		entry.lastRetryAt = time.Now()

		// 标记重试次数，让客户端和服务端都能检测到重传
		if entry.msg.Headers == nil {
			entry.msg.Headers = make(map[string]string)
		}
		entry.msg.Headers["x-retry-count"] = strconv.Itoa(entry.retryCount)

		// 直接调 conn.Submit（绕过 Session.Submit，避免重复加入 pendingAcks）
		conn.Submit(entry.msg)
		flushed++
		return true
	})

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
//     （注意：subscriptionSet 中的记录保留，用于重连时恢复）
//  3. 状态 → Suspended，记录 suspendedAt
//
// Suspended 状态下：
//   - Submit(msg) 会走 handleUndelivered：QoS-1 消息存 OfflineStore
//   - retryLoop 继续运行，但 conn=nil 时跳过重发
//   - suspendWatchdog 计时，超过 SuspendTTL 后调用 Close
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

	slog.Info("session: suspended",
		"sid", s.sessionID,
		"uid", s.UserID,
		"pending_acks", s.pendingAckCount(),
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
//   - Session.retryLoop：conn.Submit(entry.msg) [QoS-1 重试]
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
//	成功投递后，将消息加入 pendingAcks。
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

		// QoS-1：加入重试队列，等待客户端 ACK
		// 未收到 ACK 时，retryLoop 会重发
		if msg.Qos == gateway.QoS_AT_LEAST_ONCE {
			s.pendingAcks.Store(msg.MsgId, &pendingEntry{
				msg:         msg,
				firstSentAt: time.Now(),
				lastRetryAt: time.Now(),
			})
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
// Ack 将消息从 pendingAcks 中移除，停止重试。
// ═══════════════════════════════════════════════════════════════════════
func (s *Session) Ack(msgID string) {
	if _, loaded := s.pendingAcks.LoadAndDelete(msgID); loaded {
		slog.Debug("session: QoS-1 acked", "mid", msgID, "sid", s.sessionID)
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

// CleanTimeoutLoop 定期清理超时连接
func (s *Session) CleanTimeoutLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			// cleaned := s.localRouter.CleanTimeout()
			// if cleaned > 0 {
			// 	logger.Info("cleaned timeout connections", zap.Int("count", cleaned))
			// }
		}
	}
}

// Close 永久关闭 Session（登出或 Suspend TTL 超时）。
func (s *Session) Close(kick *gateway.KickPayload) {
	s.once.Do(func() {
		s.state.Store(uint32(StateClosed))
		s.cancel() // 停止 retryLoop 和 suspendWatchdog

		s.connMu.RLock()
		conn := s.conn
		s.connMu.RUnlock()

		if conn != nil {
			conn.Close(kick) // 通知客户端并关闭 TCP 连接
			s.deps.LocalRouter.UnregisterAll(s)
		}
		s.deps.LocalRouter.UnregisterSession(s.userID, s.deviceID)
		slog.Info("session: closed", "sid", s.sessionID, "uid", s.UserID)
	})
}

func (s *Session) SessionID() string { return s.sessionID }
func (s *Session) UserID() string    { return s.userID }
func (s *Session) DeviceID() string  { return s.deviceID }
func (s *Session) State() State      { return State(s.state.Load()) }
func (s *Session) IsActive() bool    { return s.State() == StateActive }

func (s *Session) pendingAckCount() int {
	n := 0
	s.pendingAcks.Range(func(_, _ any) bool { n++; return true })
	return n
}

// GetConnCount returns current connection count
func (s *Session) GetConnCount() uint {
	s.countMux.Lock()
	defer s.countMux.Unlock()
	return s.connCount
}

// ─────────────────────────────────────────────────────────────────────
// retryLoop：QoS-1 重试后台 goroutine
//
// 与 Connection 的交互：
//
//	每个重试周期：
//	  - 读取 s.conn（RLock）
//	  - 如果 conn != nil && conn.IsActive()：conn.Submit(entry.msg)
//	  - conn.Submit 写入 writeCh → WriteLoop → TCP
//
// 注意：retryLoop 直接调 conn.Submit，绕过 Session.Submit，
// 避免重复加入 pendingAcks（消息已经在队列里了）。
// ─────────────────────────────────────────────────────────────────────
func (s *Session) retryLoop() {
	interval := s.deps.RetryInterval
	if interval == 0 {
		interval = 5 * time.Second
	}
	maxRetries := s.deps.MaxRetries
	if maxRetries == 0 {
		maxRetries = 5
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if s.State() != StateActive {
				continue // Suspended：无法重发，等重连
			}

			s.pendingAcks.Range(func(k, v any) bool {
				entry := v.(*pendingEntry)
				entry.retryCount++

				if entry.retryCount > maxRetries {
					slog.Warn("session: QoS-1 give up",
						"mid", entry.msg.MsgId,
						"sid", s.sessionID,
						"retries", entry.retryCount,
					)
					s.pendingAcks.Delete(k)
					return true
				}

				entry.lastRetryAt = time.Now()
				if entry.msg.Headers == nil {
					entry.msg.Headers = make(map[string]string)
				}
				entry.msg.Headers["x-retry-count"] = strconv.Itoa(entry.retryCount)

				// 直接操作 conn，不经 Session.Submit（避免重复加入 pendingAcks）
				s.connMu.RLock()
				conn := s.conn
				s.connMu.RUnlock()

				if conn != nil && conn.IsActive() {
					if !conn.Submit(entry.msg) {
						// writeCh 满：此轮不重发，下轮再试
						slog.Debug("session: retry skipped, writeCh full",
							"mid", entry.msg.MsgId)
					}
				}
				return true
			})
		}
	}
}

// suspendWatchdog：超时关闭 Suspended 的 Session
func (s *Session) suspendWatchdog() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	ttl := s.deps.SuspendTTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if s.State() != StateSuspended {
				continue
			}
			at := s.suspendedAt.Load()
			if at > 0 && time.Since(time.UnixMilli(at)) > ttl {
				slog.Info("session: suspend TTL exceeded",
					"sid", s.SessionID,
					"uid", s.UserID,
					"ttl", ttl,
				)
				s.Close(&gateway.KickPayload{Code: 4040, Reason: "session expired"})
				return
			}
		}
	}
}

// handleUndelivered 处理无法即时投递的消息（Suspended 或 writeCh 满）。
func (s *Session) handleUndelivered(msg *gateway.Message) {
	if !msg.Offline || s.deps.Offline == nil {
		return // QoS-0 或未配置 OfflineStore：直接丢弃
	}
	if msg.Qos == gateway.QoS_AT_MOST_ONCE {
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
