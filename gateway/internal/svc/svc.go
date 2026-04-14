package svc

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/vadam-zhan/long-gw/gateway/internal/config"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"go.uber.org/zap"
)

type ServiceContext struct {
	context.Context
	Config      *config.Config
	RedisClient *redis.Client
	DB          *gorm.DB
}

func NewServiceContext(ctx context.Context, c *config.Config) *ServiceContext {
	svc := &ServiceContext{
		Context: ctx,
		Config:  c,
	}

	// 初始化 Redis
	svc.initRedis(c)

	// 初始化 MySQL
	svc.initDatabase(c)

	return svc
}

func (s *ServiceContext) initRedis(c *config.Config) {
	if c.Redis.Addr != "" {
		s.RedisClient = redis.NewClient(&redis.Options{
			Addr:     c.Redis.Addr,
			Password: c.Redis.Password,
			DB:       c.Redis.DB,
			PoolSize: c.Redis.PoolSize,
		})

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.RedisClient.Ping(ctx).Err(); err != nil {
			logger.Warn("redis connection failed", zap.Error(err))
			s.RedisClient = nil
		} else {
			logger.Info("redis connected", zap.String("addr", c.Redis.Addr))
		}
	}
}

func (s *ServiceContext) initDatabase(c *config.Config) {
	if c.Database.DSN == "" {
		logger.Warn("mysql dsn not configured, offline storage disabled")
		return
	}

	db, err := gorm.Open(mysql.Open(c.Database.DSN), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Info),
	})
	if err != nil {
		logger.Error("mysql connection failed", zap.Error(err))
		return
	}

	sqlDB, err := db.DB()
	if err != nil {
		logger.Error("get sql.DB failed", zap.Error(err))
		return
	}

	sqlDB.SetMaxOpenConns(c.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(c.Database.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(c.Database.ConnMaxLife) * time.Second)

	s.DB = db
	logger.Info("mysql connected",
		zap.String("dsn", c.Database.DSN),
		zap.Int("max_open_conns", c.Database.MaxOpenConns))
}

func (s *ServiceContext) Close() {
	if s.RedisClient != nil {
		s.RedisClient.Close()
	}
	if s.DB != nil {
		sqlDB, _ := s.DB.DB()
		sqlDB.Close()
	}
}
