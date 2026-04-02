package sdk_test

import (
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/vadam-zhan/long-gw/sdk"
	"github.com/vadam-zhan/long-gw/sdk/tcp"
	"github.com/vadam-zhan/long-gw/sdk/ws"
)

// Example demonstrates TCP client usage
func TestExample(t *testing.T) {
	// 创建TCP客户端
	client := tcp.NewClient(
		sdk.WithAddr("localhost:8089"),
		sdk.WithHeartbeatInterval(30*time.Second),
		sdk.WithMessageHandler(func(msg *sdk.Message) {
			fmt.Printf("TCP Received: type=%v, msgID=%s, body=%s\n",
				msg.Type, msg.MsgID, string(msg.Body))
		}),
		sdk.WithConnectHandler(func(state sdk.ConnState, err error) {
			fmt.Printf("TCP State: %v, err=%v\n", state, err)
		}),
	)

	// 连接服务器
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

	// 订阅主题
	if err := client.Subscribe("topic_test", 1); err != nil {
		log.Printf("Subscribe failed: %v", err)
	}

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

// Example_ws demonstrates WebSocket client usage
func TestExampleWS(t *testing.T) {
	// 创建WebSocket客户端
	client := ws.NewClient(
		sdk.WithAddr("ws://localhost:8089"),
		sdk.WithHeartbeatInterval(30*time.Second),
		sdk.WithMessageHandler(func(msg *sdk.Message) {
			fmt.Printf("WS Received: type=%v, msgID=%s, body=%s\n",
				msg.Type, msg.MsgID, string(msg.Body))
		}),
		sdk.WithConnectHandler(func(state sdk.ConnState, err error) {
			fmt.Printf("WS State: %v, err=%v\n", state, err)
		}),
	)

	// 连接服务器
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
	fmt.Printf("Auth success, heartbeat: %ds\n", authResp.HeartbeatInterval)

	// 发送业务消息（异步）
	client.SendBusinessAsync(sdk.BusinessType(sdk.BusinessTypeIM.ToProto()), "msg_002", []byte("hello from web"))
	client.SendBusinessAsync(sdk.BusinessType(sdk.BusinessTypeIM.ToProto()), "msg_003", []byte("hello from web3"))

	time.Sleep(5 * time.Minute)
	// 关闭
	client.Close()
}
