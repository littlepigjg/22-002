// Package middleware 本项目内部中间件补充：Context 注入、幂等键鉴权、速率限制。
package middleware

import (
	"context"
	"net/http"
	"sync"
	"time"

	"firmware-upgrade/pkg/logger"
	"firmware-upgrade/pkg/safemap"
)

// userKey 上下文字段键。
type userKey struct{}

// UserInfo 当前请求用户。
type UserInfo struct {
	UserID string
	Roles  []string
	Tenant string
}

// WithUser 注入用户。
func WithUser(ctx context.Context, u *UserInfo) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}

// GetUser 从上下文取出用户。
func GetUser(ctx context.Context) (*UserInfo, bool) {
	v := ctx.Value(userKey{})
	if v == nil {
		return nil, false
	}
	u, ok := v.(*UserInfo)
	return u, ok
}

// UserMiddleware 从 Header X-User-Id / X-Roles / X-Tenant 读取用户上下文。
// 注意：本项目纯标准库，不集成鉴权服务器；该中间件仅作为模拟，生产环境必须对接真实鉴权。
func UserMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := r.Header.Get("X-User-Id")
		if uid == "" {
			next.ServeHTTP(w, r)
			return
		}
		u := &UserInfo{UserID: uid}
		if roles := r.Header["X-Roles"]; len(roles) > 0 {
			u.Roles = append([]string{}, roles...)
		}
		if tenant := r.Header.Get("X-Tenant"); tenant != "" {
			u.Tenant = tenant
		}
		ctx := logger.WithUserID(r.Context(), uid)
		ctx = WithUser(ctx, u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RateLimiterMiddleware 基于客户端 IP 的简易令牌桶速率限制。
// rpm 表示每分钟最大请求数。0 或负数不限制。
func RateLimiterMiddleware(rpm int) func(http.Handler) http.Handler {
	if rpm <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	buckets := safemap.New[string, *rateBucket]()
	stopCh := make(chan struct{})
	go cleanupBuckets(buckets, stopCh)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := clientIP(r) + ":" + r.URL.Path
			b := buckets.ComputeIfAbsent(key, func() *rateBucket {
				return newBucket(rpm)
			})
			if !b.take() {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type rateBucket struct {
	mu       sync.Mutex
	permits  float64
	rate     float64 // 每秒令牌数
	cap      float64
	last     time.Time
}

func newBucket(rpm int) *rateBucket {
	rate := float64(rpm) / 60.0
	return &rateBucket{permits: rate, cap: float64(rpm) / 2, rate: rate, last: time.Now()}
}

func (b *rateBucket) take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.permits += elapsed * b.rate
	if b.permits > b.cap {
		b.permits = b.cap
	}
	if b.permits >= 1.0 {
		b.permits -= 1.0
		return true
	}
	return false
}

func cleanupBuckets(m *safemap.Map[string, *rateBucket], stopCh chan struct{}) {
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-stopCh:
			return
		case <-t.C:
			keys := m.Keys()
			for _, k := range keys {
				v, ok := m.Get(k)
				if !ok {
					continue
				}
				v.mu.Lock()
				stale := time.Since(v.last) > 10*time.Minute
				v.mu.Unlock()
				if stale {
					m.Delete(k)
				}
			}
		}
	}
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := len(xff); i > 512 {
			xff = xff[:512]
		}
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}
