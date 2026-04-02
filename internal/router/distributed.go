package router

import (
	"context"

	"github.com/redis/go-redis/v9"

	"github.com/vadam-zhan/long-gw/internal/consts"
)

// DistributedRouter 分布式路由中心
type DistributedRouter struct {
	redisClient *redis.Client
	gatewayAddr string
}

func NewDistributedRouter(redisClient *redis.Client, gatewayAddr string) *DistributedRouter {
	client := redisClient
	return &DistributedRouter{
		redisClient: client,
		gatewayAddr: gatewayAddr,
	}
}

// RegisterUser 注册用户路由
func (dr *DistributedRouter) RegisterUser(ctx context.Context, userID, deviceID string) error {
	pipe := dr.redisClient.Pipeline()
	pipe.Set(ctx, consts.GatewayRedis_User+userID, dr.gatewayAddr, consts.RedisKeyExpireTime)
	pipe.Set(ctx, consts.GatewayRedis_Device+deviceID, dr.gatewayAddr, consts.RedisKeyExpireTime)
	pipe.SAdd(ctx, consts.GatewayRedis_Nodes, dr.gatewayAddr)
	_, err := pipe.Exec(ctx)
	return err
}

// RefreshRoute 刷新路由TTL
func (dr *DistributedRouter) RefreshRoute(ctx context.Context, userID, deviceID string) error {
	pipe := dr.redisClient.Pipeline()
	pipe.Expire(ctx, consts.GatewayRedis_User+userID, consts.RedisKeyExpireTime)
	pipe.Expire(ctx, consts.GatewayRedis_Device+deviceID, consts.RedisKeyExpireTime)
	_, err := pipe.Exec(ctx)
	return err
}

// UnregisterUser 注销用户路由
func (dr *DistributedRouter) UnregisterUser(ctx context.Context, userID, deviceID string) error {
	pipe := dr.redisClient.Pipeline()
	pipe.Del(ctx, consts.GatewayRedis_User+userID)
	pipe.Del(ctx, consts.GatewayRedis_Device+deviceID)
	_, err := pipe.Exec(ctx)
	return err
}

// GetNodeByUserID 根据用户ID获取网关节点地址
func (dr *DistributedRouter) GetNodeByUserID(ctx context.Context, userID string) (string, error) {
	return dr.redisClient.Get(ctx, consts.GatewayRedis_User+userID).Result()
}

// GetNodeByDeviceID 根据设备ID获取网关节点地址
func (dr *DistributedRouter) GetNodeByDeviceID(ctx context.Context, deviceID string) (string, error) {
	return dr.redisClient.Get(ctx, consts.GatewayRedis_Device+deviceID).Result()
}
