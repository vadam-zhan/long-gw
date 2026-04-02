# Long Connection Gateway SDK

长连接网关Go客户端SDK，支持TCP、WebSocket和gRPC三种协议。

## 功能特性

- **TCP客户端**: 基于TLV协议的长连接客户端
- **WebSocket客户端**: 基于WebSocket协议的长连接客户端
- **gRPC客户端**: 后端服务向网关推送消息的gRPC客户端
- 自动心跳保活
- 鉴权认证
- 主题订阅/取消订阅
- 业务消息收发

## 安装

```bash
go get github.com/vadam-zhan/long-gw/sdk
```

## 快速开始

### TCP客户端（移动端/IoT设备）

```go
package main

import (
    "fmt"
    "log"
    "time"

    "github.com/vadam-zhan/long-gw/sdk"
    "github.com/vadam-zhan/long-gw/sdk/tcp"
)

func main() {
    // 创建TCP客户端
    client := tcp.NewClient(
        sdk.WithAddr("localhost:8080"),
        sdk.WithHeartbeatInterval(30*time.Second),
        sdk.WithMessageHandler(func(msg *sdk.Message) {
            fmt.Printf("Received: type=%v, msgID=%s, body=%s\n",
                msg.Type, msg.MsgID, string(msg.Body))
        }),
        sdk.WithConnectHandler(func(state sdk.ConnState, err error) {
            fmt.Printf("State: %v, err=%v\n", state, err)
        }),
    )

    // 连接
    if err := client.Connect(); err != nil {
        log.Fatalf("Connect failed: %v", err)
    }

    // 鉴权
    authResp, err := client.Auth(&sdk.AuthRequest{
        Token:    "your-auth-token",
        UserID:   "user123",
        DeviceID: "device-001",
        Platform: "iOS",
    })
    if err != nil {
        log.Fatalf("Auth failed: %v", err)
    }
    fmt.Printf("Auth success, heartbeat: %ds\n", authResp.HeartbeatInterval)

    // 发送业务消息
    if err := client.SendBusiness(sdk.BusinessTypeIM, "msg_001", []byte("hello")); err != nil {
        log.Printf("Send failed: %v", err)
    }

    // 接收消息
    for msg := range client.MessageChannel() {
        if msg.Type == sdk.SignalTypeBusinessDown {
            fmt.Printf("Business: %s\n", string(msg.Body))
        }
    }

    // 登出
    client.Logout(1)

    // 关闭
    client.Close()
}
```

### WebSocket客户端（Web端）

```go
package main

import (
    "fmt"
    "log"
    "time"

    "github.com/vadam-zhan/long-gw/sdk"
    "github.com/vadam-zhan/long-gw/sdk/ws"
)

func main() {
    // 创建WebSocket客户端
    client := ws.NewClient(
        sdk.WithAddr("ws://localhost:8080"),  // 或 "localhost:8080" 自动转换
        sdk.WithHeartbeatInterval(30*time.Second),
        sdk.WithMessageHandler(func(msg *sdk.Message) {
            fmt.Printf("Received: %s\n", string(msg.Body))
        }),
    )

    // 连接
    if err := client.Connect(); err != nil {
        log.Fatalf("Connect failed: %v", err)
    }

    // 鉴权
    authResp, err := client.Auth(&sdk.AuthRequest{
        Token:    "your-auth-token",
        UserID:   "user123",
        DeviceID: "device-001",
        Platform: "web",
    })
    if err != nil {
        log.Fatalf("Auth failed: %v", err)
    }

    // 发送业务消息
    client.SendBusiness(sdk.BusinessTypeIM, "msg_001", []byte("hello"))

    // 关闭
    client.Close()
}
```

### gRPC客户端（后端服务推送）

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/vadam-zhan/long-gw/sdk"
    "github.com/vadam-zhan/long-gw/sdk/grpc"
)

func main() {
    // 创建gRPC客户端
    client, err := grpc.NewClient(
        grpc.WithAddr("localhost:8081"),
        grpc.WithTimeout(5*time.Second),
    )
    if err != nil {
        log.Fatalf("Create client failed: %v", err)
    }
    defer client.Close()

    // 推送消息给指定用户
    resp, err := client.Push(context.Background(), &grpc.PushRequest{
        Receiver:     "user123",
        Payload:      []byte("hello from backend"),
        PushType:     grpc.PushTypeSingle,
        BusinessType: sdk.BusinessTypeIM,
    })
    if err != nil {
        log.Fatalf("Push failed: %v", err)
    }
    fmt.Printf("Push result: success=%v, message=%s\n", resp.Success, resp.Message)

    // 异步推送
    client.PushAsync(&grpc.PushRequest{
        Receiver:     "user456",
        Payload:      []byte("async message"),
        PushType:     grpc.PushTypeSingle,
        BusinessType: sdk.BusinessTypeIM,
    })
}
```

## 目录结构

```
sdk/
├── types.go       # 类型定义 (Message, AuthRequest, BusinessType等)
├── codec.go      # 消息编解码
├── client.go     # 客户端基类
├── example_test.go # 使用示例
├── tcp/
│   └── client.go  # TCP客户端实现
├── ws/
│   └── client.go  # WebSocket客户端实现
└── grpc/
    └── client.go   # gRPC客户端实现（后端推送）
