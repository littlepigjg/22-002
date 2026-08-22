// Package logger 提供结构化日志工具，基于标准库 log/slog 实现。
// 支持 JSON 格式输出、日志级别过滤、请求追踪字段注入。
package logger

import (
	"context"
	"log/slog"
	"os"
	"sync"
)

// ctxKey 定义上下文中日志字段的键类型，避免与其他包冲突。
type ctxKey string

const (
	// TraceIDKey 请求追踪 ID 在上下文中的键。
	TraceIDKey ctxKey = "trace_id"
	// UserIDKey 用户 ID 在上下文中的键。
	UserIDKey ctxKey = "user_id"
)

var (
	// defaultLogger 默认全局日志实例。
	defaultLogger *slog.Logger
	// once 保证日志初始化仅执行一次。
	once sync.Once
)

// Init 初始化默认全局日志器。
// level 可选 debug/info/warn/error，忽略大小写。
// 若传入空字符串则默认 info。
func Init(level string) {
	once.Do(func() {
		lv := parseLevel(level)
		handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level:       lv,
			AddSource:   true,
			ReplaceAttr: replaceAttr,
		})
		defaultLogger = slog.New(handler)
		slog.SetDefault(defaultLogger)
	})
}

// parseLevel 将字符串解析为 slog.Level，默认返回 Info。
func parseLevel(s string) slog.Level {
	switch s {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "warn", "WARN", "warning", "WARNING":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// replaceAttr 替换默认日志字段，美化键名。
func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.SourceKey:
		src, ok := a.Value.Any().(*slog.Source)
		if ok {
			return slog.String("src", src.File+":"+itoa(src.Line))
		}
	}
	return a
}

// itoa 小型整数转字符串工具，避免额外导入。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

// Default 返回默认日志器，未初始化时会自动以 info 级别初始化。
func Default() *slog.Logger {
	if defaultLogger == nil {
		Init("info")
	}
	return defaultLogger
}

// Debug 输出 Debug 级别日志。
func Debug(msg string, args ...any) {
	Default().Debug(msg, args...)
}

// Info 输出 Info 级别日志。
func Info(msg string, args ...any) {
	Default().Info(msg, args...)
}

// Warn 输出 Warn 级别日志。
func Warn(msg string, args ...any) {
	Default().Warn(msg, args...)
}

// Error 输出 Error 级别日志。
func Error(msg string, args ...any) {
	Default().Error(msg, args...)
}

// WithContext 从上下文中提取追踪字段并返回携带这些字段的日志器。
func WithContext(ctx context.Context) *slog.Logger {
	l := Default()
	if tid := ctx.Value(TraceIDKey); tid != nil {
		l = l.With(string(TraceIDKey), tid)
	}
	if uid := ctx.Value(UserIDKey); uid != nil {
		l = l.With(string(UserIDKey), uid)
	}
	return l
}

// WithTraceID 返回一个附带 trace_id 的新 context。
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, TraceIDKey, traceID)
}

// WithUserID 返回一个附带 user_id 的新 context。
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, UserIDKey, userID)
}

// NewTestLogger 用于单元测试，返回一个基于文本的 logger。
func NewTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
