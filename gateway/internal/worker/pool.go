package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/pipeline"
	"github.com/vadam-zhan/long-gw/gateway/internal/pipeline/downlink"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
	"google.golang.org/protobuf/proto"
)

// ErrPoolFull 在 channel 已满时返回（背压信号）。
var ErrPoolFull = fmt.Errorf("worker: pool channel full")

// UpstreamJob 是上行队列的任务单元。
// Sess 字段的用途：当 KafkaSender.Send 失败时，通过 Sess 回传错误给客户端。
type UpstreamJob struct {
	Sess types.SessionRef // 发送方 Session（用于 Kafka 失败时的错误回传）
	Msg  *gateway.Message // 待发送到 Kafka 的消息
}

// DownstreamJob 是下行队列的任务单元。
// 没有 Sess 字段：消息的接收方由 DownlinkChain.RouteStage 从 LocalRouter 解析。
type DownstreamJob struct {
	Msg *gateway.Message
}

// WorkerPool 上行/下行 Worker 池
type WorkerPool struct {
	cfg PoolConfig

	upstreamCh   chan UpstreamJob
	downstreamCh chan DownstreamJob

	dlChain pipeline.Chain[*downlink.DownlinkCtx] // 下行 Pipeline

	// 生命周期
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// WorkerPool 是单个业务域的独立 goroutine 池。
// IM Pool 的流量峰值不影响 Live Pool 的处理延迟。
func newWorkerPool(ctx context.Context, cfg PoolConfig) *WorkerPool {
	ctx, cancel := context.WithCancel(ctx)
	pool := &WorkerPool{
		ctx:          ctx,
		cancel:       cancel,
		cfg:          cfg,
		upstreamCh:   make(chan UpstreamJob, 100),
		downstreamCh: make(chan DownstreamJob, 100),
	}
	// 构建下行 Pipeline（包含 Resolve → FanOut → Session.Submit 的完整链路）
	var offlineAdapter types.OfflineStore
	if cfg.OfflineStore != nil {
		offlineAdapter = &offlineStoreAdapter{store: cfg.OfflineStore}
	}
	pool.dlChain = downlink.BuildChain(downlink.Deps{
		Resolver:     cfg.Resolver, // LocalRouter
		OfflineStore: offlineAdapter,
	})
	return pool
}

// Start 启动 worker 池
func (p *WorkerPool) Start(ctx context.Context) {
	// 启动上行 worker
	for i := 0; i < p.cfg.UpstreamWorkers; i++ {
		p.wg.Add(1)
		go func() {
			p.wg.Done()
			p.upstreamWorker()
		}()
	}
	// 启动下行 worker
	for i := 0; i < p.cfg.DownstreamWorkers; i++ {
		p.wg.Add(1)
		go func() {
			p.wg.Done()
			p.downstreamWorker()
		}()
	}
	slog.Info("worker pool started")
}

// Stop 停止 worker 池
func (p *WorkerPool) Stop() {
	p.cancel()
	close(p.upstreamCh)
	close(p.downstreamCh)
	p.wg.Wait()
}

// ─────────────────────────────────────────────────────────────────────
// SubmitUpstream：Session → Worker 的入口
//
// 调用方：WorkerManager.SubmitUpstream（由 Session.SubmitUpstream 触发）
// 数据流：
//
//	Session.SubmitUpstream(msg)
//	  → mgr.SubmitUpstream(biz, sess, msg)
//	    → pool.SubmitUpstream(UpstreamJob{Sess: sess, Msg: msg})
//
// 非阻塞：channel 满时立即返回 ErrPoolFull。
// 调用方（SubmitStage）收到 ErrPoolFull 后，通过 conn 回 5001 给客户端。
// ─────────────────────────────────────────────────────────────────────
func (p *WorkerPool) SubmitUpstream(job UpstreamJob) error {
	select {
	case p.upstreamCh <- job:
		return nil
	default:
		// 背压：upstreamCh 已满，拒绝接受新任务
		// 调用方（SubmitStage）负责向客户端回 5001
		return ErrPoolFull
	}
}

// ─────────────────────────────────────────────────────────────────────
// SubmitDownstream：KafkaConsumer → Worker 的入口
//
// 调用方：KafkaDownstreamConsumer.consumeLoop
// 数据流：
//
//	Kafka → json.Unmarshal → msg → mgr.SubmitDownstream(biz, msg)
//	  → pool.SubmitDownstream(DownstreamJob{Msg: msg})
//
// 非阻塞：channel 满时立即返回 false。
// 调用方（consumeLoop）记录 metric 后直接 commit offset 继续消费，
// 不阻塞 Kafka 消费进度（防止消费者 lag 积压）。
// ─────────────────────────────────────────────────────────────────────
func (p *WorkerPool) SubmitDownstream(job DownstreamJob) bool {
	select {
	case p.downstreamCh <- job:
		return true
	default:
		return false // 背压：downstreamCh 满，丢弃并记录指标
	}
}

// ─────────────────────────────────────────────────────────────────────
// upstreamWorker：上行处理 goroutine
//
// 与 Session 的交互（双向）：
//
//	正常路径（Kafka 成功）：
//	  job = <-upstreamCh
//	  err = sender.Send(ctx, job.Msg)  → Kafka
//	  // 无需回调 Session
//
//	错误路径（Kafka 失败）：
//	  job.Sess.Submit(errMsg)          → Session.Submit → conn.Submit → writeCh
//
//	★ 关键设计：Worker 通过 job.Sess（Session 接口）回传错误，
//	  不知道底层的 Connection，也不需要关心 QoS 策略。
//	  Session.Submit 内部会处理 writeCh 满的情况（离线存储或丢弃）。
//
// ─────────────────────────────────────────────────────────────────────
func (p *WorkerPool) upstreamWorker() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case job := <-p.upstreamCh:
			// 消息过期检测（可能在队列里等待期间过期）
			if IsExpired(job.Msg.ExpireAt) {
				slog.Debug("worker: upstream drop expired",
					"mid", job.Msg.MsgId,
					"biz", p.cfg.BizCode,
				)
				continue
			}

			// 发送到 Kafka（同步，等待 broker 确认）
			err := p.cfg.UpstreamSender.Send(p.ctx, job.Msg)
			if err != nil {
				slog.Error("worker: upstream send failed",
					"biz", p.cfg.BizCode,
					"mid", job.Msg.MsgId,
					"err", err,
				)

				// ★ Worker → Session 的错误回传
				// 通过 job.Sess.Submit 把错误路由回客户端
				// 调用链：job.Sess.Submit → sess.conn.Submit → writeCh → writeLoop → TCP
				errMsg := &gateway.Message{
					Type: gateway.SignalType_ERROR,
				}
				errMsg.TraceId = job.Msg.TraceId
				if job.Msg != nil {
					errMsg.AckId = job.Msg.MsgId // 让客户端能关联到哪条消息出错了
				}

				payload := &gateway.ErrorPayload{
					Code:    40010,
					Message: "upstream send failed: " + err.Error(),
				}
				p, _ := proto.Marshal(payload)
				errMsg.Body = &gateway.Body{
					Type:    job.Msg.BizCode,
					Payload: p,
				}
				errMsg.AckId = job.Msg.MsgId // 客户端通过 AckID 关联到哪条消息失败了

				// 注意：这里调用的是 SessionRef 接口的 Submit，
				// 不是 Session.SubmitUpstream。
				// Submit → conn.Submit → writeCh（非阻塞）
				if !job.Sess.Submit(errMsg) {
					// writeCh 已满：错误消息也无法送达，记录日志
					slog.Warn("worker: error reply dropped, writeCh full",
						"uid", job.Sess.UserID(),
						"mid", job.Msg.MsgId,
					)
				}
			}
		}
	}
}

