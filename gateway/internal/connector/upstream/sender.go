package upstream

import (
	"fmt"

	"github.com/vadam-zhan/long-gw/gateway/internal/config"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
	"go.uber.org/zap"
)

// SenderFactory 创建 UpstreamSender
type SenderFactory struct {
	cfg config.InteractConfig
}

// NewSenderFactory 创建工厂
func NewSenderFactory(cfg config.InteractConfig) *SenderFactory {
	return &SenderFactory{cfg: cfg}
}

// CreateSender 根据配置创建对应类型的 UpstreamSender
func (f *SenderFactory) CreateSender(bt string) (Sender, error) {
	switch f.cfg.Kind {
	case "kafka":
		logger.Info("creating kafka upstream sender",
			zap.String("business_type", bt),
			zap.String("topic", f.cfg.Kafka.BusinessTopics[bt].UpstreamTopic))
		return NewKafkaSender(f.cfg.Kafka.Brokers, f.cfg.Kafka.BusinessTopics[types.Proto(bt)].UpstreamTopic), nil

	case "grpc":
		logger.Info("creating grpc upstream sender",
			zap.String("business_type", bt),
			zap.String("addr", f.cfg.GRPC.BusinessConfig[bt].GRPCAddr))
		return NewGRPCSender(f.cfg.GRPC.BusinessConfig[bt].GRPCAddr), nil

	default:
		return nil, fmt.Errorf("unknown upstream kind: %s", f.cfg.Kind)
	}
}
