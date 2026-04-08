# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

长连接接入层网关（long-gw），支持单节点百万级长连接，采用Go语言开发。

## 技术栈

- **语言**: Go
- **HTTP框架**: Gin
- **配置**: Viper (支持YAML/JSON/TOML/.env)
- **协议**: TCP、WebSocket、gRPC、QUIC
- **消息队列**: Kafka
- **缓存/路由存储**: Redis

## 项目结构

```
long-gw/              # 项目根目录 (Go Workspace)
├── auth/              # 控制层服务
│   └── server.go      # AuthServer实现
│
├── business/          # 业务后端服务(独立部署)
│   ├── main.go        # 服务启动入口
│   ├── internal/      # 业务服务内部包
│   │   ├── server.go  # 业务服务主入口(Gin HTTP Server)
│   │   ├── consumer.go # Kafka消费者(消费上行消息)
│   │   ├── sender.go  # Kafka生产者(发送下行消息)
│   │   ├── config/    # 配置管理(Viper)
│   │   └── logger/    # 日志封装
│   ├── Makefile       # 构建脚本
│   └── Dockerfile     # 容器镜像
│
├── gateway/           # 接入层服务
│   ├── gateway.go     # 主入口、启动服务
│   ├── server.go      # GatewayServer结构体
│   ├── session.go     # Session连接管理
│   ├── cmd/           # 可执行入口
│   │   └── main.go    # 服务启动入口
│   └── internal/      # 网关内部包
│
├── sdk/               # 统一SDK
│   ├── client.go      # 客户端接口
│   ├── tcp/client.go  # TCP客户端
│   ├── ws/client.go   # WebSocket客户端
│   └── grpc/client.go # gRPC客户端
│
└── common-protocol/   # 公共协议定义
    └── v1/           # Proto生成的Go代码
        ├── kafka_message.proto
        ├── gateway.proto
        └── signal.proto
```

## 配置文件

配置通过Viper加载，支持多种配置源：

```bash
# 配置文件路径(默认: ./config.yaml)
./long-gw gateway --config /path/to/config.yaml

# 环境变量覆盖(LONG_GW_前缀)
LONG_GW_GATEWAY_ADDR=:9090 ./long-gw gateway
```

配置示例: `config.yaml.example`

```yaml
gateway:
  addr: ":8080"
  worker_num: 100
  max_conn_num: 1000000
redis:
  addr: "localhost:6379"
kafka:
  brokers: ["localhost:9092"]
```

## 启动命令

```bash
# 启动 Gateway 服务
go run ./gateway/cmd

# 启动 Business 服务
go run ./business

# 或者使用 Makefile
cd business && make build && ./bin/business
```

## 核心架构

**四层结构**: 统一SDK → 控制层(auth) → 接入层(gateway) → 路由层(Redis)

**关键设计**:
- 双Goroutine模型（readLoop/writeLoop分离）
- Worker池（上行/下行独立池）
- 本地路由（userID/deviceID → Connection映射）
- 分布式路由（Redis存储用户→节点映射）

## Kafka 数据流架构

```
┌─────────┐      ┌──────────────┐      ┌─────────┐      ┌────────────────┐
│  Client │ ──── │   Gateway    │ ──── │  Kafka  │ ──── │    Business    │
│  (SDK)  │      │ (长连接接入) │      │ (上游)  │      │   (业务后端)   │
└─────────┘      └──────────────┘      └─────────┘      └────────────────┘
                                                              │
                                                              │ 业务处理
                                                              ▼
┌─────────┐      ┌──────────────┐      ┌─────────┐      ┌────────────────┐
│  Client │ <────│   Gateway    │ <────│  Kafka  │ <────│    Business    │
│  (SDK)  │      │ (下行推送)   │      │ (下游)  │      │   (业务后端)   │
└─────────┘      └──────────────┘      └─────────┘      └────────────────┘
```

**数据流向**:
1. **上行 (Client → Business)**: Client发送消息 → Gateway接收 → Kafka (upstream topic) → Business消费处理
2. **下行 (Business → Client)**: Business处理后 → Kafka (downstream topic) → Gateway消费 → 推送给Client

**服务启动**:
```bash
# 启动 Gateway 服务
./long-gw gateway

# 启动 Business 服务
./long-gw business
```

## 关键参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| gateway.addr | :8080 | 监听地址 |
| gateway.worker_num | 100 | Worker数量 |
| gateway.max_conn_num | 1000000 | 最大连接数 |
| redis.addr | localhost:6379 | Redis地址 |
| kafka.brokers | localhost:9092 | Kafka集群 |
| business.addr | :8082 | Business HTTP服务地址 |

**Kafka Topic 配置** (通过 `kafka.business_topics`):
| 业务类型 | Upstream Topic | Downstream Topic |
|---------|---------------|-----------------|
| im | gateway-im-upstream | gateway-im-downstream |
| live | gateway-live-upstream | gateway-live-downstream |
| message | gateway-message-upstream | gateway-message-downstream |

## 开发注意事项

1. **新协议接入**: 在`internal/protocol/`实现`IConn`接口
2. **配置加载**: Viper自动合并配置文件、环境变量、命令行flag
3. **优雅关闭**: 使用context取消信号，WaitGroup等待goroutine退出
4. **Redis配置**: 分布式路由在Redis未配置时自动禁用
