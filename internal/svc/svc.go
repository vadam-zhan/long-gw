package svc

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/vadam-zhan/long-gw/internal/config"
	"github.com/vadam-zhan/long-gw/internal/logger"
	"go.uber.org/zap"
)

type ServiceContext struct {
	context.Context
	Config      *config.Config
	RedisClient *redis.Client
}

func NewServiceContext(ctx context.Context, c *config.Config) *ServiceContext {
	svc := &ServiceContext{
		Context: ctx,
		Config:  c,
	}

	// 初始化Redis客户端
	if c.Redis.Addr != "" {
		svc.RedisClient = redis.NewClient(&redis.Options{
			Addr:     c.Redis.Addr,
			Password: c.Redis.Password,
			DB:       c.Redis.DB,
			PoolSize: c.Redis.PoolSize,
		})

		// 测试连接
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := svc.RedisClient.Ping(ctx).Err(); err != nil {
			logger.Warn("redis connection failed, distributed router disabled",
				zap.Error(err),
				zap.String("addr", c.Redis.Addr))
			svc.RedisClient = nil
		} else {
			logger.Info("redis connected",
				zap.String("addr", c.Redis.Addr))
		}
	}

	return svc
}

func (s *ServiceContext) Close() {
	if s.RedisClient != nil {
		s.RedisClient.Close()
	}
}
