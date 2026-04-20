package handler

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// AdminHandler 管理接口处理
type AdminHandler struct {
	session SessionAccessor
}

// NewAdminHandler 创建 AdminHandler
func NewAdminHandler(sess SessionAccessor) *AdminHandler {
	return &AdminHandler{session: sess}
}

// HealthHandler 健康检查
func (h *AdminHandler) HealthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "ok",
		"time": time.Now().Unix(),
	})
}

// StatsHandler 统计信息
func (h *AdminHandler) StatsHandler(c *gin.Context) {
	res := StatsHandler(h.session)
	c.JSON(http.StatusOK, res)
}

// KickHandler 踢用户下线
func (h *AdminHandler) KickHandler(c *gin.Context) {
	var req struct {
		UserID string `json:"user_id" binding:"required"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": -1,
			"msg":  "invalid params: " + err.Error(),
		})
		return
	}

	localRouter := h.session.GetLocalRouter()
	conns, ok := localRouter.GetByUserID(req.UserID)
	if ok {
		for _, conn := range conns {
			conn.Close(nil)
		}
	}

	slog.Info("kick user",
		"user_id", req.UserID,
		"reason", req.Reason)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
	})
}
