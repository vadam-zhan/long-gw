package cmd

import (
	"context"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync"
	"syscall"

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
	"github.com/vadam-zhan/long-gw/gateway/internal/router"
	"github.com/vadam-zhan/long-gw/gateway/internal/session"
	"github.com/vadam-zhan/long-gw/gateway/internal/svc"
	"github.com/vadam-zhan/long-gw/gateway/internal/transport"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker/storage"

	"github.com/soheilhy/cmux"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// NewGatewayServer 创建网关服务器
func NewGatewayServer(cfg *config.Config) *GatewayServer {
	ctx, cancel := context.WithCancel(context.Background())
	gs := &GatewayServer{
		ctx:    ctx,
		cancel: cancel,
	}
	svc := svc.NewServiceContext(ctx, cfg)
	gs.svc = svc
	gs.localRouter = router.NewLocalRouter()
	gs.distRouter = router.NewDistributedRouter(svc.RedisClient, cfg.Gateway.Addr)

	offlineStore := storage.NewMySQLStore(svc.DB)

	gs.sessionRegistry = session.NewSessionRegistry(
		session.WithLocalRouter(gs.localRouter),
		// session.WithDistRouter(gs.distRouter),
		session.WithOfflineStore(offlineStore),
		session.SetSuspendTTL(cfg.Session.SuspendTTL),
		session.WithServiceContext(gs.svc),
	)

	gs.workerManager = worker.NewManager()
	for bizCode, poolCfg := range cfg.Workers {
		gs.workerManager.CreatePool(&worker.PoolConfig{
			BizCode:           bizCode,
			UpstreamWorkers:   poolCfg.UpstreamWorkers,
			DownstreamWorkers: poolCfg.DownstreamWorkers,
			UpstreamChanCap:   poolCfg.UpstreamChanCap,
			DownstreamChanCap: poolCfg.DownstreamChanCap,
			OfflineStore:      offlineStore,
			Router:            gs.localRouter,
		})
	}

	gs.ackRetrier = &session.AckRetrier{}

	gs.codec = codec.NewCodec(codec.DefaultConfig())

	// 初始化 connectionFactory
	gs.connFactory = connection.NewFactory(&connection.FactoryDeps{
		Codec:        gs.codec,
		ConnRegistry: connection.NewRegistry(),
		HandlerReg:   connection_handler.NewRegistry(connection_handler.NewAuthVerifier(cfg.Auth.Addr)),
	})

	// gs.adminHandler = handler.NewAdminHandler(sess)
	gs.wsHandler = handler.NewWsHandler()

	return gs
}

// Start 启动网关服务
func (s *GatewayServer) Start() error {
	var err error
	var wg sync.WaitGroup
	logger.Info("gateway start",
		zap.String("addr", s.svc.Config.Gateway.Addr),
		zap.Uint64("max_conn", s.svc.Config.Gateway.MaxConnNum),
		zap.Int("upstream_worker_num", s.svc.Config.Gateway.UpstreamWorkerNum),
		zap.Int("downstream_worker_num", s.svc.Config.Gateway.DownstreamWorkerNum))

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
			logger.Error("http serve failed", zap.Error(err))
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
			logger.Error("cmux serve failed", zap.Error(err))
		}
	}()

	// 启动后台任务
	s.cleanTimeoutLoop()

	// 启动 pprof 服务器
	if s.svc.Config.Gateway.Profile.Enabled {
		wg.Add(1)
		wg.Go(func() {
			defer wg.Done()
			logger.Info("pprof server started", zap.String("addr", s.svc.Config.Gateway.Profile.Addr))
			if err := http.ListenAndServe(s.svc.Config.Gateway.Profile.Addr, nil); err != nil {
				logger.Error("pprof serve failed", zap.Error(err))
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

	logger.Info("gateway server shutting down...")
	s.Stop()

	// 等待所有服务退出
	wg.Wait()
	logger.Info("gateway server stopped")
	return nil
}

// acceptTCP 接受TCP连接
func (s *GatewayServer) acceptTCP(listener net.Listener) {
	logger.Info("tcp acceptor started")
	for {
		select {
		case <-s.ctx.Done():
			logger.Info("tcp acceptor stopped")
			return
		default:
			rawConn, err := listener.Accept()
			if err != nil {
				logger.Error("tcp accept failed", zap.Error(err))
				continue
			}
			logger.Debug("new tcp connection", zap.String("remote", rawConn.RemoteAddr().String()))

			// 使用独立goroutine处理，HandleConnection 会管理自己的生命周期
			go s.HandleConnection(transport.NewTCPTransport(rawConn))
		}
	}
}

// SetupGinRouter 配置HTTP路由
func (s *GatewayServer) SetupGinRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

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
	logger.Info("new ws connection", zap.String("remote", tp.RemoteAddr()))
	go s.HandleConnection(tp)
}

func (s *GatewayServer) HandleConnection(tp transport.Transport) {

	conn, err := s.connFactory.Create(s.ctx, tp)
	if err != nil {
		return
	}

	s.connFactory.Run(s.ctx, conn)

	sess := s.sessionRegistry.GetOrCreate(conn.UserID, conn.DeviceID)

	// 处理踢人（同设备旧 Session）
	if oldConn := sess.GetConn(); oldConn != nil {
		oldConn.Close(&gateway.KickPayload{Code: 4002, Reason: "login from another device"})
	}

	sess.AttachConn(conn)

}

// serveGRPC 启动 gRPC 服务
func (s *GatewayServer) serveGRPC(listener net.Listener) {
	logger.Info("grpc server started")

	grpcSrv := grpc.NewServer()
	gateway.RegisterGatewayServer(grpcSrv, gatewaygrpc.NewGrpcServer())

	if err := grpcSrv.Serve(listener); err != nil {
		logger.Error("grpc serve failed", zap.Error(err))
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
