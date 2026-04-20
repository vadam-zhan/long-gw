package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/pipeline/downlink"
	"github.com/vadam-zhan/long-gw/gateway/internal/router"
	"github.com/vadam-zhan/long-gw/gateway/internal/session"
)

// File: gateway/internal/worker/manager.go + pool.go
//
// ═══════════════════════════════════════════════════════════════════════
// 交互点四：Worker 与 Session 和 Connection 的双向交互
//
// Worker 层有两个方向的交互：
//
//   上行（upstream）：
//     Session → Worker（SubmitUpstream）→ upstreamWorker → Kafka
//     Kafka 失败时：upstreamWorker → job.Sess.Submit(errMsg)
//                               → sess.conn.Submit(errMsg) → writeCh
//
//   下行（downstream）：
//     Kafka Consumer → Worker（SubmitDownstream）→ downstreamWorker
//     → DownlinkChain.Run() → RouteStage → sess[] → FanOutStage
//     → sess.Submit(msg) → conn.Submit(msg) → writeCh
//
// WorkerPool 持有的 Session 引用（UpstreamJob.Sess）是"回调通道"：
//   - 正常情况：Worker 不需要 Session，直接发 Kafka
//   - 失败情况：通过 Sess 把错误路由回对应的客户端连接
//
// ═══════════════════════════════════════════════════════════════════════

// ─────────────────────────────────────────────────────────────────────
// Worker 层对 Session 的最小接口
//
// Worker 只需要 Session 的投递能力，不需要其他 Session 方法。
// 通过接口解耦：upstreamWorker 只持有 SessionRef，不知道具体的 Session 类型。
// ─────────────────────────────────────────────────────────────────────

// SessionRef 是 Worker 层对 Session 的视图。
// upstreamWorker 在 Kafka 发送失败时，通过此接口将错误路由回客户端。
type SessionRef interface {
	// Submit 将错误消息投递给客户端。
	// 内部调用链：sess.Submit → conn.Submit → writeCh → writeLoop → TCP
	Submit(msg *gateway.Message) bool

	// 以下方法供 DownlinkChain 的 RouteStage 和 FanOutStage 使用
	IsActive() bool
	SessionID() string
	UserID() string
}

type PoolConfig struct {
	Router *router.LocalRouter

	BizCode           string
	UpstreamWorkers   int
	UpstreamChanCap   int
	DownstreamWorkers int
	DownstreamChanCap int
	UpstreamSender    UpstreamSender
	OfflineStore      OfflineStore // nil = QoS-0（Live 弹幕不需要离线）

	Resolver downlink.SessionResolver // LocalRouter 实现此接口

	Brokers []string
	Topic   string
}

type Manager struct {
	pools map[string]*WorkerPool // bizCode -> pool
	mu    sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{
		pools: make(map[string]*WorkerPool),
	}
}

// CreatePool 为业务域创建 WorkerPool
func (m *Manager) CreatePool(cfg *PoolConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()

	pool := NewWorkerPool(cfg)
	m.pools[cfg.BizCode] = pool
}

func (m *Manager) getPool(bizCode string) (*WorkerPool, error) {
	m.mu.RLock()
	p, ok := m.pools[bizCode]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("worker: no pool for biz %q (registered: %v)", bizCode, m.bizCodes())
	}
	return p, nil
}

func (m *Manager) bizCodes() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	codes := make([]string, 0, len(m.pools))
	for k := range m.pools {
		codes = append(codes, k)
	}
	return codes
}

// StartAll 启动所有 Pool
func (m *Manager) StartAll(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, pool := range m.pools {
		pool.Start(ctx)
	}
}

// SubmitUpstream 上行提交(Handler -> Pipeline -> WorkerManager)
func (m *Manager) SubmitUpstream(bizCode string, sess *session.Session, msg *gateway.Message) error {
	m.mu.Lock()
	pool, ok := m.pools[bizCode]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("ErrUnknownBizCode")
	}

	return pool.SubmitUpstream(UpstreamJob{Sess: sess, Msg: msg})
}

// SubmitDownstream 是 KafkaDownstreamConsumer 的调用目标。
// 按 bizCode 路由到对应 WorkerPool.downstreamCh。
func (m *Manager) SubmitDownstream(bizCode string, msg *gateway.Message) bool {
	pool, err := m.getPool(bizCode)
	if err != nil {
		slog.Warn("worker: pool not found", "biz", bizCode, "mid", msg.MsgId)
		return false
	}
	return pool.SubmitDownstream(DownstreamJob{Msg: msg})
}

func (m *Manager) Stop() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var wg sync.WaitGroup
	for biz, pool := range m.pools {
		wg.Add(1)
		go func(b string, p *WorkerPool) {
			defer wg.Done()
			p.Stop()
			slog.Info("worker: pool stopped", "biz", b)
		}(biz, pool)
	}
	wg.Wait()
}
