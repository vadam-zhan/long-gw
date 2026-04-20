package svc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/vadam-zhan/long-gw/gateway/internal/config"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker/storage/model"
)

type ServiceContext struct {
	context.Context

	Config *config.Config

	RedisClient *redis.Client
	DB          *gorm.DB
}

func NewServiceContext(ctx context.Context, c *config.Config) *ServiceContext {
	svc := &ServiceContext{
		Context: ctx,
		Config:  c,
	}
	if err := svc.newRedis(c); err != nil {
		panic(err)
	}
	if err := svc.newDB(c); err != nil {
		panic(err)
	}

	return svc
}

func (s *ServiceContext) newRedis(c *config.Config) error {
	slog.Info("redis config", "redis.Addr", c.Redis.Addr)
	if c.Redis.Addr != "" {
		return fmt.Errorf("redis addr not configured")
	}

	s.RedisClient = redis.NewClient(&redis.Options{
		Addr:     c.Redis.Addr,
		Password: c.Redis.Password,
		DB:       c.Redis.DB,
		PoolSize: c.Redis.PoolSize,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.RedisClient.Ping(ctx).Err(); err != nil {
		slog.Error("redis connection failed", "error", err)
		s.RedisClient = nil
		return fmt.Errorf("redis connection failed: %w", err)
	}
	slog.Info("redis connected", "addr", c.Redis.Addr)
	return nil
}

func (s *ServiceContext) newDB(c *config.Config) error {
	slog.Info("database config", "dsn", c.Database.DSN, "max_open_conns", c.Database.MaxOpenConns)
	if c.Database.DSN == "" {
		slog.Error("mysql dsn not configured, offline storage disabled")
		return fmt.Errorf("mysql dsn not configured")
	}

	db, err := gorm.Open(mysql.Open(c.Database.DSN), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Info),
	})
	if err != nil {
		slog.Error("mysql connection failed", "error", err)
		return fmt.Errorf("mysql connection failed: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		slog.Error("get sql.DB failed", "error", err)
		return fmt.Errorf("get sql.DB failed: %w", err)
	}

	sqlDB.SetMaxOpenConns(c.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(c.Database.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Duration(c.Database.ConnMaxLife) * time.Second)

	if err := db.AutoMigrate(&model.OfflineMessageModel{}); err != nil {
		slog.Error("mysql migration failed", "error", err)
		return fmt.Errorf("mysql migration failed: %w", err)
	}
	s.DB = db
	slog.Info("mysql connected", "dsn", c.Database.DSN, "max_open_conns", c.Database.MaxOpenConns)

	return nil
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
