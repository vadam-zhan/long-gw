package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type AuthServer struct {
	addr string
}

func NewAuthServer(addr string) *AuthServer {
	return &AuthServer{addr: addr}
}

func (s *AuthServer) Start() error {
	r := gin.Default()

	v1 := r.Group("/v1/auth")
	{
		v1.POST("/token", s.handleToken)
		v1.POST("/validate", s.handleValidate)
	}

	return r.Run(s.addr)
}

func (s *AuthServer) handleToken(c *gin.Context) {
	var req struct {
		DeviceID string `json:"device_id" binding:"required"`
		UserID   string `json:"user_id"`
		AppID    string `json:"app_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": err.Error()})
		return
	}

	// TODO: 生成token逻辑
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "ok",
		"data": gin.H{
			"token":     "mock-token-" + req.DeviceID,
			"gateway":   "127.0.0.1:8080",
			"protocol":  "tcp",
			"heartbeat": 30,
		},
	})
}

func (s *AuthServer) handleValidate(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": err.Error()})
		return
	}

	// TODO: 验证token逻辑
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"msg":     "ok",
		"user_id": "mock-user",
	})
}
