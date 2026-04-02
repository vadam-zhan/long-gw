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
gateway/              # 接入层服务
  ├── gateway.go       # 主入口、启动服务
  ├── server.go        # GatewayServer结构体
  └── session.go       # Session连接管理

auth/                 # 控制层服务
  └── server.go        # AuthServer实现

cmd/                  # Cobra命令入口
  ├── root.go          # 根命令(Viper初始化)
  └── gateway.go       # gateway子命令

internal/             # 内部包
  ├── config/          # 配置管理(Viper)
  ├── protocol/        # 协议处理
  ├── router/          # 路由中心
  └── svc/             # 服务上下文
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
# 构建
make build

# 运行(使用默认配置)
./long-gw gateway

# 指定配置文件
./long-gw gateway --config config.yaml

# 环境变量覆盖
LONG_GW_GATEWAY_ADDR=:9090 ./long-gw gateway
```

## 核心架构

**四层结构**: 统一SDK → 控制层(auth) → 接入层(gateway) → 路由层(Redis)

**关键设计**:
- 双Goroutine模型（readLoop/writeLoop分离）
- Worker池（上行/下行独立池）
- 本地路由（userID/deviceID → Connection映射）
- 分布式路由（Redis存储用户→节点映射）

## 关键参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| gateway.addr | :8080 | 监听地址 |
| gateway.worker_num | 100 | Worker数量 |
| gateway.max_conn_num | 1000000 | 最大连接数 |
| redis.addr | localhost:6379 | Redis地址 |
| kafka.brokers | localhost:9092 | Kafka集群 |

## 开发注意事项

1. **新协议接入**: 在`internal/protocol/`实现`IConn`接口
2. **配置加载**: Viper自动合并配置文件、环境变量、命令行flag
3. **优雅关闭**: 使用context取消信号，WaitGroup等待goroutine退出
4. **Redis配置**: 分布式路由在Redis未配置时自动禁用
