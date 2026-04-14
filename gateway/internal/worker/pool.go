package worker

import (
    "context"
    "sync"

    gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
    "github.com/vadam-zhan/long-gw/gateway/internal/logger"
    "github.com/vadam-zhan/long-gw/gateway/internal/types"
    "github.com/vadam-zhan/long-gw/gateway/internal/worker/storage"
    "go.uber.org/zap"
)

// DownstreamJob 下行任务
type DownstreamJob struct {
    Msg    *gateway.DownstreamKafkaMessage
    Offset int64
}

// WorkerPool 上行/下行 Worker 池
type WorkerPool struct {
    bizType gateway.BusinessType

    // 上行
    upstreamCh   chan types.UpstreamJob
    upstreamPipe UpstreamPipeline

    // 下行
    downstreamCh   chan DownstreamJob
    downstreamStore storage.OfflineStore

    // 连接注册表
    connRegistry ConnectionRegistry

    // 生命周期
    ctx    context.Context
    cancel context.CancelFunc
    wg     sync.WaitGroup
}

// UpstreamPipeline 上行管道接口
type UpstreamPipeline interface {
    Send(ctx context.Context, req *types.UpstreamRequest) error
}

// NewWorkerPool 创建 WorkerPool
func NewWorkerPool(
    bizType gateway.BusinessType,
    upstreamPipe UpstreamPipeline,
    connRegistry ConnectionRegistry,
    offlineStore storage.OfflineStore,
) *WorkerPool {
    ctx, cancel := context.WithCancel(context.Background())
    pool := &WorkerPool{
        bizType:       bizType,
        upstreamCh:    make(chan types.UpstreamJob, 1000),
        downstreamCh:  make(chan DownstreamJob, 1000),
        upstreamPipe:  upstreamPipe,
        connRegistry:  connRegistry,
        downstreamStore: offlineStore,
        ctx:           ctx,
        cancel:        cancel,
    }
    return pool
}

// Start 启动 worker 池
func (p *WorkerPool) Start() {
    // 启动上行 worker
    for i := 0; i < 10; i++ {
        p.wg.Add(1)
        go p.upstreamWorker(i)
    }
    // 启动下行 worker
    for i := 0; i < 10; i++ {
        p.wg.Add(1)
        go p.downstreamWorker(i)
    }
    logger.Info("worker pool started", zap.String("biz_type", p.bizType.String()))
}

// Stop 停止 worker 池
func (p *WorkerPool) Stop() {
    p.cancel()
    close(p.upstreamCh)
    close(p.downstreamCh)
    p.wg.Wait()
}

// SubmitUpstream 提交上行任务
func (p *WorkerPool) SubmitUpstream(ctx context.Context, job types.UpstreamJob) types.SubmitResult {
    // 处理 ACK 消息
    if job.IsAck {
        return p.handleAck(ctx, job.AckMsgID)
    }

    select {
    case p.upstreamCh <- job:
        return types.SubmitResult{Accepted: true, Reason: "ok"}
    case <-ctx.Done():
        return types.SubmitResult{Accepted: false, Reason: "context_cancelled"}
    default:
        return types.SubmitResult{Accepted: false, Reason: "queue_full"}
    }
}

// SubmitDownstream 提交下行任务
func (p *WorkerPool) SubmitDownstream(job DownstreamJob) bool {
    select {
    case p.downstreamCh <- job:
        return true
    default:
        return false
    }
}

// upstreamWorker 上行处理 goroutine
func (p *WorkerPool) upstreamWorker(id int) {
    defer p.wg.Done()
    for {
        select {
        case <-p.ctx.Done():
            return
        case job, ok := <-p.upstreamCh:
            if !ok {
                return
            }
            p.processUpstream(job)
        }
    }
}

