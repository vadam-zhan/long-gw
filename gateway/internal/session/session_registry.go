package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/svc"
)

type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*Session // sessionID -> *Session

	// 索引
	userIndex map[string]map[string]*Session // userID -> {deviceID -> Session}

	deps       Deps
	suspendTTL time.Duration // max time Suspended before Closed (default: 5m)

	svc *svc.ServiceContext
}

type SessionRegistryOption func(*SessionRegistry)

func SetSuspendTTL(ttl int) SessionRegistryOption {
	return func(sr *SessionRegistry) {
		sr.suspendTTL = time.Duration(ttl) * time.Second
	}
}

func WithLocalRouter(router LocalRouterOps) SessionRegistryOption {
	return func(sr *SessionRegistry) {
		sr.deps.LocalRouter = router
	}
}

// func WithDistRouter(router *router.DistributedRouter) SessionRegistryOption {
// 	return func(sr *SessionRegistry) {
// 		sr.deps.DistributionRouter = router
// 	}
// }

func WithOfflineStore(store OfflineStorer) SessionRegistryOption {
	return func(sr *SessionRegistry) {
		sr.deps.Offline = store
	}
}

func WithServiceContext(svc *svc.ServiceContext) SessionRegistryOption {
	return func(sr *SessionRegistry) {
		sr.svc = svc
	}
}

func NewSessionRegistry(opts ...SessionRegistryOption) *SessionRegistry {
	sr := &SessionRegistry{
		sessions:  make(map[string]*Session),
		userIndex: make(map[string]map[string]*Session),
	}
	for _, opt := range opts {
		opt(sr)
	}
	return sr
}

// GetOrCreate 获取或创建 Session
// 调用方：GatewayServer.handleConnection（鉴权成功后）
// 这里不绑定业务域，例如: bizCode
func (sr *SessionRegistry) GetOrCreate(userID, deviceID string) *Session {
	sessionID := computeSessionID(userID, deviceID) // sha256(userID+":"+deviceID)[:16]

	// 快速路径：读锁查找
	sr.mu.RLock()
	if devs, ok := sr.userIndex[userID]; ok {
		if sess, ok := devs[deviceID]; ok {
			sr.mu.RUnlock()
			// 已有 Session，可能是重连
			return sess
		}
	}
	sr.mu.RUnlock()

	// 慢路径：写锁创建
	sr.mu.Lock()
	defer sr.mu.Unlock()

	// Double-check
	if devs, ok := sr.userIndex[userID]; ok {
		if sess, ok := devs[deviceID]; ok {
			return sess
		}
	}

	// 创建新 Session
	sess := NewSession(sr.svc, userID, deviceID, "", "", "", sr.deps)
	sess.state.Store(uint32(StateAuthenticating))

	sr.sessions[sessionID] = sess
	if sr.userIndex[userID] == nil {
		sr.userIndex[userID] = make(map[string]*Session)
	}

	// 踢出同设备旧 Session
	if old, ok := sr.userIndex[userID][deviceID]; ok && old.sessionID != sessionID {
		old.Close(&gateway.KickPayload{Code: 4040, Reason: "session expired"})
	}

	sr.userIndex[userID][deviceID] = sess
	// metrics.SessionCreated.Inc()

	return sess
}

func (sr *SessionRegistry) GCLoop(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sr.gc()
		}
	}
}

// computeSessionID(userID, deviceID) // sha256(userID+":"+deviceID)[:16]
func computeSessionID(userID, deviceID string) string {
	hash := sha256.Sum256([]byte(userID + ":" + deviceID))
	return hex.EncodeToString(hash[:16])
}

func (sr *SessionRegistry) gc() {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	// for id, sess := range sr.sessions {
	// suspended 超时 -> closed
	// if sess.IsExpired(sr.suspendTTL) {
	// 	sess.Close()
	// 	delete(sr.sessions, id)

	// 	if devs, ok := sr.userIndex[sess.UserID]; ok {
	// 		delete(devs, sess.DeviceID)
	// 		if len(devs) == 0 {
	// 			delete(sr.userIndex, sess.UserID)
	// 		}
	// 	}

	// 	// metrics.SessionExpired.Inc()
	// }
	// }
}

// ==============================
// 业务域路由：Message.BizCode → WorkerPool
// ==============================

// 上行路由：由 Message.BizCode 决定进入哪个 WorkerPool
// Session 不感知业务域
//
//   Client → Connection.ReadLoop → HandlerRegistry.Dispatch
//     → UpstreamHandler.Handle(sess, conn, msg)
//       → UplinkChain: SubmitStage
//         → sess.SubmitUpstream(msg)
//           → WorkerManager.SubmitUpstream(msg.BizCode, sess, msg)
//             → pools[msg.BizCode].upstreamCh ← job
//               → upstreamWorker → Kafka gw.{bizCode}.up
//
// 下行路由：由 Message.To + Message.BizCode 决定
//   Kafka → Consumer → WorkerManager.SubmitDownstream(bizCode, msg)
//     → pools[bizCode].downstreamCh ← job
//       → downstreamWorker → DownlinkChain
//         → RouteStage: LocalRouter.Resolve(msg.To) → []*Session
//           → FanOutStage: sess.Submit(msg) → conn.Submit(msg)

// ==============================
// 多业务共存的 Session 管理
// ==============================

// 场景：用户 user_123 同时使用 IM 和 Live
//   - 同一设备只有一个 Session（per-user-per-device）
//   - IM 消息上行：msg.BizCode="im" → pools["im"].upstreamCh
//   - Live 消息上行：msg.BizCode="live" → pools["live"].upstreamCh
//   - IM 消息下行：Kafka gw.im.down → pools["im"] → Route → sess.Submit
//   - Live 消息下行：Kafka gw.live.down → pools["live"] → Route → sess.Submit
//   - Session.Submit 不关心消息来自哪个业务域，统一走 conn.Submit
//
// 关键：Session 是连接与业务之间的隔离层
//   - Connection 不知道消息属于哪个业务
//   - Worker 不知道消息属于哪个 Connection
//   - Session 作为桥梁，协调两者

// ==============================
// 业务域 WorkerPool 隔离级别
// ==============================

// ==============================
// 业务域 WorkerPool 隔离级别
// ==============================

// 1. 资源隔离（已实现）：每个 bizCode 独立 channel + goroutine 池
//    - IM 流量峰值不影响 Live 弹幕投递
//
// 2. 限流隔离（需实现）：per-bizCode 限流器
