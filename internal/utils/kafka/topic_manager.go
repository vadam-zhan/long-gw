package kafka

import (
	pb "github.com/vadam-zhan/long-gw/api/proto/v1"
	"github.com/vadam-zhan/long-gw/internal/config"
	"github.com/vadam-zhan/long-gw/internal/logger"
	"github.com/vadam-zhan/long-gw/internal/types"

	"go.uber.org/zap"
)

// TopicManager 业务类型到 Kafka Topic 的映射管理
type TopicManager struct {
	topics map[pb.BusinessType]config.TopicPair
}

// NewTopicManager 创建 TopicManager
func NewTopicManager(cfg *config.KafkaConfig) *TopicManager {
	tm := &TopicManager{
		topics: make(map[pb.BusinessType]config.TopicPair),
	}

	for key, topicCfg := range cfg.BusinessTopics {
		bt := types.BusinessType(key)
		if err := bt.Validate(); err != nil {
			logger.Warn("invalid business type in config, skipping",
				zap.String("type", key),
				zap.Error(err))
			panic(err)
		}

		tm.topics[bt.Proto()] = topicCfg
		logger.Info("registered business topic",
			zap.String("business", bt.String()),
			zap.String("upstream", topicCfg.UpstreamTopic))
	}

	return tm
}

// GetUpstreamTopic 获取上行 Topic
func (tm *TopicManager) GetUpstreamTopic(bt pb.BusinessType) string {
	if pair, ok := tm.topics[bt]; ok {
		return pair.UpstreamTopic
	}
	return ""
}

// GetTopicConfig 获取完整 Topic 配置
func (tm *TopicManager) GetTopicConfig(bt pb.BusinessType) *config.TopicPair {
	if pair, ok := tm.topics[bt]; ok {
		return &pair
	}
	return nil
}

// GetDownstreamTopic 获取下行 Topic
func (tm *TopicManager) GetDownstreamTopic(bt pb.BusinessType) string {
	if pair, ok := tm.topics[bt]; ok {
		return pair.DownstreamTopic
	}
	return ""
}

// GetAllUpstreamTopics 获取所有上行 Topics（用于 Consumer 订阅）
func (tm *TopicManager) GetAllUpstreamTopics() []string {
	topics := make([]string, 0, len(tm.topics))
	for _, pair := range tm.topics {
		if pair.UpstreamTopic != "" {
			topics = append(topics, pair.UpstreamTopic)
		}
	}
	return topics
}

// GetAllDownstreamTopics 获取所有下行 Topics（用于 Consumer 订阅）
func (tm *TopicManager) GetAllDownstreamTopics() []string {
	topics := make([]string, 0, len(tm.topics))
	for _, pair := range tm.topics {
		if pair.DownstreamTopic != "" {
			topics = append(topics, pair.DownstreamTopic)
		}
	}
	return topics
}

// BusinessTypes 返回所有已注册的业务类型
func (tm *TopicManager) BusinessTypes() []pb.BusinessType {
	result := make([]pb.BusinessType, 0, len(tm.topics))
	for bt := range tm.topics {
		result = append(result, bt)
	}
	return result
}
