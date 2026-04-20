package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// 自定义业务指标
	connNum = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gateway_conn_num",
			Help: "Number of active connections",
		},
	)

	upstreamMsgTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gateway_upstream_msg_total",
			Help: "Total number of upstream messages",
		},
	)

	downstreamMsgTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gateway_downstream_msg_total",
			Help: "Total number of downstream messages",
		},
	)

	SessionCreated = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gateway_session_created",
			Help: "Total number of session created",
		},
	)
)

func init() {
	// 注册指标
	prometheus.MustRegister(
		connNum,
		upstreamMsgTotal,
		downstreamMsgTotal,
	)
}

// Collector 指标收集器
type Collector struct {
	addr   string
	server *http.Server
	stopCh chan struct{}
	wg     sync.WaitGroup

	// 上次收集时的 GC 状态，用于计算差值
	lastNumGC   uint32
	lastPauseNs uint64
}

var collector *Collector

// NewCollector 创建指标收集器
func NewCollector(addr string) *Collector {
	return &Collector{
		addr:   addr,
		stopCh: make(chan struct{}),
	}
}

// Start 启动指标收集和HTTP服务
func (c *Collector) Start(ctx context.Context) {
	collector = c

	// 创建 HTTP handler
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	// 添加健康检查端点
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	c.server = &http.Server{
		Addr:    c.addr,
		Handler: mux,
	}

	// 启动 HTTP 服务
	c.wg.Add(1)
	c.wg.Go(func() {
		defer c.wg.Done()
		slog.Info("metrics server started", "addr", c.addr)
		if err := c.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("metrics serve failed", "error", err)
		}
	})

	// 启动指标收集循环
	c.wg.Add(1)
	c.wg.Go(func() {
		defer c.wg.Done()
		c.collectLoop(ctx)
	})
}

// collectLoop 定期收集指标
func (c *Collector) collectLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("collect loop by ctx.Done() stopped")
			return
		case <-c.stopCh:
			slog.Info("collect loop by stopCh stopped")
			return
		case <-ticker.C:
			slog.Debug("collect loop tick")
			c.collectRuntimeMetrics()
		}
	}
}

// collectRuntimeMetrics 收集运行时指标
func (c *Collector) collectRuntimeMetrics() {

}

// Stop 停止收集器
func (c *Collector) Stop() error {
	close(c.stopCh)
	if c.server != nil {
		return c.server.Close()
	}
	c.wg.Wait()
	return nil
}

// IncConnNum 增加连接数
func IncConnNum() {
	connNum.Inc()
}

// DecConnNum 减少连接数
func DecConnNum() {
	connNum.Dec()
}

// IncUpstreamMsg 增加上行消息数
func IncUpstreamMsg() {
	upstreamMsgTotal.Inc()
}

// IncDownstreamMsg 增加下行消息数
func IncDownstreamMsg() {
	downstreamMsgTotal.Inc()
}

// IncSessionCreated 增加会话创建数
func IncSessionCreated() {
	SessionCreated.Inc()
}
