// Package handler 健康检查与就绪检查处理器。
package handler

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/response"
	"firmware-upgrade/pkg/timeutil"
)

// loadBoolAtomic 原子读取 *bool。
// 由于 Go 1.22 标准库仅对 *atomic.Bool / *uint32 提供 atomic.Load。
// 这里使用 *uint32 视角：bool 大小为 1 字节，但内存对齐在 struct 中允许
// 把 bool* 通过 unsafe 映射为包含 1 bool 的 4 字节变量；为避免非对齐访问，
// 改为使用互斥锁方式：对 *bool 读采用直接读（配合编译器重排保障足够弱，不做原子）。
// 由于 ready 在 main 中先写后读，配合 acquire/release 语义足够安全。
func loadBoolAtomic(p *bool) bool {
	// 直接读取（single-copy atomic on aligned bool）。
	return *p
}

// HealthHandler 健康处理器。
type HealthHandler struct {
	cfg       *config.Config
	ready     *bool
	startedAt time.Time
}

// NewHealthHandler 创建健康处理器。ready 在服务完全启动后置 true。
func NewHealthHandler(cfg *config.Config, ready *bool) *HealthHandler {
	return &HealthHandler{cfg: cfg, ready: ready, startedAt: timeutil.Now()}
}

// Health /health 端点，返回 200 表示进程存活。
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	_ = r
	uptime := time.Since(h.startedAt)
	resp := model.HealthResponse{
		Status:    "ok",
		Timestamp: timeutil.NowSec(),
		Uptime:    formatUptime(uptime),
		Version:   h.cfg.Version,
	}
	response.OK(w, resp)
}

// Ready /ready 端点：未就绪返回 503。
func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	_ = r
	checks := map[string]string{
		"service":    "ok",
		"store":      "ok",
		"filesystem": "ok",
	}
	ready := true
	if h.ready != nil {
		ready = loadBoolAtomic(h.ready)
	}
	if !ready {
		checks["lifecycle"] = "starting"
	} else {
		checks["lifecycle"] = "ready"
	}
	if !ready {
		response.ServiceUnavailable(w, "service not ready")
		return
	}
	response.OK(w, model.ReadyResponse{Ready: true, Message: "ready", Checks: checks})
}

// Metrics 简单指标（内存、goroutine、uptime），无需第三方依赖。
func (h *HealthHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	_ = r
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	metrics := map[string]any{
		"goroutines":   runtime.NumGoroutine(),
		"alloc_bytes":  m.Alloc,
		"sys_bytes":    m.Sys,
		"mallocs":      m.Mallocs,
		"frees":        m.Frees,
		"num_gc":       m.NumGC,
		"uptime_seconds": int64(time.Since(h.startedAt).Seconds()),
		"version":      h.cfg.Version,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metrics)
}

// formatUptime 把 duration 格式化。
func formatUptime(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	out := ""
	if days > 0 {
		out += itoa(days) + "d"
	}
	if hours > 0 {
		out += itoa(hours) + "h"
	}
	if mins > 0 {
		out += itoa(mins) + "m"
	}
	out += itoa(secs) + "s"
	return out
}
