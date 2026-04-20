package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
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
		slog.Error("validate token failed",
			"token", token,
			"error", err)
		return "", "", fmt.Errorf("validate token failed: %w", err)
	}

	if !authResp.Valid {
		slog.Warn("token invalid",
			"token", token,
			"msg", authResp.Msg)
		return "", "", fmt.Errorf("token invalid: %s", authResp.Msg)
	}

	userID := authResp.UserID
	deviceID := authResp.DeviceID

	slog.Info("connection auth success",
		"user_id", userID,
		"device_id", deviceID)

	return userID, deviceID, nil
}
