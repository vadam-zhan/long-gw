package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config 应用配置
type Config struct {
	Server ServerConfig `json:"server" mapstructure:"server"`
	Log    LogConfig    `json:"log" mapstructure:"log"`
}

// ServerConfig 服务配置
type ServerConfig struct {
	Addr string `json:"addr" mapstructure:"addr"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level  string `json:"level" mapstructure:"level"`
	Format string `json:"format" mapstructure:"format"`
	File   string `json:"file" mapstructure:"file"`
}

// Load 加载配置
func Load(cfgPath string) (*Config, error) {
	v := viper.New()
	v.SetEnvPrefix("LONG_GW_AUTH")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if cfgPath != "" {
		v.SetConfigFile(cfgPath)
		if err := v.ReadInConfig(); err != nil {
			return nil, err
		}
		fmt.Printf("loaded config file: %s\n", v.ConfigFileUsed())
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
