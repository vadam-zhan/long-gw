package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/vadam-zhan/long-gw/gateway/internal/config"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"go.uber.org/zap"
)

var cfgFile string

var rootCmd = &cobra.Command{
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
		if cfg.Database.DSN != "" {
			logger.Info("database config",
				zap.String("dsn", cfg.Database.DSN),
				zap.Int("max_open_conns", cfg.Database.MaxOpenConns))
		}

		// 初始化AuthHandler
		// connection.InitAuthHandler(cfg.Auth.Addr)
		logger.Info("auth handler initialized",
			zap.String("auth_addr", cfg.Auth.Addr))

		// 创建并启动网关
		gw := NewGatewayServer(cfg)
		if err := gw.Start(); err != nil {
			logger.Fatal("gateway start failed", zap.Error(err))
		}
	},
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	// 绑定配置文件路径flag
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "./etc/config.yaml", "config file path (default: ./etc/config.yaml)")

	// 设置Viper配置
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(nil)

	// Gateway相关flag
	rootCmd.Flags().String("addr", ":8080", "gateway listen address")
	rootCmd.Flags().Int("upstream-worker-num", 50, "upstream worker pool size")
	rootCmd.Flags().Int("downstream-worker-num", 50, "downstream worker pool size")
	rootCmd.Flags().Uint64("max-conn", 1000000, "max connection number")

	// 绑定flag到viper(用于环境变量覆盖)
	viper.BindPFlag("gateway.addr", rootCmd.Flags().Lookup("addr"))
	viper.BindPFlag("gateway.upstream_worker_num", rootCmd.Flags().Lookup("upstream-worker-num"))
	viper.BindPFlag("gateway.downstream_worker_num", rootCmd.Flags().Lookup("downstream-worker-num"))
	viper.BindPFlag("gateway.max_conn_num", rootCmd.Flags().Lookup("max-conn"))
}