// processUpstream 处理上行任务
func (p *WorkerPool) processUpstream(job types.UpstreamJob) {
    defer func() {
        if r := recover(); r != nil {
            logger.Error("processUpstream panic", zap.Any("error", r))
        }
    }()

    req := &types.UpstreamRequest{
        ConnID: job.ConnID,
        Msg: &types.Message{
            Type:       types.SignalTypeBusinessUp,
            Payload:    job.Payload,
            UserID:     job.UserID,
            DeviceID:   job.DeviceID,
            ConnID:     job.ConnID,
        },
    }

    // 使用 Background context，因为 types.UpstreamJob 不包含 context
    if err := p.upstreamPipe.Send(context.Background(), req); err != nil {
        logger.Error("upstream send failed", zap.String("conn_id", job.ConnID), zap.Error(err))
    }
}

// handleAck 处理 ACK 消息
func (p *WorkerPool) handleAck(ctx context.Context, msgID string) types.SubmitResult {
    if p.downstreamStore == nil {
        return types.SubmitResult{Accepted: true, Reason: "no_offline_store"}
    }

    // 标记已送达
    p.downstreamStore.MarkDelivered(ctx, msgID)
    // 删除离线消息
    p.downstreamStore.Delete(ctx, msgID)

    return types.SubmitResult{Accepted: true, Reason: "ok"}
}

// downstreamWorker 下行处理 goroutine
func (p *WorkerPool) downstreamWorker(id int) {
    defer p.wg.Done()
    for {
        select {
        case <-p.ctx.Done():
            return
        case job, ok := <-p.downstreamCh:
            if !ok {
                return
            }
            p.processDownstream(job)
        }
    }
}

// processDownstream 处理下行任务
func (p *WorkerPool) processDownstream(job DownstreamJob) {
    msg := job.Msg

    // 获取 QoS 级别
    qos := p.getQosLevel(msg.BusinessType)

    // 构建下行消息
    downstreamMsg := p.buildDownstreamMessage(msg, qos)

    // 获取用户连接
    writer, ok := p.connRegistry.Get(msg.ConnId)
    if !ok {
        // 用户离线，处理离线存储
        p.handleOffline(msg, qos)
        return
    }

    // 尝试投递
    if !writer.Write(downstreamMsg) {
        logger.Warn("downstream write channel full, store offline",
            zap.String("user_id", msg.UserId),
            zap.String("msg_id", msg.CorrelationId))
        p.handleOffline(msg, qos)
    }
}

// handleOffline 处理离线消息
func (p *WorkerPool) handleOffline(msg *gateway.DownstreamKafkaMessage, qos types.QosLevel) {
    // Live 消息不需要离线存储
    if qos == types.QosAtMostOnce {
        return
    }

    offlineMsg := &storage.OfflineMessage{
        MsgID:     msg.CorrelationId,
        UserID:    msg.UserId,
        DeviceID:  msg.DeviceId,
        FromUserID: "",
        MsgType:   msg.BusinessType,
        RoomType:  0,
        Payload:   msg.Payload,
        SeqID:     0,
        Timestamp: msg.Timestamp,
    }

    p.downstreamStore.Save(context.Background(), offlineMsg)
}

// buildDownstreamMessage 构建下行消息
func (p *WorkerPool) buildDownstreamMessage(msg *gateway.DownstreamKafkaMessage, qos types.QosLevel) *types.Message {
    return &types.Message{
        Type: types.SignalTypeBusinessDown,
        Payload: &types.DownstreamPayload{
            MsgID:      msg.CorrelationId,
            BizType:    types.BusinessTypeFromProto(msg.BusinessType),
            RoomID:     msg.UserId,
            RoomType:   types.RoomTypeSingle,
            FromUserID: "",
            Body:       msg.Payload,
            Qos:        qos,
            NeedAck:    qos == types.QosAtLeastOnce,
        },
        Metadata: map[string]string{
            "correlation_id": msg.CorrelationId,
        },
    }
}

// getQosLevel 获取 QoS 级别
func (p *WorkerPool) getQosLevel(bizType gateway.BusinessType) types.QosLevel {
    switch bizType {
    case gateway.BusinessType_BusinessType_LIVE:
        return types.QosAtMostOnce
    default:
        return types.QosAtLeastOnce
    }
}
