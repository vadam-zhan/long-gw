package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/config"
	"github.com/vadam-zhan/long-gw/gateway/internal/connection"
	connection_handler "github.com/vadam-zhan/long-gw/gateway/internal/connection/handler"
	"github.com/vadam-zhan/long-gw/gateway/internal/handler"
	gatewaygrpc "github.com/vadam-zhan/long-gw/gateway/internal/handler/gatewaygrpc"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/logic/codec"
	"github.com/vadam-zhan/long-gw/gateway/internal/metrics"
	"github.com/vadam-zhan/long-gw/gateway/internal/pipeline/uplink"
	"github.com/vadam-zhan/long-gw/gateway/internal/router"
	"github.com/vadam-zhan/long-gw/gateway/internal/session"
	"github.com/vadam-zhan/long-gw/gateway/internal/svc"
	"github.com/vadam-zhan/long-gw/gateway/internal/transport"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker/storage"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker/upstream"

	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"
)

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
	gs.connRegistry = connection.NewRegistry()

	// 初始化会话注册表
	gs.sessRegistry = session.NewRegistry(
		session.WithLocalRouter(gs.localRouter),
		session.WithOfflineStore(gs.offlineStore),
		session.SetSuspendTTL(cfg.Session.SuspendTTL),
		session.WithServiceContext(gs.svc),
	)

	gs.workerManager = worker.NewManager(gs.ctx)
	if err := gs.registerWorkerPools(); err != nil {
		cancel()
		return nil, fmt.Errorf("gateway: worker pools: %w", err)
	}

	gs.codec = codec.NewCodec(codec.DefaultConfig())

	uplinkChain := uplink.BuildChain(nil) // nil = no rate limiter in dev; inject real limiter in prod
	chainAdapter := uplink.NewChainAdapter(uplinkChain)

	authVerifier := connection_handler.NewAuthVerifier(cfg.Auth.Addr)
	handlerReg := connection_handler.Build(authVerifier, chainAdapter)

	// 初始化 connectionFactory
	gs.connFactory = connection.NewFactory(&connection.FactoryDeps{
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

	// gs.adminHandler = handler.NewAdminHandler(sess)
	gs.wsHandler = handler.NewWsHandler()

	return gs, nil
}

// Start 启动网关服务
func (s *GatewayServer) Start() error {
	var err error
	var wg sync.WaitGroup
	slog.Info("gateway start", "addr", s.svc.Config.Gateway.Addr, "max_conn", s.svc.Config.Gateway.MaxConnNum)

	// 目前只支持 tcp 协议，不支持 udp 协议
	s.listener, err = net.Listen("tcp", s.svc.Config.Gateway.Addr)
	if err != nil {
		return err
	}
	s.cmux = cmux.New(s.listener)
	grpcListener := s.cmux.Match(cmux.HTTP2HeaderField("content-type", "application/grpc"))
	httpListener := s.cmux.Match(cmux.HTTP1Fast())
	tcpListener := s.cmux.Match(cmux.Any())

	wg.Add(1)
	wg.Go(func() {
		defer wg.Done()
		// 设置 HTTP/WS 路由
		ginEngine := s.SetupGinRouter()
		httpSrv := &http.Server{Handler: ginEngine.Handler()}
		if err := httpSrv.Serve(httpListener); err != nil && err != http.ErrServerClosed {
			slog.Error("http serve failed", "error", err)
		}
	})

	wg.Add(1)
	wg.Go(func() {
		defer wg.Done()
		// TCP 原始协议服务
		s.acceptTCP(tcpListener)
	})

	wg.Add(1)
	wg.Go(func() {
		defer wg.Done()
		// gRPC 服务
		s.serveGRPC(grpcListener)
	})

	// 启动 cmux（阻塞直到服务关闭）
	go func() {
		if err := s.cmux.Serve(); err != nil {
			slog.Error("cmux serve failed", "error", err)
		}
	}()

	// 启动后台任务
	s.cleanTimeoutLoop()

	// 启动 pprof 服务器
	if s.svc.Config.Gateway.Profile.Enabled {
		wg.Add(1)
		wg.Go(func() {
			defer wg.Done()
			slog.Info("pprof server started", "addr", s.svc.Config.Gateway.Profile.Addr)
			if err := http.ListenAndServe(s.svc.Config.Gateway.Profile.Addr, nil); err != nil {
				slog.Error("pprof serve failed", "error", err)
			}
		})
	}

	// 启动 Prometheus metrics 服务器
	if s.svc.Config.Gateway.Metrics.Enabled {
		s.metricsCollector = metrics.NewCollector(s.svc.Config.Gateway.Metrics.Addr)
		s.metricsCollector.Start(s.ctx)
	}

	// 等待系统信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("gateway server shutting down...")
	s.Stop()

	wg.Wait()
	slog.Info("gateway server stopped")
	return nil
}

// acceptTCP 接受TCP连接
func (s *GatewayServer) acceptTCP(listener net.Listener) {
	slog.Info("tcp acceptor started")
	for {
		select {
		case <-s.ctx.Done():
			slog.Info("tcp acceptor stopped")
			return
		default:
			rawConn, err := listener.Accept()
			if err != nil {
				slog.Error("tcp accept failed", "error", err)
				continue
			}

			traceID := logger.GenerateTraceID()
			ctx := context.WithValue(s.ctx, logger.TraceIDKey, traceID)
			slog.DebugContext(ctx, "new tcp connection", "remote", rawConn.RemoteAddr().String())

			go s.connFactory.CreateAndRun(ctx, transport.NewTCPTransport(rawConn))
		}
	}
}

// SetupGinRouter 配置HTTP路由
func (s *GatewayServer) SetupGinRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	r.Use(logger.TraceMiddleware())

	// 管理接口
	adminG := r.Group("/v1/admin")
	{
		adminG.GET("/health", s.adminHandler.HealthHandler)
		adminG.GET("/stats", s.adminHandler.StatsHandler)
		adminG.POST("/kick", s.adminHandler.KickHandler)
	}

	// WebSocket接口
	wgG := r.Group("/v1/ws")
	{
		wgG.GET("/connect", s.wsUpgradeHandler)
	}

	return r
}

