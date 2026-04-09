package sdk_test

import (
	"fmt"
	"log"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vadam-zhan/long-gw/sdk"
	"github.com/vadam-zhan/long-gw/sdk/tcp"
	"github.com/vadam-zhan/long-gw/sdk/ws"
)

// TestExample demonstrates TCP client usage with 5-minute sustained interaction
func TestExample(t *testing.T) {
	// 1. 请求Auth服务生成Token
	authClient := sdk.NewAuthClient("localhost:8081")
	tokenResp, err := authClient.GenerateToken(&sdk.GenerateTokenRequest{
		DeviceID: "device-001",
		UserID:   "user123",
		AppID:    "test-app",
	})
	if err != nil {
		log.Fatalf("GenerateToken failed: %v", err)
	}
	fmt.Printf("[TCP] Token generated: %s..., gateway: %s, heartbeat: %ds\n",
		tokenResp.Token[:16], tokenResp.Gateway, tokenResp.Heartbeat)

	// 2. 使用Token连接Gateway
	client := tcp.NewClient(
		sdk.WithAddr("localhost:8089"),
		sdk.WithHeartbeatInterval(time.Duration(tokenResp.Heartbeat)*time.Second),
		sdk.WithMessageHandler(func(msg *sdk.Message) {
			fmt.Printf("[TCP] Received: type=%v, msgID=%s, body=%s\n",
				msg.Type, msg.MsgID, string(msg.Body))
		}),
		sdk.WithConnectHandler(func(state sdk.ConnState, err error) {
			fmt.Printf("[TCP] State: %v, err=%v\n", state, err)
		}),
	)

	// 连接服务器
	if err := client.Connect(); err != nil {
		log.Fatalf("[TCP] Connect failed: %v", err)
	}

	// 3. 使用Token进行鉴权
	authResp, err := client.Auth(&sdk.AuthRequest{
		Token:    tokenResp.Token,
		UserID:   "user123",
		DeviceID: "device-001",
		Platform: "iOS",
	})
	if err != nil {
		log.Fatalf("[TCP] Auth failed: %v", err)
	}
	fmt.Printf("[TCP] Auth success, heartbeat interval: %ds\n", authResp.HeartbeatInterval)

	// 订阅主题
	if err := client.Subscribe("topic_test", 1); err != nil {
		log.Printf("[TCP] Subscribe failed: %v", err)
	} else {
		fmt.Printf("[TCP] Subscribed to topic_test\n")
	}

	// 4. 持续交互5分钟，每10秒发送一条消息
	duration := 5 * time.Minute
	interval := 10 * time.Second
	msgCount := atomic.Int32{}

	fmt.Printf("[TCP] Starting sustained interaction for %v (sending every %v)\n",
		duration, interval)

	endTime := time.Now().Add(duration)
	msgID := 0

	for time.Now().Before(endTime) {
		// 发送业务消息
		msgID++
		body := fmt.Sprintf("hello from TCP client, message #%d at %v", msgID, time.Now().Format("15:04:05"))
		if err := client.SendBusiness(sdk.BusinessTypeIM, fmt.Sprintf("msg_%d", msgID), []byte(body)); err != nil {
			log.Printf("[TCP] Send failed: %v", err)
		} else {
			msgCount.Add(1)
			fmt.Printf("[TCP] Sent message #%d: %s\n", msgCount.Load(), body)
		}

		// 接收消息（带超时）
		select {
		case msg := <-client.MessageChannel():
			if msg.Type == sdk.SignalTypeBusinessDown {
				fmt.Printf("[TCP] Business down: %s\n", string(msg.Body))
			}
		case <-time.After(interval):
			// 达到发送间隔，继续发送下一条
			continue
		}
	}

	fmt.Printf("[TCP] Sent total %d messages, closing connection...\n", msgCount.Load())

	// 登出
	client.Logout(1)

	// 关闭
	client.Close()
	fmt.Printf("[TCP] Connection closed\n")
}

// TestExampleWS demonstrates WebSocket client usage with 5-minute sustained interaction
func TestExampleWS(t *testing.T) {
	// 1. 请求Auth服务生成Token
	authClient := sdk.NewAuthClient("localhost:8081")
	tokenResp, err := authClient.GenerateToken(&sdk.GenerateTokenRequest{
		DeviceID: "device-ws-001",
		UserID:   "user456",
		AppID:    "web-app",
	})
	if err != nil {
		log.Fatalf("GenerateToken failed: %v", err)
	}
	fmt.Printf("[WS] Token generated: %s..., gateway: %s, heartbeat: %ds\n",
		tokenResp.Token[:16], tokenResp.Gateway, tokenResp.Heartbeat)

	// 2. 使用Token连接Gateway
	client := ws.NewClient(
		sdk.WithAddr("ws://localhost:8089"),
		sdk.WithHeartbeatInterval(time.Duration(tokenResp.Heartbeat)*time.Second),
		sdk.WithMessageHandler(func(msg *sdk.Message) {
			fmt.Printf("[WS] Received: type=%v, msgID=%s, body=%s\n",
				msg.Type, msg.MsgID, string(msg.Body))
		}),
		sdk.WithConnectHandler(func(state sdk.ConnState, err error) {
			fmt.Printf("[WS] State: %v, err=%v\n", state, err)
		}),
	)

	// 连接服务器
	if err := client.Connect(); err != nil {
		log.Fatalf("[WS] Connect failed: %v", err)
	}

	// 3. 使用Token进行鉴权
	authResp, err := client.Auth(&sdk.AuthRequest{
		Token:    tokenResp.Token,
		UserID:   "user456",
		DeviceID: "device-ws-001",
		Platform: "web",
	})
	if err != nil {
		log.Fatalf("[WS] Auth failed: %v", err)
	}
	fmt.Printf("[WS] Auth success, heartbeat interval: %ds\n", authResp.HeartbeatInterval)

	// 4. 持续交互5分钟，每10秒发送一条消息
	duration := 5 * time.Minute
	interval := 10 * time.Second
	msgCount := atomic.Int32{}

	fmt.Printf("[WS] Starting sustained interaction for %v (sending every %v)\n",
		duration, interval)

	endTime := time.Now().Add(duration)
	msgID := 0

	for time.Now().Before(endTime) {
		// 发送业务消息
		msgID++
		body := fmt.Sprintf("hello from WebSocket client, message #%d at %v", msgID, time.Now().Format("15:04:05"))
		client.SendBusinessAsync(sdk.BusinessTypeIM, fmt.Sprintf("msg_%d", msgID), []byte(body))
		msgCount.Add(1)
		fmt.Printf("[WS] Sent message #%d: %s\n", msgCount.Load(), body)

		// 接收消息（带超时）
		select {
		case msg, ok := <-client.MessageChannel():
			if !ok {
				fmt.Printf("[WS] Message channel closed\n")
				return
			}
			if msg.Type == sdk.SignalTypeBusinessDown {
				fmt.Printf("[WS] Business down: %s\n", string(msg.Body))
			}
		case <-time.After(interval):
			// 达到发送间隔，继续发送下一条
			continue
		}
	}

	fmt.Printf("[WS] Sent total %d messages, closing connection...\n", msgCount.Load())

	// 登出
	client.Logout(1)

	// 关闭
	client.Close()
	fmt.Printf("[WS] Connection closed\n")
}
