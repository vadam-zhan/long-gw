package kafka

import (
	"log/slog"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/config"
)

// stringToBusinessType maps string business type keys to proto BusinessType
var stringToBusinessType = map[string]gateway.BusinessType{
	"im":      gateway.BusinessType_IM,
	"live":    gateway.BusinessType_LIVE,
	"message": gateway.BusinessType_MESSAGE,
}

// TopicManager 业务类型到 Kafka Topic 的映射管理
type TopicManager struct {
	topics map[string]config.TopicPair
}

// NewTopicManager 创建 TopicManager
func NewTopicManager(cfg *config.KafkaConfig) *TopicManager {
	tm := &TopicManager{
		topics: make(map[string]config.TopicPair),
	}

	for key, topicCfg := range cfg.BusinessTopics {
		tm.topics[key] = topicCfg
		slog.Info("registered business topic",
			"business", key,
			"upstream", topicCfg.UpstreamTopic,
			"downstream", topicCfg.DownstreamTopic)
	}

	return tm
}

// GetUpstreamTopic 获取上行 Topic
func (tm *TopicManager) GetUpstreamTopic(key string) string {
	if pair, ok := tm.topics[key]; ok {
		return pair.UpstreamTopic
	}
	return ""
}

// GetTopicConfig 获取完整 Topic 配置
func (tm *TopicManager) GetTopicConfig(key string) *config.TopicPair {
	if pair, ok := tm.topics[key]; ok {
		return &pair
	}
	return nil
}

// GetDownstreamTopic 获取下行 Topic
func (tm *TopicManager) GetDownstreamTopic(key string) string {
	if pair, ok := tm.topics[key]; ok {
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

// BusinessTypes 返回所有已注册的业务类型（字符串 key）
func (tm *TopicManager) BusinessTypes() []string {
	keys := make([]string, 0, len(tm.topics))
	for key := range tm.topics {
		keys = append(keys, key)
	}
	return keys
}

// StringToProtoBusinessType converts a string business type to proto BusinessType
func StringToProtoBusinessType(s string) gateway.BusinessType {
	if bt, ok := stringToBusinessType[s]; ok {
		return bt
	}
	return gateway.BusinessType_UNSPECIFIED
}
