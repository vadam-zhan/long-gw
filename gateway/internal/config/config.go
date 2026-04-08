package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config 应用配置
type Config struct {
	Gateway  GatewayConfig  `json:"gateway" mapstructure:"gateway"`
	Redis    RedisConfig    `json:"redis" mapstructure:"redis"`
	Upstream InteractConfig `json:"upstream" mapstructure:"upstream"`
	Auth     AuthConfig     `json:"auth" mapstructure:"auth"`
	Log      LogConfig      `json:"log" mapstructure:"log"`
}

// GatewayConfig 网关配置
type GatewayConfig struct {
	Addr                string `json:"addr" mapstructure:"addr"`
	UpstreamWorkerNum   int    `json:"upstream_worker_num" mapstructure:"upstream_worker_num"`
	DownstreamWorkerNum int    `json:"downstream_worker_num" mapstructure:"downstream_worker_num"`
	MaxConnNum          uint64 `json:"max_conn_num" mapstructure:"max_conn_num"`
	ReadBufSize         int    `json:"read_buf_size" mapstructure:"read_buf_size"`
	WriteBufSize        int    `json:"write_buf_size" mapstructure:"write_buf_size"`
}

// RedisConfig Redis配置
type RedisConfig struct {
	Addr     string `json:"addr" mapstructure:"addr"`
	Password string `json:"password" mapstructure:"password"`
	DB       int    `json:"db" mapstructure:"db"`
	PoolSize int    `json:"pool_size" mapstructure:"pool_size"`
	RouteTTL int    `json:"route_ttl" mapstructure:"route_ttl"`
}

// InteractConfig 与业务方交互配置，下行目前都是 kafka
type InteractConfig struct {
	// 方式：kafka 或 grpc
	Kind  string      `json:"kind" mapstructure:"kind"`
	Kafka KafkaConfig `json:"kafka" mapstructure:"kafka"`
	GRPC  GRPCConfig  `json:"grpc" mapstructure:"grpc"`
}

type KafkaConfig struct {
	Brokers []string `json:"brokers" mapstructure:"brokers"`
	// 业务类型 -> Topic 映射
	BusinessTopics map[string]TopicPair `json:"business_topics" mapstructure:"business_topics"`
}

// TopicPair 上行/下行 Topic 对
type TopicPair struct {
	// 上行 topic
	UpstreamTopic string `json:"upstream_topic" mapstructure:"upstream_topic"`
	// 下行 Topic
	DownstreamTopic string `json:"downstream" mapstructure:"downstream"`
}

type GRPCConfig struct {
	// 业务类型 -> 映射
	BusinessConfig map[string]GRPCPair `json:"business_config" mapstructure:"business_config"`
}

// GRPCPair gRPC 模式：服务地址
type GRPCPair struct {
	// gRPC 模式：服务地址
	GRPCAddr string `json:"grpc_addr" mapstructure:"grpc_addr"`
}

// AuthConfig Auth服务配置
type AuthConfig struct {
	Addr string `json:"addr" mapstructure:"addr"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level  string `json:"level" mapstructure:"level"`
	Format string `json:"format" mapstructure:"format"`
	File   string `json:"file" mapstructure:"file"`
}

// Load 加载配置
// 支持的配置源优先级（从高到低）：
// 1. 命令行flag指定的配置文件
// 2. .env 文件（如果存在）
// 3. config.yaml 文件（如果存在）
// 4. 环境变量 (LONG_GW_前缀)
// 5. 默认值
func Load(cfgPath string) (*Config, error) {
	v := viper.New()
	// 设置环境变量前缀
	v.SetEnvPrefix("LONG_GW")
	// 将环境变量中的下划线转换为点号，支持嵌套配置
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// 添加默认配置
	setDefaults(v)

	// 如果指定了配置文件路径，优先使用
	if cfgPath != "" {
		// 根据文件扩展名自动推断类型
		v.SetConfigFile(cfgPath)
		if err := v.ReadInConfig(); err != nil {
			return nil, err
		}
		fmt.Printf("loaded config file: %s\n", v.ConfigFileUsed())
	} else {
		// 尝试加载 .env 文件
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

// setDefaults 设置默认配置
func setDefaults(v *viper.Viper) {
	// Gateway默认值
	v.SetDefault("gateway.addr", ":8080")
	v.SetDefault("gateway.upstream_worker_num", 50)
	v.SetDefault("gateway.downstream_worker_num", 50)
	v.SetDefault("gateway.max_conn_num", 1000000)
	v.SetDefault("gateway.read_buf_size", 4096)
	v.SetDefault("gateway.write_buf_size", 4096)

	// Redis默认值
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)
	v.SetDefault("redis.pool_size", 100)
	v.SetDefault("redis.route_ttl", 120)

	// Kafka默认值
	v.SetDefault("kafka.brokers", []string{"localhost:9092"})
	v.SetDefault("kafka.business_topics", map[string]any{
		"im": map[string]any{
			"upstream_kind":  "kafka",
			"upstream_topic": "gateway-im-upstream",
			"downstream":     "gateway-im-downstream",
		},
		"live": map[string]any{
			"upstream_kind":  "kafka",
			"upstream_topic": "gateway-live-upstream",
			"downstream":     "gateway-live-downstream",
		},
		"message": map[string]any{
			"upstream_kind":  "kafka",
			"upstream_topic": "gateway-message-upstream",
			"downstream":     "gateway-message-downstream",
		},
	})

	// Auth默认值
	v.SetDefault("auth.addr", ":8081")

	// Log默认值
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")
	v.SetDefault("log.file", "")
}
