package gatewaygrpc

import (
	"context"
	"log/slog"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/gen/gateway/v1"
)

// GrpcServer gRPC 服务端实现（适配新版 GatewayInternal 接口）
type GrpcServer struct {
	gateway.UnimplementedGatewayInternalServer
}

// NewGrpcServer 创建 gRPC 服务端
func NewGrpcServer() *GrpcServer {
	return &GrpcServer{}
}

// PushMessage 处理单条推送请求
func (s *GrpcServer) PushMessage(ctx context.Context, req *gateway.PushMessageReq) (*gateway.PushMessageResp, error) {
	slog.Debug("grpc PushMessage received",
		"to", req.To,
		"biz", req.BizCode,
		"origin", req.Origin)

	// TODO: 实现消息推送逻辑
	// 1. 根据 req.To 查找目标 Session
	// 2. 通过 downlink pipeline 投递消息

	return &gateway.PushMessageResp{
		Accepted: true,
		Status:   gateway.PushMessageResp_DELIVERED,
	}, nil
}
