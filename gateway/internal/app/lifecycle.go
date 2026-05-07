package app

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/soheilhy/cmux"
	"github.com/vadam-zhan/long-gw/gateway/internal/metrics"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker/upstream"
)

// Start 启动网关服务
func (gs *GatewayServer) Start() error {
	var err error
	var wg sync.WaitGroup
	slog.Info("gateway start", "addr", gs.svc.Config.Gateway.Addr, "max_conn", gs.svc.Config.Gateway.MaxConnNum)

	// 目前只支持 tcp 协议，不支持 udp 协议
	gs.listener, err = net.Listen("tcp", gs.svc.Config.Gateway.Addr)
	if err != nil {
		return err
	}
	gs.cmux = cmux.New(gs.listener)
	gs.modules = append(gs.modules, NewGrpcModule(gs.cmux))
	gs.modules = append(gs.modules, NewHttpModule(gs.ctx, gs.cmux, gs.connFactory))
	gs.modules = append(gs.modules, NewTcpModule(gs.cmux, gs.connFactory))

	for _, module := range gs.modules {
		wg.Add(1)
		wg.Go(func() {
			defer wg.Done()
			module.Start(gs.ctx)
		})
	}

	// 启动 cmux（阻塞直到服务关闭）
	go func() {
		if err := gs.cmux.Serve(); err != nil {
			slog.Error("cmux serve failed", "error", err)
		}
	}()

	// 启动 Timer（分片时间轮）
	if gs.timer != nil {
		gs.timer.Start(gs.ctx)
		slog.Info("timer scheduler started")
	}

	// 启动后台任务
	gs.cleanTimeoutLoop()

	// 启动 pprof 服务器
	if gs.svc.Config.Gateway.Profile.Enabled {
		wg.Add(1)
		wg.Go(func() {
			defer wg.Done()
			slog.Info("pprof server started", "addr", gs.svc.Config.Gateway.Profile.Addr)
			if err := http.ListenAndServe(gs.svc.Config.Gateway.Profile.Addr, nil); err != nil {
				slog.Error("pprof serve failed", "error", err)
			}
		})
	}

	// 启动 Prometheus metrics 服务器
	if gs.svc.Config.Gateway.Metrics.Enabled {
		gs.metricsCollector = metrics.NewCollector(gs.svc.Config.Gateway.Metrics.Addr)
		gs.metricsCollector.Start(gs.ctx)
	}

	// 等待系统信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("gateway server shutting down...")
	gs.Stop()

	wg.Wait()
	slog.Info("gateway server stopped")
	return nil
}

// cleanTimeoutLoop 清理session中超时连接
func (gs *GatewayServer) cleanTimeoutLoop() {

}

// Stop 优雅关闭网关服务
func (gs *GatewayServer) Stop() {
	for _, m := range gs.modules {
		m.Stop(gs.ctx)
	}
	gs.cancel()

	if gs.metricsCollector != nil {
		gs.metricsCollector.Stop()
	}

	if gs.listener != nil {
		_ = gs.listener.Close()
	}
	if gs.cmux != nil {
		gs.cmux.Close()
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