func (s *GatewayServer) wsUpgradeHandler(c *gin.Context) {
	rawConn := s.wsHandler.UpgradeHandler(c)
	if rawConn == nil {
		return
	}

	tp := transport.NewWSTransport(rawConn)
	slog.Info("new ws connection", "remote", tp.RemoteAddr())

	go s.connFactory.CreateAndRun(s.ctx, tp)
}

// serveGRPC 启动 gRPC 服务
func (s *GatewayServer) serveGRPC(listener net.Listener) {
	slog.Info("grpc server started")

	grpcSrv := grpc.NewServer()
	gateway.RegisterGatewayServer(grpcSrv, gatewaygrpc.NewGrpcServer())

	if err := grpcSrv.Serve(listener); err != nil {
		slog.Error("grpc serve failed", "error", err)
	}
}

// cleanTimeoutLoop 清理session中超时连接
func (s *GatewayServer) cleanTimeoutLoop() {

}

// Stop 优雅关闭网关服务
func (s *GatewayServer) Stop() {
	s.cancel()
	// 关闭 metrics 收集器
	if s.metricsCollector != nil {
		s.metricsCollector.Stop()
	}

	// 关闭监听
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.cmux != nil {
		s.cmux.Close()
	}
}

func (gs *GatewayServer) registerWorkerPools() error {
	for bizCode, wcfg := range gs.svc.Config.Workers {
		var sender types.UpstreamSender
		switch wcfg.UpstreamSender {
		case "kafka", "":
			sender = upstream.NewKafkaSender(gs.svc.Config.Upstream.Kafka.Brokers, gs.svc.Config.Upstream.Kafka.BusinessTopics[bizCode].UpstreamTopic)
		default:
			return fmt.Errorf("unknown upstream_sender %q for biz %s", wcfg.UpstreamSender, bizCode)
		}

		// Resolver: LocalRouter implements the SessionResolver interface
		// expected by WorkerPool's downstreamWorker (FanOut path).
		gs.workerManager.AddPool(worker.PoolConfig{
			BizCode:           bizCode,
			UpstreamWorkers:   wcfg.UpstreamWorkers,
			UpstreamChanCap:   wcfg.UpstreamChanCap,
			DownstreamWorkers: wcfg.DownstreamWorkers,
			DownstreamChanCap: wcfg.DownstreamChanCap,
			UpstreamSender:    sender,
			OfflineStore:      gs.offlineStore,
			Resolver:          gs.localRouter, // resolves To → []SessionTarget
		})
	}
	return nil
}
