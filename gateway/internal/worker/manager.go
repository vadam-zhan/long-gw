package worker

import (
    "context"

    gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
    "github.com/vadam-zhan/long-gw/gateway/internal/worker/upstream"
    "github.com/vadam-zhan/long-gw/gateway/internal/router"
    "github.com/vadam-zhan/long-gw/gateway/internal/worker/storage"
    "github.com/vadam-zhan/long-gw/gateway/internal/types"
)

// UpstreamSenderFactory 上行发送器工厂接口
type UpstreamSenderFactory interface {
    CreateSender(bt string) (upstream.Sender, error)
}

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

// GetPool 获取指定业务类型的 WorkerPool
func (wm *WorkerManager) GetPool(bizType gateway.BusinessType) (*WorkerPool, bool) {
    pool, ok := wm.pools[bizType]
    return pool, ok
}

// GetUpstreamSender 获取上行发送器（供 Connection 使用）
func (wm *WorkerManager) GetUpstreamSender() UpstreamSender {
    return &multiBizTypeSender{pools: wm.pools}
}

// CreatePools 创建所有业务的 WorkerPool
func (wm *WorkerManager) CreatePools(businessTypes []string, factory UpstreamSenderFactory) {
    for _, btStr := range businessTypes {
        bt := kafkaStringToProtoBusinessType(btStr)
        if bt == gateway.BusinessType_BusinessType_UNSPECIFIED {
            continue
        }

        sender, err := factory.CreateSender(btStr)
        if err != nil {
            continue
        }

        pool := NewWorkerPool(
            bt,
            AsUpstreamPipeline(sender),
            wm.connRegistry,
            wm.offlineStore,
        )
        wm.pools[bt] = pool
    }
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
    Submit(ctx context.Context, job types.UpstreamJob) types.SubmitResult
}

// multiBizTypeSender 多业务类型上行发送器
type multiBizTypeSender struct {
    pools map[gateway.BusinessType]*WorkerPool
}

func (s *multiBizTypeSender) Submit(ctx context.Context, job types.UpstreamJob) types.SubmitResult {
    // 将 types.BusinessType 转换为 gateway.BusinessType
    bizType := job.BizType.Proto()

    pool, ok := s.pools[bizType]
    if !ok {
        return types.SubmitResult{Accepted: false, Reason: "unknown_biz_type"}
    }

    return pool.SubmitUpstream(ctx, job)
}

// kafkaStringToProtoBusinessType 将字符串转换为 proto BusinessType
func kafkaStringToProtoBusinessType(bt string) gateway.BusinessType {
    switch bt {
    case "im":
        return gateway.BusinessType_BusinessType_IM
    case "live":
        return gateway.BusinessType_BusinessType_LIVE
    case "message":
        return gateway.BusinessType_BusinessType_MESSAGE
    default:
        return gateway.BusinessType_BusinessType_UNSPECIFIED
    }
}