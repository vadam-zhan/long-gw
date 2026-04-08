package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config 应用配置
type Config struct {
	Business BusinessConfig `json:"business" mapstructure:"business"`
	Kafka    KafkaConfig    `json:"kafka" mapstructure:"kafka"`
	Log      LogConfig      `json:"log" mapstructure:"log"`
}

// BusinessConfig 业务服务配置
type BusinessConfig struct {
	Addr string `json:"addr" mapstructure:"addr"`
}

// KafkaConfig Kafka配置
type KafkaConfig struct {
	Brokers []string `json:"brokers" mapstructure:"brokers"`
	// 业务类型 -> Topic 映射
	BusinessTopics map[string]TopicPair `json:"business_topics" mapstructure:"business_topics"`
}

// TopicPair 上行/下行 Topic 对
type TopicPair struct {
	UpstreamTopic   string `json:"upstream_topic" mapstructure:"upstream_topic"`
	DownstreamTopic string `json:"downstream_topic" mapstructure:"downstream_topic"`
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
	v.SetEnvPrefix("LONG_GW")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	if cfgPath != "" {
		v.SetConfigFile(cfgPath)
		if err := v.ReadInConfig(); err != nil {
			return nil, err
		}
		fmt.Printf("loaded config file: %s\n", v.ConfigFileUsed())
	} else {
		v.SetConfigType("env")
		v.SetConfigName(".env")
		v.AddConfigPath(".")
		if err := v.ReadInConfig(); err == nil {
			fmt.Printf("loaded .env config file: %s\n", v.ConfigFileUsed())
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("business.addr", ":8082")

	v.SetDefault("kafka.brokers", []string{"localhost:9092"})
	v.SetDefault("kafka.business_topics", map[string]any{
		"im": map[string]any{
			"upstream_topic":   "gateway-im-upstream",
			"downstream_topic": "gateway-im-downstream",
		},
		"live": map[string]any{
			"upstream_topic":   "gateway-live-upstream",
			"downstream_topic": "gateway-live-downstream",
		},
		"message": map[string]any{
			"upstream_topic":   "gateway-message-upstream",
			"downstream_topic": "gateway-message-downstream",
		},
	})

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("log.file", "")
}
