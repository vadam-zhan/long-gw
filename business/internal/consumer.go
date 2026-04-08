package business

import (
	"context"
	"sync"

	proto "github.com/golang/protobuf/proto"
	"github.com/segmentio/kafka-go"
	"github.com/vadam-zhan/long-gw/business/internal/logger"
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	pb "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"go.uber.org/zap"
)

// UpstreamHandler 上行消息处理函数类型
type UpstreamHandler func(msg *gateway.UpstreamKafkaMessage) error

// Consumer Kafka 消费者
type Consumer struct {
	brokers   []string
	readerMap map[string]*kafka.Reader
	topics    []string
	handler   UpstreamHandler
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mux       sync.Mutex
}

// NewConsumer 创建 Kafka 消费者
func NewConsumer(brokers []string, topics []string) *Consumer {
	return &Consumer{
		brokers:   brokers,
		topics:    topics,
		readerMap: make(map[string]*kafka.Reader),
	}
}

// Start 开始消费消息
func (c *Consumer) Start(ctx context.Context, handler UpstreamHandler) error {
	c.handler = handler

	for _, topic := range c.topics {
		c.wg.Add(1)
		go func(topic string) {
			defer c.wg.Done()
			r := c.getReader(topic)

			logger.Info("consumer started for topic", zap.String("topic", topic))

			for {
				select {
				case <-ctx.Done():
					return
				default:
					msg, err := r.FetchMessage(ctx)
					if err != nil {
						if ctx.Err() != nil {
							return
						}
						logger.Error("kafka fetch message error", zap.Error(err))
						continue
					}

					// 反序列化上行消息
					upstreamMsg := &pb.UpstreamKafkaMessage{}
					if err := proto.Unmarshal(msg.Value, upstreamMsg); err != nil {
						logger.Error("failed to unmarshal upstream message",
							zap.Error(err),
							zap.Int64("offset", msg.Offset))
						// 提交以避免毒消息重试
						r.CommitMessages(ctx, msg)
						continue
					}

					// 调用处理函数
					if err := c.handler(upstreamMsg); err != nil {
						logger.Error("failed to handle upstream message",
							zap.Error(err),
							zap.String("correlation_id", upstreamMsg.CorrelationId))
						// 处理失败，不提交，消息会被重新投递
						continue
					}

					// 处理成功后提交
					if err := r.CommitMessages(ctx, msg); err != nil {
						logger.Error("failed to commit message", zap.Error(err))
					}
				}
			}
		}(topic)
	}

	logger.Info("kafka consumer started", zap.Strings("topics", c.topics))
	return nil
}

func (c *Consumer) getReader(topic string) *kafka.Reader {
	c.mux.Lock()
	defer c.mux.Unlock()

	if reader, ok := c.readerMap[topic]; ok {
		return reader
	}

	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  c.brokers,
		GroupID:  "business-consumer-group",
		Topic:    topic,
		MinBytes: 1,
		MaxBytes: 10e6,
		Dialer: &kafka.Dialer{
			DualStack: true,
		},
	})
	c.readerMap[topic] = r
	return r
}

// Stop 停止消费者
func (c *Consumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	for _, r := range c.readerMap {
		r.Close()
	}
	logger.Info("kafka consumer stopped")
	return nil
}
