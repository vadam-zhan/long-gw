package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	gatewayv1 "github.com/vadam-zhan/long-gw/common-protocol/gen/gateway/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/access/connruntime"
	"github.com/vadam-zhan/long-gw/gateway/internal/access/tcp"
	accessws "github.com/vadam-zhan/long-gw/gateway/internal/access/websocket"
	"github.com/vadam-zhan/long-gw/gateway/internal/handler"
	"github.com/vadam-zhan/long-gw/gateway/internal/handler/gatewaygrpc"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"
)

// ********************************************
// ********************************************
// **************** Tcp Module ***************
// ********************************************

func NewTcpModule(cmux cmux.CMux, connFactory *connruntime.Factory) *TcpModule {
	return &TcpModule{
		cmux:        cmux,
		connFactory: connFactory,
	}
}

func (tm *TcpModule) Start(ctx context.Context) error {
	if tm.cmux == nil || tm.connFactory == nil {
		return fmt.Errorf("cmux or connFactory is nil")
	}
	listener := tm.cmux.Match(cmux.Any())
	slog.Info("tcp acceptor started")
	for {
		select {
		case <-ctx.Done():
			slog.Info("tcp acceptor stopped")
			return nil
		default:
			rawConn, err := listener.Accept()
			if err != nil {
				slog.Error("tcp accept failed", "error", err)
				continue
			}

			traceID := logger.GenerateTraceID()
			ctx := context.WithValue(ctx, logger.TraceIDKey, traceID)
			slog.DebugContext(ctx, "new tcp connection", "remote", rawConn.RemoteAddr().String())

			go tm.connFactory.CreateAndRun(ctx, tcp.NewTCPTransport(rawConn))
		}
	}
}

func (tm *TcpModule) Stop(ctx context.Context) error {
	return nil
}

// ********************************************
// ********************************************
// **************** Http Module ***************
// ********************************************

func NewHttpModule(ctx context.Context, cmux cmux.CMux, connFactory *connruntime.Factory) *HttpModule {
	return &HttpModule{
		ctx:         ctx,
		cmux:        cmux,
		connFactory: connFactory,
		wsUpgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

func (hm *HttpModule) Start(ctx context.Context) error {
	if hm.cmux == nil || hm.connFactory == nil {
		return fmt.Errorf("cmux or connFactory is nil")
	}

	listener := hm.cmux.Match(cmux.HTTP1Fast())

	ginEngine := hm.setupGinRouter()
	httpSrv := &http.Server{Handler: ginEngine.Handler()}
	if err := httpSrv.Serve(listener); err != nil && err != http.ErrServerClosed {
		slog.Error("http serve failed", "error", err)
		return err
	}

	return nil
}

func (hm *HttpModule) Stop(ctx context.Context) error {
	return nil
}

// setupGinRouter 配置HTTP路由
func (hm *HttpModule) setupGinRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	r.Use(logger.TraceMiddleware())

	adminHandler := handler.NewAdminHandler(nil)

	// 管理接口
	adminG := r.Group("/v1/admin")
	{
		adminG.GET("/health", adminHandler.HealthHandler)
		adminG.GET("/stats", adminHandler.StatsHandler)
		adminG.POST("/kick", adminHandler.KickHandler)
	}

	// WebSocket接口
	wgG := r.Group("/v1/ws")
	{
		wgG.GET("/connect", hm.wsUpgradeHandler)
	}

	return r
}

func (hm *HttpModule) wsUpgradeHandler(c *gin.Context) {
	rawConn, err := hm.wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		slog.Error("ws upgrade failed", "error", err)
		return
	}

	tp := accessws.NewWSTransport(rawConn)
	slog.Info("new ws connection", "remote", tp.RemoteAddr())

	go hm.connFactory.CreateAndRun(hm.ctx, tp)
}

// ********************************************
// ********************************************
// **************** Grpc Module ***************
// ********************************************

func NewGrpcModule(cmux cmux.CMux) *GrpcModule {
	return &GrpcModule{
		cmux: cmux,
	}
}

func (gm *GrpcModule) Start(ctx context.Context) error {
	slog.Info("grpc server started")

	grpcSrv := grpc.NewServer()
	gatewayv1.RegisterGatewayServer(grpcSrv, gatewaygrpc.NewGrpcServer())

	listener := gm.cmux.Match(cmux.HTTP2HeaderField("content-type", "application/grpc"))
	if err := grpcSrv.Serve(listener); err != nil {
		slog.Error("grpc serve failed", "error", err)
	}

	return nil
}

func (gm *GrpcModule) Stop(ctx context.Context) error {
	return nil
}
