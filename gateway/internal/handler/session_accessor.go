package handler

import (
	"github.com/vadam-zhan/long-gw/gateway/internal/router"
)

// SessionAccessor Session 访问接口，用于解耦 handler 和 session
type SessionAccessor interface {
	GetConnCount() uint
	GetLocalRouter() LocalRouterAccessor
}

// LocalRouterAccessor 本地路由访问接口
type LocalRouterAccessor interface {
	Count() (userCount, deviceCount uint)
	GetByUserID(userID string) ([]router.ConnectionInterface, bool)
}

// KickAccessor 用于踢人操作的接口
type KickAccessor interface {
	KickUser(userID string) error
}

// StatsHandler 返回统计数据
func StatsHandler(sess SessionAccessor) map[string]interface{} {
	userCount, deviceCount := sess.GetLocalRouter().Count()
	return map[string]interface{}{
		"code":         0,
		"conn_count":   sess.GetConnCount(),
		"user_count":   userCount,
		"device_count": deviceCount,
	}
}
