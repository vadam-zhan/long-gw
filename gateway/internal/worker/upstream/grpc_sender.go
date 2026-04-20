package upstream

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// GrpcSender gRPC 上行发送器
type GrpcSender struct {
	addr   string
	method string
	conn   *grpc.ClientConn
}

// NewGRPCSender 创建 gRPC 发送器
func NewGRPCSender(addr string) *GrpcSender {
	return &GrpcSender{
		addr: addr,
	}
}

// Send 发送上行消息到 gRPC 服务
func (s *GrpcSender) Send(ctx context.Context) error {
	_, err := s.getConn()
	if err != nil {
		return err
	}

	// TODO: 根据实际业务方定义的 gRPC service interface 调整
	// 这里使用通用的UnaryCaller方式，实际项目中可能需要定义业务方的 service interface
	slog.Debug("grpc upstream send",
		"addr", s.addr,
		"method", s.method)

	// 模拟发送 - 实际实现需要调用业务方的 gRPC 接口，这里可以理解成 callback 形式，只要业务方实现了接口即可
	// 例如: client.SendMessage(ctx, &pb.BusinessMessage{...})
	return nil
}

func (s *GrpcSender) getConn() (*grpc.ClientConn, error) {
	if s.conn == nil {
		conn, err := grpc.Dial(
			s.addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
			grpc.WithTimeout(3*time.Second),
		)
		if err != nil {
			slog.Error("grpc dial failed",
				"addr", s.addr,
				"error", err)
			return nil, err
		}
		s.conn = conn
	}
	return s.conn, nil
}

// Close 关闭连接
func (s *GrpcSender) Close() {
	if s.conn != nil {
		s.conn.Close()
	}
}
