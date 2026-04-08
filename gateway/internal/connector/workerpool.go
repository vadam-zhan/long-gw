package connector

import (
	"context"
	"sync"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/connector/upstream"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/svc"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"

	"go.uber.org/zap"
)

// WorkerPoolInterface worker池接口
type WorkerPoolInterface interface {
	Start()
	Stop()
	SubmitUpstream(job UpstreamJob) bool
	SubmitDownstream(job DownstreamJob) bool
}

// UpstreamJob 上行任务
type UpstreamJob struct {
	Msg  *types.Message
	Ctx  context.Context
	Conn *Connection
}

// DownstreamJob 下行任务
type DownstreamJob struct {
	DownstreamMsg *gateway.DownstreamKafkaMessage
}

// WorkerPool 分离 Upstream/Downstream 的 worker 池
type WorkerPool struct {
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc

	businessType gateway.BusinessType

	upstreamCh       chan UpstreamJob
	downstreamCh     chan DownstreamJob
	upstreamSender   upstream.Sender           // 上行发送器（Kafka/gRPC）
	downstreamRouter DownstreamRouterInterface // 下行，kafka 消费后消息写入tcp，todo 未来扩展成支持 grpc 也写入 tcp
}

// NewWorkerPool 创建 worker 池
func NewWorkerPool(svc *svc.ServiceContext, businessType gateway.BusinessType, sender upstream.Sender, router DownstreamRouterInterface) *WorkerPool {
	poolCtx, cancel := context.WithCancel(svc.Context)
	return &WorkerPool{
		ctx:              poolCtx,
		cancel:           cancel,
		businessType:     businessType,
		upstreamCh:       make(chan UpstreamJob, svc.Config.Gateway.UpstreamWorkerNum*10),
		downstreamCh:     make(chan DownstreamJob, svc.Config.Gateway.DownstreamWorkerNum*10),
		upstreamSender:   sender,
		downstreamRouter: router,
	}
}

// Start 启动 worker 池
func (p *WorkerPool) Start() {
	upstreamChLen := cap(p.upstreamCh)
	downstreamChLen := cap(p.downstreamCh)

	for i := 0; i < upstreamChLen/10; i++ {
		p.wg.Add(1)
		go p.upstreamWorker(i)
	}
	for i := 0; i < downstreamChLen/10; i++ {
		p.wg.Add(1)
		go p.downstreamWorker(i)
	}
	logger.Info("worker pool started",
		zap.Int("upstream_workers", upstreamChLen/10),
		zap.Int("downstream_workers", downstreamChLen/10),
		zap.String("business_type", p.businessType.String()))
}

// upstreamWorker 处理上行任务
func (p *WorkerPool) upstreamWorker(id int) {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			logger.Debug("upstream worker stopped",
				zap.Int("worker_id", id))
			return
		case job, ok := <-p.upstreamCh:
			if !ok {
				return
			}
			p.processUpstreamJob(job)
		}
	}
}

// downstreamWorker 处理下行任务
func (p *WorkerPool) downstreamWorker(id int) {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			logger.Debug("downstream worker stopped",
				zap.Int("worker_id", id))
			return
		case job, ok := <-p.downstreamCh:
			if !ok {
				return
			}
			p.processDownstreamJob(job)
		}
	}
}

// processUpstreamJob 处理上行任务
func (p *WorkerPool) processUpstreamJob(job UpstreamJob) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("processUpstreamJob panic",
				zap.Any("error", r),
				zap.String("msg_id", job.Msg.MsgID))
			p.sendErrorToClient(job)
		}
	}()

	if p.upstreamSender == nil {
		logger.Debug("upstream sender not available, skipping upstream send",
			zap.String("msg_id", job.Msg.MsgID))
		return
	}

	job.Msg.UserID, job.Msg.DeviceID = job.Conn.GetUserInfo()
	req := &types.UpstreamRequest{
		ConnID: job.Conn.GetConnID(),
		Msg:    job.Msg,
	}
	if err := p.upstreamSender.Send(job.Ctx, req); err != nil {
		logger.Error("failed to send upstream message",
			zap.Error(err),
			zap.String("conn_id", req.ConnID),
			zap.String("sender_kind", p.upstreamSender.Kind().String()))
		p.sendErrorToClient(job)
	}
}

// processDownstreamJob 处理下行任务
func (p *WorkerPool) processDownstreamJob(job DownstreamJob) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("processDownstreamJob panic",
				zap.Any("error", r),
				zap.String("correlation_id", job.DownstreamMsg.CorrelationId))
		}
	}()

	if p.downstreamRouter == nil {
		logger.Warn("downstream router not available, skipping message",
			zap.String("correlation_id", job.DownstreamMsg.CorrelationId))
		return
	}

	if err := p.downstreamRouter.RouteDownstreamMessage(job.DownstreamMsg); err != nil {
		logger.Debug("downstream message routing failed",
			zap.String("correlation_id", job.DownstreamMsg.CorrelationId),
			zap.Error(err))
	}
}

// sendErrorToClient sends an error message back to the client
func (p *WorkerPool) sendErrorToClient(job UpstreamJob) {
	respMsg := &types.Message{
		MsgID:  job.Msg.MsgID,
		Type:   gateway.SignalType_SIGNAL_TYPE_UNSPECIFIED,
		Body:   []byte("upstream failed"),
		UserID: job.Msg.UserID,
	}
	select {
	case job.Conn.WriteCh <- respMsg:
	default:
		userID, _ := job.Conn.GetUserInfo()
		logger.Warn("write channel full, cannot send error",
			zap.String("user_id", userID))
	}
}

// SubmitUpstream 提交上行任务
func (p *WorkerPool) SubmitUpstream(job UpstreamJob) bool {
	select {
	case p.upstreamCh <- job:
		return true
	default:
		logger.Warn("upstream channel full, reject upstream job")
		return false
	}
}

// SubmitDownstream 提交下行任务
func (p *WorkerPool) SubmitDownstream(job DownstreamJob) bool {
	select {
	case p.downstreamCh <- job:
		return true
	default:
		logger.Warn("downstream channel full, reject downstream job",
			zap.String("correlation_id", job.DownstreamMsg.CorrelationId))
		return false
	}
}

// Stop 停止 worker 池
func (p *WorkerPool) Stop() {
	p.cancel()
	close(p.upstreamCh)
	close(p.downstreamCh)
	p.wg.Wait()
	logger.Info("worker pool stopped")
}
