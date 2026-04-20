package upstream

import (
	"context"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	pb "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

// KafkaSender Kafka 上行发送器
type KafkaSender struct {
	brokers   []string
	writerMap map[string]*kafka.Writer
	mux       sync.Mutex
	topic     string
}

// NewKafkaSender 创建 Kafka 发送器
func NewKafkaSender(brokers []string, topic string) *KafkaSender {
	return &KafkaSender{
		brokers:   brokers,
		topic:     topic,
		writerMap: make(map[string]*kafka.Writer),
	}
}

func (s *KafkaSender) Publish(ctx context.Context, msg *gateway.Message) error {

	return nil
}

// Send 发送上行消息到 Kafka
func (s *KafkaSender) Send(ctx context.Context, req *types.UpstreamRequest) error {
	// 从 Payload 提取业务数据
	var payload []byte
	var bizType pb.BusinessType
	if bp, ok := req.Msg.Payload.(*types.BusinessPayload); ok {
		payload = bp.Body
		bizType = bp.BizType.Proto()
	}

	// Build protobuf message
	upstreamMsg := &pb.UpstreamKafkaMessage{
		ConnId:       req.ConnID,
		UserId:       req.Msg.UserID,
		DeviceId:     req.Msg.DeviceID,
		OriginalType: req.Msg.Type.ToProto(),
		Payload:      payload,
		Timestamp:    time.Now().UnixMilli(),
		BusinessType: bizType,
	}

	logger.Info("kafka sender upstreamMsg", zap.Any("upstreamMsg", upstreamMsg))

	data, err := proto.Marshal(upstreamMsg)
	if err != nil {
		logger.Error("failed to marshal upstream message",
			zap.Error(err))
		return err
	}

	var messages []kafka.Message
	messages = append(messages, kafka.Message{
		Key:   []byte(req.ConnID),
		Value: data,
	})

	return s.getWriter(s.topic).WriteMessages(ctx, messages...)
}

// Kind 返回发送器类型
func (s *KafkaSender) Kind() types.UpstreamKind {
	return types.UpstreamKindKafka
}

func (s *KafkaSender) getWriter(topic string) *kafka.Writer {
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
	logger.Info("kafka sender writer", zap.Any("writer.topic", writer.Topic))
	s.writerMap[topic] = writer
	return writer
}

// Close gracefully shuts down the producer
func (s *KafkaSender) Close() {
	for _, w := range s.writerMap {
		w.Close()
	}
	logger.Info("kafka producer closed")
}
