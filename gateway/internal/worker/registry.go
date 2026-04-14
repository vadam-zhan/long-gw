package worker

import "github.com/vadam-zhan/long-gw/gateway/internal/types"

// ConnectionWriter 消息写入接口
// 定义在 worker 包，由 connection 包实现
type ConnectionWriter interface {
    Write(msg *types.Message) bool
}

// ConnectionRegistry 连接注册表接口
// 定义在 worker 包，由 session 包实现
type ConnectionRegistry interface {
    Register(connID string, writer ConnectionWriter)
    Unregister(connID string)
    Get(connID string) (ConnectionWriter, bool)
}