```

## 配置选项

### TCP/WebSocket 客户端

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `WithAddr` | string | - | 服务器地址 |
| `WithConnectTimeout` | duration | 10s | 连接超时 |
| `WithReadTimeout` | duration | 30s | 读取超时 |
| `WithWriteTimeout` | duration | 5s | 写入超时 |
| `WithHeartbeatInterval` | duration | 30s | 心跳间隔 |
| `WithAuthTimeout` | duration | 10s | 鉴权超时 |
| `WithMessageHandler` | func | nil | 消息处理回调 |
| `WithConnectHandler` | func | nil | 连接状态回调 |

### gRPC 客户端

| 选项 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `WithAddr` | string | - | gRPC服务器地址 |
| `WithTimeout` | duration | 5s | 调用超时 |
| `WithDialOption` | grpc.DialOption | - | gRPC拨号选项 |

## 业务类型

```go
sdk.BusinessTypeIM      // IM业务
sdk.BusinessTypeLIVE    // 直播业务
sdk.BusinessTypeMESSAGE // 消息业务
```

## 信令类型

```go
sdk.SignalTypeHeartbeatPing       // 心跳请求
sdk.SignalTypeHeartbeatPong       // 心跳响应
sdk.SignalTypeAuthRequest         // 鉴权请求
sdk.SignalTypeAuthResponse        // 鉴权响应
sdk.SignalTypeBusinessUp          // 业务上行
sdk.SignalTypeBusinessDown        // 业务下行
sdk.SignalTypeSubscribeRequest    // 订阅请求
sdk.SignalTypeSubscribeResponse   // 订阅响应
sdk.SignalTypeLogoutRequest       // 登出请求
sdk.SignalTypeLogoutResponse      // 登出响应
```

## 连接状态

```go
sdk.ConnStateConnecting           // 连接中
sdk.ConnStateConnected            // 已连接
sdk.ConnStateAuthenticated        // 已鉴权
sdk.ConnStateDisconnected         // 已断开
sdk.ConnStateAuthenticationFailed // 鉴权失败
```

## 推送类型（gRPC）

```go
grpc.PushTypeSingle  // 单播推送
grpc.PushTypeGroup   // 组播推送
```

## 异步模式

所有操作都支持同步和异步两种方式：

```go
// TCP/WebSocket - 同步鉴权
resp, err := client.Auth(auth)

// TCP/WebSocket - 异步鉴权
client.AuthAsync(auth)

// TCP/WebSocket - 同步发送
client.SendBusiness(bizType, msgID, body)

// TCP/WebSocket - 异步发送
client.SendBusinessAsync(bizType, msgID, body)

// gRPC - 同步推送
resp, err := client.Push(ctx, req)

// gRPC - 异步推送
client.PushAsync(req)
```

## 消息接收

两种方式接收消息：

1. **回调方式**：
```go
client := tcp.NewClient(
    sdk.WithMessageHandler(func(msg *sdk.Message) {
        fmt.Printf("Received: %s\n", string(msg.Body))
    }),
)
```

2. **通道方式**：
```go
for msg := range client.MessageChannel() {
    fmt.Printf("Received: %s\n", string(msg.Body))
}
```

## 协议说明

### TCP协议 (TLV格式)

```
+--------+--------+--------+--------+
|  magic |  ver   |  type  | length |
+--------+--------+--------+--------+
   2B       1B       1B       4B

+-----------------------------------+
|         protobuf body              |
+-----------------------------------+
```

- Magic: 0x1234
- Version: 0x01
- Length: protobuf body长度(大端序)

### WebSocket协议

WebSocket使用标准二进制帧传输protobuf数据。

### gRPC协议

后端服务通过gRPC调用`Gateway.PushMessage`接口向用户推送消息。

## 注意事项

1. TCP/WebSocket客户端必须在连接后5秒内完成鉴权
2. 心跳默认间隔30秒
3. 业务消息需要在鉴权成功后才能发送
4. 建议使用异步接口发送消息，避免阻塞
5. gRPC客户端适用于后端服务向网关推送消息的场景
