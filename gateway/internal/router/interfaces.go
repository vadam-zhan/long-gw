package router

// LocalRouterInterface 本地路由接口
type LocalRouterInterface interface {
	Register(userID, deviceID string, conn ConnectionInterface)
	UnRegister(conn ConnectionInterface)
}

// ConnectionInterface 连接接口（供路由层使用）
type ConnectionInterface interface {
	GetConnID() string
	GetUserInfo() (userID, deviceID string)
	Close()
	IsTimeout() bool
}