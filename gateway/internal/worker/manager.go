package worker

import (
    "context"

    gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
    "github.com/vadam-zhan/long-gw/gateway/internal/router"
    "github.com/vadam-zhan/long-gw/gateway/internal/worker/storage"
    "github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// WorkerManager 统一管理所有 Worker 组件
type WorkerManager struct {
    distRouter   router.DistributedRouterInterface
    connRegistry ConnectionRegistry
    offlineStore storage.OfflineStore
    pools        map[gateway.BusinessType]*WorkerPool
}

// NewWorkerManager 创建 WorkerManager
func NewWorkerManager(
    dr router.DistributedRouterInterface,
    store storage.OfflineStore,
) *WorkerManager {
    return &WorkerManager{
        distRouter:   dr,
        offlineStore: store,
        pools:        make(map[gateway.BusinessType]*WorkerPool),
    }
}

// SetConnectionRegistry 设置连接注册表
func (wm *WorkerManager) SetConnectionRegistry(reg ConnectionRegistry) {
    wm.connRegistry = reg
}

// RegisterPool 注册 WorkerPool
func (wm *WorkerManager) RegisterPool(bizType gateway.BusinessType, pool *WorkerPool) {
    wm.pools[bizType] = pool
}

// GetConnectionRegistry 获取连接注册表
func (wm *WorkerManager) GetConnectionRegistry() ConnectionRegistry {
    return wm.connRegistry
}

// GetOfflineStore 获取离线存储
func (wm *WorkerManager) GetOfflineStore() storage.OfflineStore {
    return wm.offlineStore
}

// GetUpstreamSender 获取上行发送器（供 Connection 使用）
func (wm *WorkerManager) GetUpstreamSender() UpstreamSender {
    return &multiBizTypeSender{pools: wm.pools}
}

// Start 启动所有 Worker
func (wm *WorkerManager) Start(ctx context.Context) error {
    for _, pool := range wm.pools {
        pool.Start()
    }
    return nil
}

// Stop 停止所有 Worker
func (wm *WorkerManager) Stop() {
    for _, pool := range wm.pools {
        pool.Stop()
    }
}

// UpstreamSender 上行发送器接口
type UpstreamSender interface {
    Submit(ctx context.Context, job UpstreamJob) types.SubmitResult
}

// UpstreamJob 上行任务（简化版本）
type UpstreamJob struct {
    BizType gateway.BusinessType
    ConnID  string
    UserID  string
    Payload []byte
}

// multiBizTypeSender 多业务类型上行发送器
type multiBizTypeSender struct {
    pools map[gateway.BusinessType]*WorkerPool
}

func (s *multiBizTypeSender) Submit(ctx context.Context, job UpstreamJob) types.SubmitResult {
    pool, ok := s.pools[job.BizType]
    if !ok {
        return types.SubmitResult{Accepted: false, Reason: "unknown_biz_type"}
    }

    // 将 []byte payload 包装为 BusinessPayload
    bizPayload := &types.BusinessPayload{
        Body: job.Payload,
    }

    upstreamJob := types.UpstreamJob{
        ConnID:  job.ConnID,
        UserID:  job.UserID,
        BizType: types.BusinessTypeFromProto(job.BizType),
        Payload: bizPayload,
    }

    return pool.SubmitUpstream(ctx, upstreamJob)
}
