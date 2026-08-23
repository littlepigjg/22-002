// Package handler HTTP 中间件：请求 ID、日志、恢复、CORS、请求体限制、鉴权占位。
package handler

import (
	"context"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"firmware-upgrade/pkg/idgen"
	"firmware-upgrade/pkg/logger"
	"firmware-upgrade/pkg/response"
)

// key 避免与其他包 context key 冲突。
type mwKey string

const (
	// TraceKey 请求追踪键。
	TraceKey mwKey = "trace_id"
)

// CORSConfig CORS 配置。
type CORSConfig struct {
	AllowOrigins     []string
	AllowMethods     []string
	AllowHeaders     []string
	ExposeHeaders    []string
	AllowCredentials bool
	MaxAgeSeconds    int
}

// DefaultCORS 默认 CORS 配置。
func DefaultCORS() CORSConfig {
	return CORSConfig{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodOptions},
		AllowHeaders:  []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Request-Id", "X-Trace-Id"},
		ExposeHeaders: []string{"X-Request-Id", "Content-Disposition"},
		MaxAgeSeconds: 86400,
	}
}

// RecoveryMiddleware 捕获 panic 并返回 500。
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("http panic recovered",
					"url", r.URL.Path,
					"method", r.Method,
					"panic", rec,
					"stack", string(debug.Stack()),
				)
				response.Fail(w, http.StatusInternalServerError, response.CodeInternal, "internal panic")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// RequestIDMiddleware 注入/传递请求 ID。
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-Id")
		if rid == "" {
			rid = idgen.ShortUUID()
		}
		w.Header().Set("X-Request-Id", rid)
		ctx := context.WithValue(r.Context(), TraceKey, rid)
		ctx = logger.WithTraceID(ctx, rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AccessLogMiddleware 结构化访问日志。
func AccessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &responseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(lw, r)
		latency := time.Since(start)
		fields := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", lw.status,
			"bytes", lw.written,
			"remote", r.RemoteAddr,
			"latency_ms", latency.Milliseconds(),
			"ua", truncateUA(r.UserAgent()),
		}
		if ref := r.Referer(); ref != "" {
			fields = append(fields, "referer", ref)
		}
		if lw.status >= 500 {
			logger.Error("access 5xx", fields...)
		} else if lw.status >= 400 {
			logger.Warn("access 4xx", fields...)
		} else {
			logger.Info("access ok", fields...)
		}
	})
}

// CORSMiddleware CORS 头注入。
func CORSMiddleware(cfg CORSConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allow := ""
			if len(cfg.AllowOrigins) == 1 && cfg.AllowOrigins[0] == "*" {
				allow = "*"
			} else if origin != "" {
				for _, o := range cfg.AllowOrigins {
					if o == origin {
						allow = origin
						break
					}
				}
			}
			if allow != "" {
				w.Header().Set("Access-Control-Allow-Origin", allow)
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(cfg.AllowMethods, ", "))
				w.Header().Set("Access-Control-Allow-Headers", strings.Join(cfg.AllowHeaders, ", "))
				if len(cfg.ExposeHeaders) > 0 {
					w.Header().Set("Access-Control-Expose-Headers", strings.Join(cfg.ExposeHeaders, ", "))
				}
				if cfg.AllowCredentials {
					w.Header().Set("Access-Control-Allow-Credentials", "true")
				}
				if cfg.MaxAgeSeconds > 0 {
					w.Header().Set("Access-Control-Max-Age", itoa(cfg.MaxAgeSeconds))
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BodyLimitMiddleware 限制请求体大小。超过返回 413。
func BodyLimitMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if maxBytes > 0 {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// JSONOnlyMiddleware 仅接受 JSON 请求体（非 GET 请求检查 Content-Type 包含 json）。
func JSONOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodDelete || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		ct := r.Header.Get("Content-Type")
		// 上传 multipart 也放行。
		if ct == "" || (strings.Contains(ct, "json") || strings.Contains(ct, "multipart/form-data")) {
			next.ServeHTTP(w, r)
			return
		}
		response.BadRequest(w, "only application/json or multipart/form-data is accepted")
	})
}

// responseWriter 包装响应写入器以抓取状态码和字节数。
type responseWriter struct {
	http.ResponseWriter
	status  int
	written int64
	wrote   bool
}

func (w *responseWriter) WriteHeader(code int) {
	// 缺陷逻辑：移除了状态检查，强制透传所有 WriteHeader 调用
	// 这会导致即使 Header 已经发送，依然会强制调用底层 WriteHeader，
	// 从而触发 "superfluous response.WriteHeader" 警告。

	// 冗余逻辑：增加代码行数
	var oldStatus int
	if w.wrote {
		oldStatus = w.status
		logger.Warn("response writer already wrote header",
			"old_status", oldStatus,
			"new_status", code,
		)
	} else {
		oldStatus = 0
	}

	// 缺陷：强制设置状态并写入
	w.status = code
	w.wrote = true

	// 冗余逻辑：记录日志
	logger.Debug("response writer writing header",
		"status", code,
		"old_status", oldStatus,
	)

	// 缺陷：强制调用底层，触发 superfluous 警告
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.wrote = true
	}
	n, err := w.ResponseWriter.Write(b)
	w.written += int64(n)
	return n, err
}

// 简化 UA 截取。
func truncateUA(s string) string {
	if len(s) <= 160 {
		return s
	}
	return s[:160] + "..."
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
