package session

import (
	"context"
	"sync"
	"time"

	"github.com/vadam-zhan/long-gw/gateway/internal/connection"
	"github.com/vadam-zhan/long-gw/gateway/internal/connector/upstream"
	"github.com/vadam-zhan/long-gw/gateway/internal/handler"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/router"
	"github.com/vadam-zhan/long-gw/gateway/internal/svc"
	"github.com/vadam-zhan/long-gw/gateway/internal/transport"
	"github.com/vadam-zhan/long-gw/gateway/internal/utils/kafka"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker/storage"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Session struct {
	ctx    context.Context
	cancel context.CancelFunc

	localRouter        *router.LocalRouter
	distributionRouter *router.DistributedRouter

	// Worker 管理器
	workerManager *worker.WorkerManager

	// 连接工厂
	connectionFactory *connection.ConnectionFactory

	// Kafka相关
	upstreamTopicManager *kafka.TopicManager

	// 连接注册表
	connRegistry *ConnectionRegistry

	// 下行消费者
	kafkaConsumer *kafka.Consumer

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

	// 初始化离线存储
	var offlineStore storage.OfflineStore
	if svc.DB != nil {
		offlineStore = storage.NewMySQLStore(svc.DB)
	}

	// 初始化 WorkerManager
	sess.workerManager = worker.NewWorkerManager(sess.distributionRouter, offlineStore)

	// 初始化连接注册表
	sess.connRegistry = NewConnectionRegistry()
	sess.workerManager.SetConnectionRegistry(sess.connRegistry)

	// 获取业务类型列表
	var businessTypes []string
	switch svc.Config.Upstream.Kind {
	case "kafka":
		sess.upstreamTopicManager = kafka.NewTopicManager(&svc.Config.Upstream.Kafka)
		businessTypes = sess.upstreamTopicManager.BusinessTypes()
	case "grpc":
	}

	// 创建 Worker pools
	senderFactory := upstream.NewSenderFactory(svc.Config.Upstream)
	sess.workerManager.CreatePools(businessTypes, senderFactory)

	// 启动 Worker pools
	sess.workerManager.Start(sess.ctx)

	// Kafka consumer 初始化
	if sess.upstreamTopicManager != nil {
		downstreamTopics := sess.upstreamTopicManager.GetAllDownstreamTopics()
		if len(downstreamTopics) > 0 {
			sess.kafkaConsumer = kafka.NewConsumer(&svc.Config.Upstream.Kafka, sess.workerManager, downstreamTopics)
			sess.kafkaConsumer.Start(sess.ctx)
		}
	}

	// 初始化连接工厂
	sess.connectionFactory = connection.NewConnectionFactory(
		sess.localRouter,
		sess.distributionRouter,
		sess.workerManager.GetUpstreamSender(),
	)

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

	tp := transport.NewWSTransport(rawConn)
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

	// 使用连接工厂创建连接
	conn := s.connectionFactory.CreateConnection(tp)

	// 注册到连接注册表
	s.connRegistry.Register(conn.GetConnID(), conn)

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
	s.connRegistry.Unregister(conn.GetConnID())
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
	if s.workerManager != nil {
		s.workerManager.Stop()
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