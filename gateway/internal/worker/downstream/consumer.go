package downstream

import (
	"context"
	"sync"

	"github.com/segmentio/kafka-go"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker"
	"github.com/vadam-zhan/long-gw/gateway/internal/worker/storage"
	"google.golang.org/protobuf/proto"
)

// Consumer Kafka 下行消息消费者
type Consumer struct {
	brokers      []string
	topics       []string
	pools        map[gateway.BusinessType]*worker.WorkerPool
	registry     worker.ConnectionRegistry
	offlineStore storage.OfflineStore
	wg           sync.WaitGroup
}

// NewConsumer 创建消费者
func NewConsumer(
	brokers []string,
	topics []string,
	pools map[gateway.BusinessType]*worker.WorkerPool,
	registry worker.ConnectionRegistry,
	store storage.OfflineStore,
) *Consumer {
	return &Consumer{
		brokers:      brokers,
		topics:       topics,
		pools:        pools,
		registry:     registry,
		offlineStore: store,
	}
}

// Start 启动消费者
func (c *Consumer) Start(ctx context.Context) error {
	for _, topic := range c.topics {
		c.wg.Add(1)
		go c.consumeTopic(ctx, topic)
	}
	return nil
}

// Stop 停止消费者
func (c *Consumer) Stop() {
	c.wg.Wait()
}

func (c *Consumer) consumeTopic(ctx context.Context, topic string) {
	defer c.wg.Done()

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  c.brokers,
		GroupID:  "gateway-downstream-consumer",
		Topic:    topic,
		MinBytes: 1,
		MaxBytes: 10e6,
	})

	for {
		select {
		case <-ctx.Done():
			return
		default:
			msg, err := reader.FetchMessage(ctx)
			if err != nil {
				continue
			}

			downstreamMsg := &gateway.DownstreamKafkaMessage{}
			if err := proto.Unmarshal(msg.Value, downstreamMsg); err != nil {
				reader.CommitMessages(ctx, msg)
				continue
			}

			// 提交到对应业务类型的 pool
			if pool, ok := c.pools[downstreamMsg.BusinessType]; ok {
				pool.SubmitDownstream(worker.DownstreamJob{
					Msg:    downstreamMsg,
					Offset: msg.Offset,
				})
			}

			reader.CommitMessages(ctx, msg)
		}
	}
}
