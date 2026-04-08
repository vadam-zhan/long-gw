package business

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/vadam-zhan/long-gw/business/internal/config"
	"github.com/vadam-zhan/long-gw/business/internal/logger"
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"go.uber.org/zap"
)

// Server 业务服务主结构
type Server struct {
	cfg        *config.Config
	engine     *gin.Engine
	httpServer *http.Server
	consumer   *Consumer
	sender     *Sender
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// NewServer 创建业务服务实例
func NewServer(cfg *config.Config) *Server {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(ginLogger())

	ctx, cancel := context.WithCancel(context.Background())

	return &Server{
		cfg:    cfg,
		engine: engine,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start 启动业务服务
func (s *Server) Start() error {
	logger.Info("starting business server",
		zap.Strings("kafka_brokers", s.cfg.Kafka.Brokers),
		zap.String("http_addr", s.cfg.Business.Addr))

	// 初始化 Kafka 生产者
	s.sender = NewSender(s.cfg.Kafka.Brokers)

	// 初始化 Kafka 消费者
	upstreamTopics := s.getUpstreamTopics()
	s.consumer = NewConsumer(s.cfg.Kafka.Brokers, upstreamTopics)

	// 启动 Kafka 消费者
	if err := s.consumer.Start(s.ctx, s.handleUpstreamMessage); err != nil {
		return err
	}

	// 注册 HTTP 路由
	s.registerRoutes()

	// 启动 HTTP 服务器
	s.httpServer = &http.Server{
		Addr:    s.cfg.Business.Addr,
		Handler: s.engine,
	}

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server error", zap.Error(err))
		}
	}()

	logger.Info("business server started", zap.String("addr", s.cfg.Business.Addr))
	return nil
}

// registerRoutes 注册 HTTP 路由
func (s *Server) registerRoutes() {
	// 健康检查
	s.engine.GET("/health", s.handleHealth)

	// 业务 API 路由组
	api := s.engine.Group("/api/v1")
	{
		// 上行消息推送接口（供外部业务系统调用，直接发送下行消息到 Kafka）
		api.POST("/push", s.handlePushMessage)

		// 批量推送
		api.POST("/push/batch", s.handleBatchPush)
	}
}

// getUpstreamTopics 获取所有上行 topic
func (s *Server) getUpstreamTopics() []string {
	topics := make([]string, 0, len(s.cfg.Kafka.BusinessTopics))
	for _, tp := range s.cfg.Kafka.BusinessTopics {
		topics = append(topics, tp.UpstreamTopic)
	}
	return topics
}

// handleUpstreamMessage 处理上行消息
// 从 Kafka 消费上行消息，进行业务处理后，可能需要推送下行消息
func (s *Server) handleUpstreamMessage(msg *gateway.UpstreamKafkaMessage) error {
	logger.Info("========== Received Upstream Message ==========",
		zap.String("correlation_id", msg.CorrelationId),
		zap.String("conn_id", msg.ConnId),
		zap.String("user_id", msg.UserId),
		zap.String("device_id", msg.DeviceId),
		zap.String("business_type", msg.BusinessType.String()),
		zap.String("original_type", msg.OriginalType.String()),
		zap.Int64("timestamp", msg.Timestamp),
		zap.ByteString("payload", msg.Payload))

	// 构造下行响应消息
	replyPayload := fmt.Sprintf(`{"ack":"ok","original_correlation_id":"%s","reply_time":%d}`, msg.CorrelationId, time.Now().UnixMilli())
	downstreamMsg := &gateway.DownstreamKafkaMessage{
		CorrelationId: fmt.Sprintf("%d", time.Now().UnixNano()),
		ConnId:        msg.ConnId,
		UserId:        msg.UserId,
		DeviceId:      msg.DeviceId,
		TargetType:    gateway.SignalType_SIGNAL_TYPE_MESSAGE_ACK,
		Payload:       []byte(replyPayload),
		Timestamp:     time.Now().UnixMilli(),
		BusinessType:  msg.BusinessType,
	}

	// 发送到下游
	topic := s.getDownstreamTopic(msg.BusinessType)
	if topic == "" {
		logger.Warn("no downstream topic for business type",
			zap.String("business_type", msg.BusinessType.String()))
		return nil
	}

	logger.Info("========== Sending Downstream Reply ==========",
		zap.String("topic", topic),
		zap.String("correlation_id", downstreamMsg.CorrelationId),
		zap.String("original_correlation_id", msg.CorrelationId),
		zap.String("payload", replyPayload))

	return s.sender.Send(s.ctx, topic, downstreamMsg)
}

// getDownstreamTopic 根据业务类型获取下行 topic
func (s *Server) getDownstreamTopic(bizType gateway.BusinessType) string {
	bizTypeName := bizType.String()
	if tp, ok := s.cfg.Kafka.BusinessTopics[bizTypeName]; ok {
		return tp.DownstreamTopic
	}
	return ""
}

// handleHealth 健康检查
func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"time":   time.Now().Unix(),
	})
}

