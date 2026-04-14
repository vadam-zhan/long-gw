package router

import (
	"maps"
	"sync"

	"github.com/vadam-zhan/long-gw/gateway/internal/consts"
)

// LocalRouter 本地路由中心
type LocalRouter struct {
	mux          sync.RWMutex
	userIDConns  map[string][]ConnectionInterface
	deviceIDConn map[string]ConnectionInterface
}

func NewLocalRouter() *LocalRouter {
	return &LocalRouter{
		userIDConns:  make(map[string][]ConnectionInterface),
		deviceIDConn: make(map[string]ConnectionInterface),
	}
}

// Register 注册连接
func (lr *LocalRouter) Register(userID, deviceID string, conn ConnectionInterface) {
	lr.mux.Lock()
	defer lr.mux.Unlock()

	// 单端登录：踢掉同设备旧连接
	if oldConn, ok := lr.deviceIDConn[deviceID]; ok {
		oldConn.Close()
		lr.removeConnLocked(oldConn)
	}

	// 注册设备映射
	lr.deviceIDConn[deviceID] = conn

	// 注册用户映射（支持多端）
	conns := lr.userIDConns[userID]
	// 限制多端登录数量
	if len(conns) >= consts.MaxMultiLogin {
		// 踢掉最早的连接
		if len(conns) > 0 {
			conns[0].Close()
			lr.removeConnLocked(conns[0])
		}
	}
	conns = append(lr.userIDConns[userID], conn)
	lr.userIDConns[userID] = conns
}

// UnRegister 注销连接
func (lr *LocalRouter) UnRegister(conn ConnectionInterface) {
	lr.mux.Lock()
	defer lr.mux.Unlock()
	lr.removeConnLocked(conn)
}

func (lr *LocalRouter) removeConnLocked(conn ConnectionInterface) {
	userID, deviceID := conn.GetUserInfo()

	// 移除设备映射
	if oldConn, ok := lr.deviceIDConn[deviceID]; ok && oldConn == conn {
		delete(lr.deviceIDConn, deviceID)
	}

	// 移除用户映射
	conns, ok := lr.userIDConns[userID]
	if !ok {
		return
	}
	newConns := make([]ConnectionInterface, 0, len(conns)-1)
	for _, c := range conns {
		if c != conn {
			newConns = append(newConns, c)
		}
	}
	if len(newConns) == 0 {
		delete(lr.userIDConns, userID)
	} else {
		lr.userIDConns[userID] = newConns
	}
}

// GetByUserID 根据用户ID获取连接列表（多端推送）
func (lr *LocalRouter) GetByUserID(userID string) ([]ConnectionInterface, bool) {
	lr.mux.Lock()
	defer lr.mux.Unlock()
	conns, ok := lr.userIDConns[userID]
	return conns, ok
}

// GetByDeviceID 根据设备ID获取单连接（精准推送）
func (lr *LocalRouter) GetByDeviceID(deviceID string) (ConnectionInterface, bool) {
	lr.mux.Lock()
	defer lr.mux.Unlock()
	conn, ok := lr.deviceIDConn[deviceID]
	return conn, ok
}

// Count 获取连接数统计
func (lr *LocalRouter) Count() (userCount, deviceCount uint) {
	lr.mux.Lock()
	defer lr.mux.Unlock()
	return uint(len(lr.userIDConns)), uint(len(lr.deviceIDConn))
}

// CleanTimeout 清理超时连接
func (lr *LocalRouter) CleanTimeout() int {
	lr.mux.RLock()
	deviceConns := make(map[string]ConnectionInterface, len(lr.deviceIDConn))
	maps.Copy(deviceConns, lr.deviceIDConn)
	lr.mux.RUnlock()

	var cleaned int
	for deviceID, conn := range deviceConns {
		if conn.IsTimeout() {
			lr.mux.Lock()
			conn.Close()
			delete(lr.deviceIDConn, deviceID)
			cleaned++
			lr.mux.Unlock()
		}
	}
	return cleaned
}
