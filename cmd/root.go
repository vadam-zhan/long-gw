package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	cfgFile string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "long-gw",
	Short: "long connection gateway",
	Long: `long-gw is a high-performance long connection gateway
that supports millions of concurrent connections.`,
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
}
