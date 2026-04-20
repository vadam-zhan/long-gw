package gatewaygrpc

import (
	"context"
	"log/slog"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
)

// GrpcServer gRPC 服务端实现
type GrpcServer struct {
	gateway.UnimplementedGatewayServer
}

// NewGrpcServer 创建 gRPC 服务端
func NewGrpcServer() *GrpcServer {
	return &GrpcServer{}
}

// PushMessage 处理推送消息请求
func (s *GrpcServer) PushMessage(ctx context.Context, req *gateway.PushMessageReq) (*gateway.PushMessageResp, error) {
	slog.Debug("grpc PushMessage received",
		"receiver", req.Receiver,
		"payload_len", len(req.Payload),
		"business_type", req.BusinessType.String())

	// go session.HandleConnection(transport.NewnGRPCSession(rawConn))

	// TODO: 实现消息推送逻辑
	// 1. 根据 receiver 和 business_type 查找目标连接
	// 2. 通过 downstream router 发送消息

	return &gateway.PushMessageResp{
		Success: true,
		Message: "ok",
	}, nil
}