func IsExpired(expireAt int64) bool {
	return expireAt > 0 && time.Now().UnixMilli() > expireAt
}

// ─────────────────────────────────────────────────────────────────────
// downstreamWorker：下行处理 goroutine
//
// 与 Session 的交互（通过 DownlinkChain）：
//
//	job = <-downstreamCh
//	dlChain.Run(ctx) → ValidateStage → RouteStage → FilterStage → FanOutStage
//
//	RouteStage 与 Session 的交互：
//	  resolver.Resolve(msg.To) → LocalRouter → []SessionRef
//	  ctx.TargetSessions = sessions
//
//	FanOutStage 与 Session 的交互（扇出投递）：
//	  for sess in ctx.TargetSessions:
//	      ok = sess.Submit(msg)           → Session.Submit → conn.Submit → writeCh
//	      if !ok: offlineStore.Store(msg) → 持久化
//
// 背压传导完整路径：
//
//	Kafka 消费速率 → downstreamCh 满 → 消费者丢弃/限速
//	conn.Submit 失败 → Session 存离线 → 客户端重连后拉取
//
// ─────────────────────────────────────────────────────────────────────
func (p *WorkerPool) downstreamWorker() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case job := <-p.downstreamCh:
			// 创建下行上下文，运行完整 DownlinkChain
			ctx := &downlink.DownlinkCtx{
				BaseCtx: pipeline.BaseCtx{
					ReceivedAt: time.Now(),
					Values:     make(map[string]any),
				},
				Message: job.Msg,
			}
			// dlChain 内部的交互：
			//   ValidateStage: 过期检测（MQ 消费延迟可能导致过期）
			//   RouteStage:    resolver.Resolve(To) → []Session
			//   FilterStage:   过滤非 Active 的 Session，处理回音消除
			//   FanOutStage:   sess.Submit(msg) → conn.Submit → writeCh
			p.dlChain.Run(ctx)
		}
	}
}

type offlineStoreAdapter struct{ store types.OfflineStore }

func (a *offlineStoreAdapter) Store(ctx context.Context, msg *gateway.Message) error {
	return a.store.Store(ctx, msg)
}

func (a *offlineStoreAdapter) Fetch(ctx context.Context, userID string, afterSeq uint64) ([]*gateway.Message, error) {
	return a.store.Fetch(ctx, userID, afterSeq)
}
func (a *offlineStoreAdapter) Delete(ctx context.Context, msgID string) error {
	return a.store.Delete(ctx, msgID)
}
