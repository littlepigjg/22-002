// Package httpx 提供 HTTP 辅助工具：Query 绑定、分页响应包装、方法常量。
package httpx

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// MethodGet 等方法常量（补标准库缺失）。
const (
	MethodGet     = http.MethodGet
	MethodPost    = http.MethodPost
	MethodPut     = http.MethodPut
	MethodDelete  = http.MethodDelete
	MethodPatch   = http.MethodPatch
	MethodOptions = http.MethodOptions
	MethodHead    = http.MethodHead
)

// QueryString 读取字符串参数。
func QueryString(v url.Values, key, def string) string {
	s := v.Get(key)
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// QueryInt 读取整数参数。
func QueryInt(v url.Values, key string, def int) int {
	s := v.Get(key)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// QueryInt64 读取 int64 参数。
func QueryInt64(v url.Values, key string, def int64) int64 {
	s := v.Get(key)
	if s == "" {
		return def
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// QueryBool 读取布尔参数。
func QueryBool(v url.Values, key string, def bool) bool {
	s := strings.ToLower(strings.TrimSpace(v.Get(key)))
	switch s {
	case "":
		return def
	case "1", "true", "yes", "on", "y", "t":
		return true
	case "0", "false", "no", "off", "n", "f":
		return false
	}
	return def
}

// QueryStrings 读取重复字符串参数。
func QueryStrings(v url.Values, key string) []string {
	values := v[key]
	out := make([]string, 0, len(values))
	for _, s := range values {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// ClientIP 从请求中解析客户端真实 IP。
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		idx := strings.Index(xff, ",")
		if idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	host = strings.Trim(host, "[]")
	return host
}

// IsGet 判断是否 GET / HEAD 请求。
func IsGet(r *http.Request) bool {
	return r.Method == MethodGet || r.Method == MethodHead
}

// IsWriteMethod 判断是否为会改变状态的方法。
func IsWriteMethod(r *http.Request) bool {
	switch r.Method {
	case MethodPost, MethodPut, MethodPatch, MethodDelete:
		return true
	}
	return false
}
