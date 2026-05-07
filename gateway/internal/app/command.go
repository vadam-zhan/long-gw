package app

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "gateway",
	Short: "start gateway server",
	Run:   Run,
}

var cfgFile string

func init() {
	// 绑定配置文件路径flag
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "./configs/gateway.yaml", "config file path (default: ./configs/config.yaml)")

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
