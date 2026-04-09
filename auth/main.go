package main

import (
	"flag"

	"github.com/vadam-zhan/long-gw/auth/internal/config"
	"github.com/vadam-zhan/long-gw/auth/internal/logger"
	"github.com/vadam-zhan/long-gw/auth/internal/server"
	"go.uber.org/zap"
)

var configFile = flag.String("f", "etc/config.yaml", "the config file")

func main() {
	flag.Parse()

	// 加载配置
	cfg, err := config.Load(*configFile)
	if err != nil {
		logger.Error("failed to load config", zap.Error(err))
		return
	}

	// 初始化日志
	logger.Init(cfg.Log.Level, cfg.Log.Format, cfg.Log.File)

	// 创建并运行 Auth 服务
	srv := server.NewAuthServer(cfg.Server.Addr)
	logger.Info("auth server starting", zap.String("addr", cfg.Server.Addr))
	if err := srv.Start(); err != nil {
		logger.Error("auth server error", zap.Error(err))
	}
}
