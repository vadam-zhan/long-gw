package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/vadam-zhan/long-gw/gateway/internal/logger"
	"go.uber.org/zap"
)

type AuthResponse struct {
	Code     int    `json:"code"`
	Msg      string `json:"msg"`
	Valid    bool   `json:"valid"`
	UserID   string `json:"user_id"`
	DeviceID string `json:"device_id"`
}

type AuthVerifier struct {
	authAddr string
}

func NewAuthVerifier(authAddr string) *AuthVerifier {
	return &AuthVerifier{
		authAddr: authAddr,
	}
}

// validateToken 调用auth服务验证token
func (h *AuthVerifier) validateToken(token string) (*AuthResponse, error) {
	reqBody := map[string]string{"token": token}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	url := fmt.Sprintf("http://%s/v1/auth/validate", h.authAddr)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBody))
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

func (h *AuthVerifier) Verify(token string) (string, string, error) {
	// 调用auth服务验证token
	authResp, err := h.validateToken(token)
	if err != nil {
		logger.Error("validate token failed",
			zap.String("token", token),
			zap.Error(err))
		return "", "", fmt.Errorf("validate token failed: %w", err)
	}

	if !authResp.Valid {
		logger.Warn("token invalid",
			zap.String("token", token),
			zap.String("msg", authResp.Msg))
		return "", "", fmt.Errorf("token invalid: %s", authResp.Msg)
	}

	userID := authResp.UserID
	deviceID := authResp.DeviceID

	logger.Info("connection auth success",
		zap.String("user_id", userID),
		zap.String("device_id", deviceID))

	return userID, deviceID, nil
}
