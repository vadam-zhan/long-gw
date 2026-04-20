package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Config 应用配置
type Config struct {
	Gateway  GatewayConfig         `json:"gateway" mapstructure:"gateway"`
	Redis    RedisConfig           `json:"redis" mapstructure:"redis"`
	Session  SessionConfig         `json:"session" mapstructure:"session"`
	Workers  map[string]PoolConfig `json:"workers" mapstructure:"workers"`
	Upstream InteractConfig        `json:"upstream" mapstructure:"upstream"`
	Auth     AuthConfig            `json:"auth" mapstructure:"auth"`
	Log      LogConfig             `json:"log" mapstructure:"log"`
	Database DatabaseConfig        `json:"database" mapstructure:"database"`
}

// DatabaseConfig MySQL 数据库配置
type DatabaseConfig struct {
	DSN          string `json:"dsn" mapstructure:"dsn"`
	MaxOpenConns int    `json:"max_open_conns" mapstructure:"max_open_conns"`
	MaxIdleConns int    `json:"max_idle_conns" mapstructure:"max_idle_conns"`
	ConnMaxLife  int    `json:"conn_max_life" mapstructure:"conn_max_life"`
}

// GatewayConfig 网关配置
type GatewayConfig struct {
	Addr                string        `json:"addr" mapstructure:"addr"`
	UpstreamWorkerNum   int           `json:"upstream_worker_num" mapstructure:"upstream_worker_num"`
	DownstreamWorkerNum int           `json:"downstream_worker_num" mapstructure:"downstream_worker_num"`
	MaxConnNum          uint64        `json:"max_conn_num" mapstructure:"max_conn_num"`
	ReadBufSize         int           `json:"read_buf_size" mapstructure:"read_buf_size"`
	WriteBufSize        int           `json:"write_buf_size" mapstructure:"write_buf_size"`
	Profile             ProfileConfig `json:"profile" mapstructure:"profile"`
	Metrics             MetricsConfig `json:"metrics" mapstructure:"metrics"`
}

type PoolConfig struct {
	BizCode           string `json:"biz_code" mapstructure:"biz_code"` // im live message
	UpstreamSender    string `json:"upstream_sender" mapstructure:"upstream_sender"`
	UpstreamWorkers   int    `json:"upstream_workers"  mapstructure:"upstream_workers"`
	UpstreamChanCap   int    `json:"upstream_chan_cap"  mapstructure:"upstream_chan_cap"`
	DownstreamWorkers int    `json:"downstream_workers"  mapstructure:"downstream_workers"`
	DownstreamChanCap int    `json:"downstream_chan_cap" mapstructure:"downstream_chan_cap"`
}

type SessionConfig struct {
	SuspendTTL int `json:"suspend_ttl" mapstructure:"suspend_ttl"`
}

// ProfileConfig pprof配置
type ProfileConfig struct {
	Enabled bool   `json:"enabled" mapstructure:"enabled"`
	Addr    string `json:"addr" mapstructure:"addr"`
}

// MetricsConfig Prometheus指标配置
type MetricsConfig struct {
	Enabled bool   `json:"enabled" mapstructure:"enabled"`
	Addr    string `json:"addr" mapstructure:"addr"`
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
func Load(cfgPath string) (*Config, error) {
	v := viper.New()
	// 设置环境变量前缀
	v.SetEnvPrefix("LONG_GW")
	// 将环境变量中的下划线转换为点号，支持嵌套配置
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

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
