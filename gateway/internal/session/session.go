package session

import (
	"context"
	"sync"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/connector"
	"github.com/vadam-zhan/long-gw/gateway/internal/connector/transport"
	"github.com/vadam-zhan/long-gw/gateway/internal/connector/upstream"
	"github.com/vadam-zhan/long-gw/gateway/internal/handler"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/router"
	"github.com/vadam-zhan/long-gw/gateway/internal/svc"
	"github.com/vadam-zhan/long-gw/gateway/internal/utils/kafka"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Session struct {
	ctx    context.Context
	cancel context.CancelFunc

	localRouter        *router.LocalRouter
	distributionRouter *router.DistributedRouter

	// 业务 worker 池
	workerPools map[gateway.BusinessType]connector.WorkerPoolInterface

	// Kafka相关
	upstreamTopicManager *kafka.TopicManager

	kafkaConsumer    *kafka.Consumer
	downstreamRouter *connector.DownstreamRouter

	maxConnNum uint64
	connCount  uint
	countMux   sync.Mutex

	svc *svc.ServiceContext

	// handlers
	adminHandler *handler.AdminHandler
	wsHandler    *handler.WsHandler
}

func NewSession(svc *svc.ServiceContext) *Session {
	sess := &Session{}
	sess.ctx, sess.cancel = context.WithCancel(context.Background())
	sess.localRouter = router.NewLocalRouter()
	sess.distributionRouter = router.NewDistributedRouter(svc.RedisClient, svc.Config.Gateway.Addr)
	sess.maxConnNum = svc.Config.Gateway.MaxConnNum
	sess.svc = svc

	var businessTypes []string
	switch svc.Config.Upstream.Kind {
	case "kafka":
		// 初始化 TopicManager
		sess.upstreamTopicManager = kafka.NewTopicManager(&svc.Config.Upstream.Kafka)
		businessTypes = sess.upstreamTopicManager.BusinessTypes()

	case "grpc":

	}

	// 初始化 upstream sender factory
	senderFactory := upstream.NewSenderFactory(svc.Config.Upstream)

	// 初始化下行路由器
	sess.downstreamRouter = connector.NewDownstreamRouter()

	// 创建业务的 worker 池
	sess.workerPools = make(map[gateway.BusinessType]connector.WorkerPoolInterface)
	for _, btStr := range businessTypes {
		// 转换为 proto BusinessType
		bt := kafka.StringToProtoBusinessType(btStr)
		if bt == gateway.BusinessType_BusinessType_UNSPECIFIED {
			logger.Warn("unknown business type, skipping",
				zap.String("business_type", btStr))
			continue
		}

		// 根据配置创建对应类型的 upstream sender
		sender, err := senderFactory.CreateSender(btStr)
		if err != nil {
			logger.Error("failed to create upstream sender",
				zap.String("business_type", btStr),
				zap.Error(err))
			continue
		}
		sess.workerPools[bt] = connector.NewWorkerPool(
			sess.svc,
			bt,
			sender,
			sess.downstreamRouter,
		)
		sess.workerPools[bt].Start()
		logger.Info("worker pool started",
			zap.String("business_type", btStr),
			zap.String("sender_kind", sender.Kind().String()))
	}

	// Kafka consumer初始化 - 订阅所有下行 topics 下行，目前只支持 kafka 订阅的下行任务，如果是 grpc 调用，则这个服务需要提供 grpc 下行接口，而不是在这里处理
	if sess.upstreamTopicManager != nil {
		downstreamTopics := sess.upstreamTopicManager.GetAllDownstreamTopics()
		if len(downstreamTopics) > 0 {
			sess.kafkaConsumer = kafka.NewConsumer(&svc.Config.Upstream.Kafka, sess.workerPools, downstreamTopics)
			sess.kafkaConsumer.Start(sess.ctx)
			logger.Info("kafka consumer started",
				zap.Strings("topics", downstreamTopics))
		}
	}

	logger.Info("session created",
		zap.Int("upstream_worker_num", svc.Config.Gateway.UpstreamWorkerNum),
		zap.Int("downstream_worker_num", svc.Config.Gateway.DownstreamWorkerNum))

	// 初始化 handlers
	sess.adminHandler = handler.NewAdminHandler(sess)
	sess.wsHandler = handler.NewWsHandler()

	return sess
}

// GetLocalRouter 获取本地路由（实现 SessionAccessor 接口）
func (s *Session) GetLocalRouter() handler.LocalRouterAccessor {
	return s.localRouter
}

// SetupGinRouter 配置HTTP路由
func (s *Session) SetupGinRouter() *gin.Engine {
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

func (s *Session) wsUpgradeHandler(c *gin.Context) {
	rawConn := s.wsHandler.UpgradeHandler(c)
	if rawConn == nil {
		return
	}

	tp := s.wsHandler.CreateTransport(rawConn)
	logger.Info("new ws connection", zap.String("remote", tp.RemoteAddr()))
	go s.HandleConnection(tp)
}

// HandleConnection 处理新的连接请求
func (s *Session) HandleConnection(tp transport.Transport) {
	// session 连接数限制检查
	if uint64(s.GetConnCount()) >= s.maxConnNum {
		tp.Close()
		logger.Warn("connection rejected: limit reached", zap.String("remote", tp.RemoteAddr()))
		return
	}

	// 创建Session并设置路由
	conn := connector.NewConnection(tp)
	conn.SetRouters(s.localRouter, s.distributionRouter)
	conn.SetWorkerPool(s.workerPools)

	// 注册到下游路由器
	if s.downstreamRouter != nil {
		s.downstreamRouter.RegisterConnection(conn.GetConnID(), conn)
	}

	// 增加连接计数
	count := s.IncrConnCount()
	logger.Info("connection accepted",
		zap.String("remote", tp.RemoteAddr()),
		zap.String("conn_id", conn.GetConnID()),
		zap.Uint("conn_count", count))

	// 启动双goroutine处理
	go conn.WriteLoop()
	conn.ReadLoop()

	// ReadLoop结束后清理
	if s.downstreamRouter != nil {
		s.downstreamRouter.UnregisterConnection(conn.GetConnID())
	}
	count = s.DecrConnCount()
	logger.Info("connection closed",
		zap.String("remote", tp.RemoteAddr()),
		zap.Uint("conn_count", count))
	conn.Close()
}

// CleanTimeoutLoop 定期清理超时连接
func (s *Session) CleanTimeoutLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			cleaned := s.localRouter.CleanTimeout()
			if cleaned > 0 {
				logger.Info("cleaned timeout connections", zap.Int("count", cleaned))
			}
		}
	}
}

// Close 优雅关闭session
func (s *Session) Close() {
	s.cancel()

	// 停止worker池
	for _, wp := range s.workerPools {
		wp.Stop()
	}

	// 停止Kafka consumer
	if s.kafkaConsumer != nil {
		s.kafkaConsumer.Stop()
	}
}

// IncrConnCount atomically increments connection count
func (s *Session) IncrConnCount() uint {
	s.countMux.Lock()
	defer s.countMux.Unlock()
	s.connCount++
	return s.connCount
}

// DecrConnCount atomically decrements connection count
func (s *Session) DecrConnCount() uint {
	s.countMux.Lock()
	defer s.countMux.Unlock()
	s.connCount--
	return s.connCount
}

// GetConnCount returns current connection count
func (s *Session) GetConnCount() uint {
	s.countMux.Lock()
	defer s.countMux.Unlock()
	return s.connCount
}
