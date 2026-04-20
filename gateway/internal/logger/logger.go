package logger

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// ==================== 全局固定配置 ====================
const (
	ServiceName = "gateway-service" // 服务名
	Env         = "test"            // 环境 dev/test/prod
	Version     = "v1.0.0"          // 版本
	LogPath     = "./logs"          // 日志目录
)

// ==================== 上下文Key定义 ====================
type ctxKey string

const (
	TraceIDKey ctxKey = "trace_id"
)

// ==================== 初始化生产级Slog ====================
func Init(level string, logPath string) {
	// 1. 创建日志目录
	if logPath == "" {
		logPath = LogPath
	}
	_ = os.MkdirAll(logPath, 0755)

	// 2. 日志滚动切割（生产必备）
	rotate := &lumberjack.Logger{
		Filename:   filepath.Join(logPath, "app.log"),
		MaxSize:    128,  // 单文件最大128MB
		MaxBackups: 30,   // 保留30个文件
		MaxAge:     7,    // 保留7天
		Compress:   true, // 压缩
		LocalTime:  true,
	}

	slogLevel := slog.LevelInfo
	if level != "" {
		if err := slogLevel.UnmarshalText([]byte(level)); err != nil {
			slogLevel = slog.LevelInfo
		}
	}

	// 3. Handler 配置
	opts := &slog.HandlerOptions{
		Level:     slogLevel,
		AddSource: true, // 自动打印文件行号
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// 美化时间
			if a.Key == slog.TimeKey {
				return slog.String("time", time.Now().Format("2006-01-02 15:04:05.000"))
			}
			return a
		},
	}

	// 4. 双输出：JSON文件 + 文本控制台
	fileHandler := slog.NewJSONHandler(rotate, opts)
	consoleHandler := slog.NewTextHandler(os.Stdout, opts)
	multiHandler := NewMultiHandler(fileHandler, consoleHandler)

	// 5. 包装自定义Handler（自动提取trace + 自动打印堆栈）
	customHandler := NewCustomHandler(multiHandler)

	// 6. 绑定全局固定字段
	rootLogger := slog.New(customHandler).With(
		slog.String("service", ServiceName),
		slog.String("env", Env),
		slog.String("version", Version),
	)

	// 设置全局默认Logger
	slog.SetDefault(rootLogger)
	slog.Info("slog 初始化完成")
}

// ==================== 自定义Handler：自动提取trace + 错误堆栈 ====================
type CustomHandler struct {
	handler slog.Handler
}

func NewCustomHandler(h slog.Handler) *CustomHandler {
	return &CustomHandler{handler: h}
}

func (h *CustomHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

func (h *CustomHandler) Handle(ctx context.Context, r slog.Record) error {
	// 1. 自动从ctx提取trace_id
	if traceID, ok := ctx.Value(TraceIDKey).(string); ok {
		r.AddAttrs(slog.String("trace_id", traceID))
	}

	// 2. ERROR级别自动添加堆栈信息
	if r.Level == slog.LevelError {
		stack := getStack()
		r.AddAttrs(slog.String("stack", stack))
	}

	return h.handler.Handle(ctx, r)
}

func (h *CustomHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &CustomHandler{handler: h.handler.WithAttrs(attrs)}
}

func (h *CustomHandler) WithGroup(name string) slog.Handler {
	return &CustomHandler{handler: h.handler.WithGroup(name)}
}

// ==================== 获取goroutine堆栈 ====================
func getStack() string {
	buf := make([]byte, 4096<<2)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}
