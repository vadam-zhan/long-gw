package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/spf13/cobra"
	"github.com/vadam-zhan/long-gw/gateway/internal/access/connruntime"
	"github.com/vadam-zhan/long-gw/gateway/internal/access/handler"
	"github.com/vadam-zhan/long-gw/gateway/internal/config"
	"github.com/vadam-zhan/long-gw/gateway/internal/delivery/ack"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/pipeline/uplink"
	"github.com/vadam-zhan/long-gw/gateway/internal/protocol/codec"
	"github.com/vadam-zhan/long-gw/gateway/internal/router"
	"github.com/vadam-zhan/long-gw/gateway/internal/session"
	"github.com/vadam-zhan/long-gw/gateway/internal/svc"
	"github.com/vadam-zhan/long-gw/gateway/internal/timer"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker/storage"
)

func Run(cmd *cobra.Command, args []string) {
	// 加载配置
	cfg, err := config.Load(cfgFile)
	if err != nil {
		slog.Error("failed to load config", "error", err)
	}

	// 初始化日志
	logger.Init(cfg.Log.Level, cfg.Log.File)

	// 输出配置信息
	slog.Info("gateway config loaded", "cfg", cfg)

	// 创建并启动网关
	gw, err := NewGatewayServer(cfg)
	if err != nil {
		slog.Error("gateway start failed", "error", err)
	}
	if err := gw.Start(); err != nil {
		slog.Error("gateway start failed", "error", err)
	}
}

// NewGatewayServer 创建网关服务器
func NewGatewayServer(cfg *config.Config) (*GatewayServer, error) {
	ctx, cancel := context.WithCancel(context.Background())
	gs := &GatewayServer{
		ctx:    ctx,
		cancel: cancel,
	}
	gs.svc = svc.NewServiceContext(ctx, cfg)

	// 初始化路由
	gs.localRouter = router.NewLocalRouter()
	gs.distRouter = router.NewDistributedRouter(gs.svc.RedisClient, cfg.Gateway.Addr)

	// 初始化离线存储
	gs.offlineStore = storage.NewMySQLStore(gs.svc.DB)

	// 初始化连接注册表
	gs.connRegistry = connruntime.NewRegistry()

	// 初始化 Timer（分片时间轮）
	gs.timer = timer.NewScheduler(64)

	// 初始化 ACK Scanner（集中调度 QoS-1 超时重试）
	ackScanner := ack.NewScanner(gs.timer, 5*time.Second, gs.offlineStore)

	// 初始化会话注册表
	gs.sessRegistry = session.NewRegistry(
		session.WithLocalRouter(gs.localRouter),
		session.WithOfflineStore(gs.offlineStore),
		session.SetSuspendTTL(cfg.Session.SuspendTTL),
		session.WithAckScanner(ackScanner),
	)

	gs.workerManager = worker.NewManager(gs.ctx)
	if err := gs.registerWorkerPools(); err != nil {
		cancel()
		return nil, fmt.Errorf("gateway: worker pools: %w", err)
	}

	gs.codec = codec.NewCodec(codec.DefaultConfig())

	uplinkChain := uplink.BuildChain(nil) // nil = no rate limiter in dev; inject real limiter in prod
	chainAdapter := uplink.NewChainAdapter(uplinkChain)

	authVerifier := handler.NewAuthVerifier(cfg.Auth.Addr)
	handlerReg := handler.Build(authVerifier, chainAdapter)

	// 初始化 connectionFactory
	gs.connFactory = connruntime.NewFactory(&connruntime.FactoryDeps{
		Codec:            gs.codec,
		HandlerReg:       handlerReg,
		ConnRegistry:     gs.connRegistry,
		SessRegistry:     gs.sessRegistry,
		LocalRouter:      gs.localRouter,
		DistRouter:       gs.distRouter,
		HeartbeatTimeout: 30 * time.Second,
		HandshakeTimeout: 30 * time.Second,
		MaxBodySize:      2 << 20,
		SelfAddr:         cfg.Gateway.Addr,
	})

	return gs, nil
}