// PushRequest 推送消息请求
type PushRequest struct {
	ConnID       string               `json:"conn_id" binding:"required"`
	UserID       string               `json:"user_id" binding:"required"`
	DeviceID     string               `json:"device_id" binding:"required"`
	BusinessType gateway.BusinessType `json:"business_type" binding:"required"`
	TargetType   gateway.SignalType   `json:"target_type" binding:"required"`
	Payload      []byte               `json:"payload" binding:"required"`
}

// handlePushMessage 处理推送消息请求
func (s *Server) handlePushMessage(c *gin.Context) {
	var req PushRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	topic := s.getDownstreamTopic(req.BusinessType)
	if topic == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported business type"})
		return
	}

	msg := &gateway.DownstreamKafkaMessage{
		CorrelationId: fmt.Sprintf("%d", time.Now().UnixNano()),
		ConnId:        req.ConnID,
		UserId:        req.UserID,
		DeviceId:      req.DeviceID,
		TargetType:    req.TargetType,
		Payload:       req.Payload,
		Timestamp:     time.Now().UnixMilli(),
		BusinessType:  req.BusinessType,
	}

	if err := s.sender.Send(s.ctx, topic, msg); err != nil {
		logger.Error("failed to push message", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to push message"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":         "ok",
		"correlation_id": msg.CorrelationId,
	})
}

// BatchPushRequest 批量推送请求
type BatchPushRequest struct {
	Messages []PushRequest `json:"messages" binding:"required,min=1,max=100"`
}

// handleBatchPush 处理批量推送请求
func (s *Server) handleBatchPush(c *gin.Context) {
	var req BatchPushRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	results := make([]struct {
		Index  int    `json:"index"`
		Status string `json:"status"`
		Error  string `json:"error,omitempty"`
	}, 0, len(req.Messages))

	for i, pushReq := range req.Messages {
		topic := s.getDownstreamTopic(pushReq.BusinessType)
		if topic == "" {
			results = append(results, struct {
				Index  int    `json:"index"`
				Status string `json:"status"`
				Error  string `json:"error,omitempty"`
			}{i, "error", "unsupported business type"})
			continue
		}

		msg := &gateway.DownstreamKafkaMessage{
			CorrelationId: fmt.Sprintf("%d", time.Now().UnixNano()),
			ConnId:        pushReq.ConnID,
			UserId:        pushReq.UserID,
			DeviceId:      pushReq.DeviceID,
			TargetType:    pushReq.TargetType,
			Payload:       pushReq.Payload,
			Timestamp:     time.Now().UnixMilli(),
			BusinessType:  pushReq.BusinessType,
		}

		if err := s.sender.Send(s.ctx, topic, msg); err != nil {
			results = append(results, struct {
				Index  int    `json:"index"`
				Status string `json:"status"`
				Error  string `json:"error,omitempty"`
			}{i, "error", err.Error()})
			continue
		}

		results = append(results, struct {
			Index  int    `json:"index"`
			Status string `json:"status"`
			Error  string `json:"error,omitempty"`
		}{i, "ok", ""})
	}

	c.JSON(http.StatusOK, gin.H{
		"total":   len(req.Messages),
		"results": results,
	})
}

// Stop 停止业务服务
func (s *Server) Stop() error {
	logger.Info("stopping business server")

	s.cancel()

	// 关闭 HTTP 服务器
	if s.httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("http server shutdown error", zap.Error(err))
		}
	}

	// 关闭消费者
	if s.consumer != nil {
		if err := s.consumer.Stop(); err != nil {
			logger.Error("failed to stop consumer", zap.Error(err))
		}
	}

	// 关闭生产者
	if s.sender != nil {
		s.sender.Close()
	}

	logger.Info("business server stopped")
	return nil
}

// Run 运行服务（阻塞）
func (s *Server) Run() {
	// 启动服务
	if err := s.Start(); err != nil {
		logger.Error("failed to start business server", zap.Error(err))
		os.Exit(1)
	}

	logger.Info("business service running...")

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down business service...")

	// 停止服务
	if err := s.Stop(); err != nil {
		logger.Error("failed to stop business server", zap.Error(err))
		os.Exit(1)
	}
}

// ginLogger Gin 日志中间件
func ginLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		c.Next()

		if raw != "" {
			path = path + "?" + raw
		}

		logger.Info("gin request",
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("ip", c.ClientIP()),
			zap.Duration("latency", time.Since(start)))
	}
}
