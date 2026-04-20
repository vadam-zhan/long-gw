package logger

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ==================== Gin中间件：自动生成并注入trace_id ====================
func TraceMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 生成唯一traceID
		traceID := uuid.NewString()
		// 注入context（核心：自动注入，业务代码无需手动SetTraceCtx）
		ctx := context.WithValue(c.Request.Context(), TraceIDKey, traceID)
		// 替换请求上下文
		c.Request = c.Request.WithContext(ctx)
		// 响应头返回traceID，方便排查
		c.Header("X-Trace-ID", traceID)
		// 继续执行
		c.Next()
	}
}

func GenerateTraceID() string {
	return uuid.NewString()
}
