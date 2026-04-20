package worker

import (
	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
)

// ConnectionWriter 消息写入接口
// 定义在 worker 包，由 connection 包实现
type ConnectionWriter interface {
	Write(msg *gateway.Message) bool
}

// ConnectionRegistry 连接注册表接口
// 定义在 worker 包，由 session 包实现
type ConnectionRegistry interface {
	Register(connID string, writer ConnectionWriter)
	Unregister(connID string)
	Get(connID string) (ConnectionWriter, bool)
}
