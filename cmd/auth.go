/*
Copyright © 2026 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"github.com/spf13/cobra"
	"github.com/vadam-zhan/long-gw/auth"
	"github.com/vadam-zhan/long-gw/internal/logger"
	"go.uber.org/zap"
)

// authCmd represents the auth command
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "start auth server",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		// 初始化日志
		logger.Init("info", "json", "")

		srv := auth.NewAuthServer(":8081")
		logger.Info("auth server starting", zap.String("addr", ":8081"))
		if err := srv.Start(); err != nil {
			logger.Fatal("auth server start failed", zap.Error(err))
		}
	},
}

func init() {
	rootCmd.AddCommand(authCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// authCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags, which will only run when this action is called directly.
	// authCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
