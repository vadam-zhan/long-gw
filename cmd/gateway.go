package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/vadam-zhan/long-gw/gateway"
	"github.com/vadam-zhan/long-gw/internal/config"
	"github.com/vadam-zhan/long-gw/internal/logger"
	"go.uber.org/zap"
)

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "start gateway server",
	Run: func(cmd *cobra.Command, args []string) {
		// 加载配置
		cfg, err := config.Load(cfgFile)
		if err != nil {
			logger.Fatal("failed to load config", zap.Error(err))
		}

		// 初始化日志
		logger.Init(cfg.Log.Level, cfg.Log.Format, cfg.Log.File)

		// 输出配置信息
		logger.Info("gateway config loaded",
			zap.String("addr", cfg.Gateway.Addr),
			zap.Uint64("max_conn", cfg.Gateway.MaxConnNum),
			zap.Int("upstream_worker_num", cfg.Gateway.UpstreamWorkerNum),
			zap.Int("downstream_worker_num", cfg.Gateway.DownstreamWorkerNum))
		if cfg.Redis.Addr != "" {
			logger.Info("redis config",
				zap.String("addr", cfg.Redis.Addr))
		}

		// 创建并启动网关
		gw := gateway.NewGatewayServer(cfg)
		if err := gw.Start(); err != nil {
			logger.Fatal("gateway start failed", zap.Error(err))
		}
	},
}

func init() {
	rootCmd.AddCommand(gatewayCmd)

	// Gateway相关flag
	gatewayCmd.Flags().String("addr", ":8080", "gateway listen address")
	gatewayCmd.Flags().Int("upstream-worker-num", 50, "upstream worker pool size")
	gatewayCmd.Flags().Int("downstream-worker-num", 50, "downstream worker pool size")
	gatewayCmd.Flags().Uint64("max-conn", 1000000, "max connection number")

	// 绑定flag到viper(用于环境变量覆盖)
	viper.BindPFlag("gateway.addr", gatewayCmd.Flags().Lookup("addr"))
	viper.BindPFlag("gateway.upstream_worker_num", gatewayCmd.Flags().Lookup("upstream-worker-num"))
	viper.BindPFlag("gateway.downstream_worker_num", gatewayCmd.Flags().Lookup("downstream-worker-num"))
	viper.BindPFlag("gateway.max_conn_num", gatewayCmd.Flags().Lookup("max-conn"))
}
