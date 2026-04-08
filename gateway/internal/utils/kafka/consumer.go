package kafka

import (
	"context"
	"sync"

	"github.com/segmentio/kafka-go"
	pb "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/config"
	"github.com/vadam-zhan/long-gw/gateway/internal/connector"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
)

// Consumer handles group consumption of Kafka messages
type Consumer struct {
	cfg       *config.KafkaConfig
	ReaderMap map[string]*kafka.Reader
	pools     map[pb.BusinessType]connector.WorkerPoolInterface
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	topics    []string
	mux       sync.Mutex
}

// NewConsumer creates a new Kafka consumer
func NewConsumer(cfg *config.KafkaConfig, pools map[pb.BusinessType]connector.WorkerPoolInterface, topics []string) *Consumer {
	logger.Info("kafka consumer created",
		zap.Strings("brokers", cfg.Brokers),
		zap.Strings("topics", topics))

	return &Consumer{
		cfg:       cfg,
		ReaderMap: map[string]*kafka.Reader{},
		pools:     pools,
		topics:    topics,
	}
}

// Start begins consuming messages
func (c *Consumer) Start(ctx context.Context) error {
	for _, topic := range c.topics {
		c.wg.Add(1)
		go func(topic string) {
			defer c.wg.Done()
			r := c.newReader(topic)

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

					// Deserialize downstream message
					downstreamMsg := &pb.DownstreamKafkaMessage{}
					if err := proto.Unmarshal(msg.Value, downstreamMsg); err != nil {
						logger.Error("failed to unmarshal downstream message",
							zap.Error(err),
							zap.Int64("offset", msg.Offset))
						// Commit to avoid reprocessing poison messages
						r.CommitMessages(ctx, msg)
						continue
					}

					// Submit to worker pool
					job := connector.DownstreamJob{
						DownstreamMsg: downstreamMsg,
					}
					if !c.pools[downstreamMsg.BusinessType].SubmitDownstream(job) {
						logger.Warn("worker pool full, message will be redelivered",
							zap.String("correlation_id", downstreamMsg.CorrelationId),
							zap.Int64("offset", msg.Offset))
						// worker pool 拒绝 → 不 commit（消息会被重新投递）
						// Do NOT commit - message will be redelivered when consumer rebalances
						continue
					}

					// Commit only after successfully accepted by worker pool
					if err := r.CommitMessages(ctx, msg); err != nil {
						logger.Error("failed to commit message", zap.Error(err))
					}
				}
			}
		}(topic)
	}

	logger.Info("kafka consumer started")
	return nil
}

func (c *Consumer) newReader(topic string) *kafka.Reader {
	c.mux.Lock()
	defer c.mux.Unlock()
	if reader, ok := c.ReaderMap[topic]; ok {
		return reader
	}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  c.cfg.Brokers,
		GroupID:  "gateway-consumer-group",
		Topic:    topic,
		MinBytes: 1,
		MaxBytes: 10e6,
		Dialer: &kafka.Dialer{
			DualStack: true,
		},
	})
	c.ReaderMap[topic] = r
	return r
}

// Stop gracefully stops the consumer
func (c *Consumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	for _, r := range c.ReaderMap {
		r.Close()
	}
	return nil
}
