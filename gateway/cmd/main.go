package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/vadam-zhan/long-gw/gateway/internal/config"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "gateway",
	Short: "start gateway server",
	Run: func(cmd *cobra.Command, args []string) {
		// 加载配置
		cfg, err := config.Load(cfgFile)
		if err != nil {
			slog.Error("failed to load config", "error", err)
		}

		// 初始化日志
		logger.Init(cfg.Log.Level, cfg.Log.File)

		// 输出配置信息
		slog.Info("gateway config loaded", "cfg", cfg)

		// 创建并启动网关
		gw, err := NewGatewayServer(cfg)
		if err != nil {
			slog.Error("gateway start failed", "error", err)
		}
		if err := gw.Start(); err != nil {
			slog.Error("gateway start failed", "error", err)
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
