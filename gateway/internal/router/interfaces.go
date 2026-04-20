package router

// LocalRouterInterface 本地路由接口
type LocalRouterInterface interface {
	Register(userID, deviceID string, conn SessionInterface)
	UnRegister(conn SessionInterface)
}

// SessionInterface 会话接口（供路由层使用）
type SessionInterface interface {
	GetSessionID() string
	GetDeviceID() string
	GetUserID() string
	Close()
	IsActive() bool
	IsTimeout() bool
}
