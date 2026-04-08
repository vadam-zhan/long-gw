package business

import (
	"context"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/business/internal/logger"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

// Sender Kafka 生产者
type Sender struct {
	brokers   []string
	writerMap map[string]*kafka.Writer
	mux       sync.Mutex
}

// NewSender 创建 Kafka 生产者
func NewSender(brokers []string) *Sender {
	return &Sender{
		brokers:   brokers,
		writerMap: make(map[string]*kafka.Writer),
	}
}

// Send 发送下行消息到 Kafka
func (s *Sender) Send(ctx context.Context, topic string, msg *gateway.DownstreamKafkaMessage) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		logger.Error("failed to marshal downstream message",
			zap.Error(err),
			zap.String("correlation_id", msg.CorrelationId))
		return err
	}

	messages := []kafka.Message{
		{
			Key:   []byte(msg.ConnId),
			Value: data,
		},
	}

	return s.getWriter(topic).WriteMessages(ctx, messages...)
}

func (s *Sender) getWriter(topic string) *kafka.Writer {
	s.mux.Lock()
	defer s.mux.Unlock()

	if writer, ok := s.writerMap[topic]; ok {
		return writer
	}

	writer := &kafka.Writer{
		Addr:                   kafka.TCP(s.brokers...),
		Topic:                  topic,
		Balancer:               &kafka.Hash{},
		BatchSize:              1,
		BatchTimeout:           10 * time.Millisecond,
		Async:                  true,
		AllowAutoTopicCreation: true,
	}
	logger.Info("kafka writer created", zap.String("topic", topic))
	s.writerMap[topic] = writer
	return writer
}

// Close 关闭生产者
func (s *Sender) Close() {
	s.mux.Lock()
	defer s.mux.Unlock()

	for topic, w := range s.writerMap {
		if err := w.Close(); err != nil {
			logger.Error("failed to close writer",
				zap.Error(err),
				zap.String("topic", topic))
		}
	}
	logger.Info("kafka sender closed")
}
