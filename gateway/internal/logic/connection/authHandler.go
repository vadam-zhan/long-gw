package connection

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	gateway "github.com/vadam-zhan/long-gw/common-protocol/v1"
	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"github.com/vadam-zhan/long-gw/gateway/internal/types"
	"go.uber.org/zap"
)

// AuthResponse auth服务验证响应的结构
type AuthResponse struct {
	Code     int    `json:"code"`
	Msg      string `json:"msg"`
	Valid    bool   `json:"valid"`
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
}

// AuthHandler 处理鉴权请求
type AuthHandler struct {
	authClient *http.Client
	authAddr   string
}

var (
	authHandler     *AuthHandler
	authHandlerOnce sync.Once
)

// InitAuthHandler 初始化全局AuthHandler
func InitAuthHandler(authAddr string) {
	authHandlerOnce.Do(func() {
		authHandler = &AuthHandler{
			authAddr: authAddr,
			authClient: &http.Client{
				Timeout: 3 * time.Second,
			},
		}
	})
	GlobalHandlerRegistry.Register(gateway.SignalType_SIGNAL_TYPE_AUTH_REQUEST, authHandler)
}

// GetAuthHandler 获取全局AuthHandler实例
func GetAuthHandler() *AuthHandler {
	return authHandler
}

// validateToken 调用auth服务验证token
func (h *AuthHandler) validateToken(token string) (*AuthResponse, error) {
	reqBody := map[string]string{"token": token}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	url := fmt.Sprintf("http://%s/v1/auth/validate", h.authAddr)
	resp, err := h.authClient.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("call auth service failed: %w", err)
	}
	defer resp.Body.Close()

	var authResp AuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return nil, fmt.Errorf("decode response failed: %w", err)
	}

	return &authResp, nil
}

func (h *AuthHandler) Handle(conn ConnectionAccessor, msg *types.Message) error {
	token := msg.AuthToken

	// 调用auth服务验证token
	authResp, err := h.validateToken(token)
	if err != nil {
		logger.Error("validate token failed",
			zap.String("token", token),
			zap.Error(err))

		msg.Type = gateway.SignalType_SIGNAL_TYPE_AUTH_RESPONSE
		conn.GetWriteCh() <- msg
		return err
	}

	if !authResp.Valid {
		logger.Warn("token invalid",
			zap.String("token", token),
			zap.String("msg", authResp.Msg))

		msg.Type = gateway.SignalType_SIGNAL_TYPE_AUTH_RESPONSE
		conn.GetWriteCh() <- msg
		return fmt.Errorf("token invalid: %s", authResp.Msg)
	}

	userID := msg.UserID
	deviceID := msg.DeviceID

	conn.SetUserInfo(userID, deviceID)
	conn.SetAuthed(true)

	conn.RouterRegister(userID, deviceID)

	logger.Info("connection auth success",
		zap.String("user_id", userID),
		zap.String("device_id", deviceID))

	msg.Type = gateway.SignalType_SIGNAL_TYPE_AUTH_RESPONSE
	select {
	case conn.GetWriteCh() <- msg:
	default:
		logger.Warn("write channel full, closing slow client",
			zap.String("remote", conn.GetRemoteAddr()))
	}

	return nil
}
