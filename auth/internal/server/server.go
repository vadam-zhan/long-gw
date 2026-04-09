package server

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// TokenInfo token信息
type TokenInfo struct {
	Token    string
	UserID   string
	DeviceID string
	AppID    string
	Gateway  string
	Protocol string
	ExpireAt time.Time
	CreatedAt time.Time
}

// AuthServer 认证服务
type AuthServer struct {
	addr   string
	tokens sync.Map // token -> TokenInfo
}

// NewAuthServer 创建认证服务
func NewAuthServer(addr string) *AuthServer {
	return &AuthServer{addr: addr}
}

// Start 启动服务
func (s *AuthServer) Start() error {
	// 启动token清理goroutine
	go s.cleanExpiredTokens()

	r := gin.Default()

	v1 := r.Group("/v1/auth")
	{
		v1.POST("/token", s.handleToken)
		v1.POST("/validate", s.handleValidate)
	}

	return r.Run(s.addr)
}

// handleToken 生成token
func (s *AuthServer) handleToken(c *gin.Context) {
	var req struct {
		DeviceID string `json:"device_id" binding:"required"`
		UserID   string `json:"user_id" binding:"required"`
		AppID    string `json:"app_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": err.Error()})
		return
	}

	// 生成token
	token, err := generateToken(32)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": -1, "msg": "generate token failed"})
		return
	}

	// Token 24小时过期
	expireAt := time.Now().Add(24 * time.Hour)

	// 存储token信息
	tokenInfo := &TokenInfo{
		Token:    token,
		UserID:   req.UserID,
		DeviceID: req.DeviceID,
		AppID:    req.AppID,
		Gateway:  "127.0.0.1:8089",
		Protocol: "tcp",
		ExpireAt: expireAt,
		CreatedAt: time.Now(),
	}
	s.tokens.Store(token, tokenInfo)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "ok",
		"data": gin.H{
			"token":      token,
			"gateway":    tokenInfo.Gateway,
			"protocol":   tokenInfo.Protocol,
			"heartbeat":  30,
			"expire_at":  expireAt.Unix(),
		},
	})
}

// handleValidate 验证token
func (s *AuthServer) handleValidate(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": err.Error()})
		return
	}

	// 查询token
	val, ok := s.tokens.Load(req.Token)
	if !ok {
		c.JSON(http.StatusOK, gin.H{
			"code":    401,
			"msg":     "token not found",
			"valid":   false,
		})
		return
	}

	tokenInfo := val.(*TokenInfo)

	// 检查是否过期
	if time.Now().After(tokenInfo.ExpireAt) {
		s.tokens.Delete(req.Token)
		c.JSON(http.StatusOK, gin.H{
			"code":    401,
			"msg":     "token expired",
			"valid":   false,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"msg":     "ok",
		"valid":   true,
		"user_id":  tokenInfo.UserID,
		"device_id": tokenInfo.DeviceID,
	})
}

// cleanExpiredTokens 清理过期token
func (s *AuthServer) cleanExpiredTokens() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()
		s.tokens.Range(func(key, value interface{}) bool {
			tokenInfo := value.(*TokenInfo)
			if now.After(tokenInfo.ExpireAt) {
				s.tokens.Delete(key)
			}
			return true
		})
	}
}

// generateToken 生成随机token
func generateToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
