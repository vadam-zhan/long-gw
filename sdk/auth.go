package sdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// AuthClient Auth服务客户端
type AuthClient struct {
	addr   string
	client *http.Client
}

// NewAuthClient 创建Auth客户端
func NewAuthClient(addr string) *AuthClient {
	return &AuthClient{
		addr: addr,
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// GenerateTokenRequest 生成Token请求
type GenerateTokenRequest struct {
	DeviceID string `json:"device_id"`
	UserID   string `json:"user_id"`
	AppID    string `json:"app_id,omitempty"`
}

// GenerateTokenResponse 生成Token响应
type GenerateTokenResponse struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data *TokenResponse  `json:"data,omitempty"`
}

// GenerateToken 请求Auth服务生成Token
func (c *AuthClient) GenerateToken(req *GenerateTokenRequest) (*TokenResponse, error) {
	jsonBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	url := fmt.Sprintf("http://%s/v1/auth/token", c.addr)
	resp, err := c.client.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("call auth service failed: %w", err)
	}
	defer resp.Body.Close()

	var generateResp GenerateTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&generateResp); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}

	if generateResp.Code != 0 {
		return nil, fmt.Errorf("auth service error: %s", generateResp.Msg)
	}

	return generateResp.Data, nil
}
