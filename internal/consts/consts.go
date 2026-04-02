package consts

import "time"

const (
	MaxMultiLogin = 5 // 单用户最大在线设备数
)

const (
	AuthTimeout       = 10 * time.Second // 认证超时时间
	HeartbeatInterval = 30 * time.Second // 客户端心跳间隔
	HeartbeatTimeout  = 90 * time.Second // 心跳超时时间（3次无心跳断开）
	WriteChannelSize  = 4096             // 写Channel缓冲大小
	MaxPacketSize     = 1024 * 1024      // 最大数据包长度1MB，防大包攻击
)

const (
	GatewayRedis_User   = "gateway:user:"
	GatewayRedis_Device = "gateway:device:"
	GatewayRedis_Nodes  = "gateway:nodes"

	RedisKeyExpireTime = 120 * time.Second
)

const (
	BusinessType_IM      = "im"
	BusinessType_LIVE    = "live"
	BusinessType_MESSAGE = "message"
)
