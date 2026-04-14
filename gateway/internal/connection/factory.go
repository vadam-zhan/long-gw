package connection

import (
	"github.com/vadam-zhan/long-gw/gateway/internal/router"
	"github.com/vadam-zhan/long-gw/gateway/internal/transport"
)

// ConnectionFactory 创建连接实例的工厂
type ConnectionFactory struct {
	localRouter      router.LocalRouterInterface
	distRouter      router.DistributedRouterInterface
	upstreamSender   UpstreamSender
}

// NewConnectionFactory 创建连接工厂
func NewConnectionFactory(
	localRouter router.LocalRouterInterface,
	distRouter router.DistributedRouterInterface,
	upstreamSender UpstreamSender,
) *ConnectionFactory {
	return &ConnectionFactory{
		localRouter:    localRouter,
		distRouter:     distRouter,
		upstreamSender: upstreamSender,
	}
}

// CreateConnection 创建连接实例
func (f *ConnectionFactory) CreateConnection(tp transport.Transport) *Connection {
	conn := NewConnection(tp)
	conn.SetRouters(f.localRouter, f.distRouter)
	conn.SetUpstreamSender(f.upstreamSender)
	return conn
}