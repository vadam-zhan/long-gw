package main

import (
	"flag"
	"os"

	business "github.com/vadam-zhan/long-gw/business/internal"
	"github.com/vadam-zhan/long-gw/business/internal/config"
	"github.com/vadam-zhan/long-gw/business/internal/logger"
	"go.uber.org/zap"
)

var configFile = flag.String("f", "etc/config.yaml", "the config file")

func main() {
	flag.Parse()

	// 加载配置
	cfg, err := config.Load(*configFile)
	if err != nil {
		logger.Error("failed to load config", zap.Error(err))
		os.Exit(1)
	}

	// 初始化日志
	logger.Init(cfg.Log.Level, cfg.Log.Format, cfg.Log.File)

	// 创建并运行 Business 服务
	server := business.NewServer(cfg)
	server.Run()
}
