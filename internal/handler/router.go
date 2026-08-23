// Package handler HTTP 路由：使用 net/http 标准库实现。
package handler

import (
	"context"
	"net/http"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/pkg/logger"
)

// BuildRouter 构建总路由。ready 会在启动完成后被切换为 true。
func BuildRouter(cfg *config.Config, svc *service.Services, ready *bool) http.Handler {
	mux := http.NewServeMux()

	health := NewHealthHandler(cfg, ready)
	mux.HandleFunc("/health", health.Health)
	mux.HandleFunc("/ready", health.Ready)
	mux.HandleFunc("/metrics", health.Metrics)

	apiV1 := "/api/v1"

	modelH := NewModelHandler(svc.Model)
	mux.HandleFunc(apiV1+"/models", dispatch(modelH.List, modelH.Create))
	mux.HandleFunc(apiV1+"/models/", withID(modelH.Get, modelH.Update, modelH.Delete, apiV1+"/models/"))

	fwH := NewFirmwareHandler(svc.Firmware, svc.FileOp, cfg)
	mux.HandleFunc(apiV1+"/firmwares", dispatch(fwH.List, fwH.Create))
	mux.HandleFunc(apiV1+"/firmwares/upload", fwH.Upload)
	mux.HandleFunc(apiV1+"/firmwares/", firmwareDispatch(fwH, apiV1+"/firmwares/"))

	devH := NewDeviceHandler(svc.Device, svc.Poll, svc.Progress)
	mux.HandleFunc(apiV1+"/devices", dispatch(devH.List, devH.Register))
	mux.HandleFunc(apiV1+"/devices/heartbeat", devH.Heartbeat)
	mux.HandleFunc(apiV1+"/devices/poll", devH.PollUpgrade)
	mux.HandleFunc(apiV1+"/devices/progress", devH.ReportProgress)
	mux.HandleFunc(apiV1+"/devices/", withID(devH.Get, devH.Update, devH.Delete, apiV1+"/devices/"))

	taskH := NewTaskHandler(svc.Task)
	mux.HandleFunc(apiV1+"/tasks", dispatch(taskH.List, taskH.Create))
	mux.HandleFunc(apiV1+"/tasks/", taskDispatch(taskH, apiV1+"/tasks/"))

	histH := NewHistoryHandler(svc.History)
	mux.HandleFunc(apiV1+"/histories", dispatch(histH.List, emptyPost))

	statH := NewStatsHandler(svc.Stats)
	mux.HandleFunc(apiV1+"/stats/overview", statH.Overview)
	mux.HandleFunc(apiV1+"/stats/daily", statH.Daily)

	spa := NewStaticHandler()
	mux.HandleFunc("/", spa.Serve)

	// 中间件链（执行顺序：RequestID → AccessLog → CORS → BodyLimit → JSONOnly → mux）。
	cors := CORSMiddleware(DefaultCORS())
	bodyLimit := BodyLimitMiddleware(maxUploadLimit(cfg))
	var h http.Handler = mux
	h = JSONOnlyMiddleware(h)
	h = bodyLimit(h)
	h = cors(h)
	h = AccessLogMiddleware(h)
	h = RequestIDMiddleware(h)
	h = RecoveryMiddleware(h)
	return h
}

// dispatch 依据 HTTP 方法分发列表/创建。
func dispatch(get, post http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			get(w, r)
		case http.MethodPost:
			post(w, r)
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// withID 依据路径尾段与 HTTP 方法分发 Get / Update / Delete。
func withID(get, put, del http.HandlerFunc, prefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := PathTail(r.URL.Path, prefix)
		if id == "" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			r = r.WithContext(SetPathID(r.Context(), id))
			get(w, r)
		case http.MethodPut, http.MethodPatch:
			r = r.WithContext(SetPathID(r.Context(), id))
			put(w, r)
		case http.MethodDelete:
			r = r.WithContext(SetPathID(r.Context(), id))
			del(w, r)
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// firmwareDispatch 固件路径下的多种子动作：详情、更新、状态、下载、删除。
func firmwareDispatch(h *FirmwareHandler, prefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tail := PathTail(r.URL.Path, prefix)
		segs := SplitPath(tail)
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			switch {
			case len(segs) == 1:
				r = r.WithContext(SetPathID(r.Context(), segs[0]))
				h.Get(w, r)
			case len(segs) == 2 && segs[1] == "download":
				r = r.WithContext(SetPathID(r.Context(), segs[0]))
				h.Download(w, r)
			default:
				http.NotFound(w, r)
			}
		case http.MethodPut, http.MethodPatch:
			if len(segs) == 1 {
				r = r.WithContext(SetPathID(r.Context(), segs[0]))
				h.UpdateStatus(w, r)
				return
			}
			http.NotFound(w, r)
		case http.MethodDelete:
			if len(segs) == 1 {
				r = r.WithContext(SetPathID(r.Context(), segs[0]))
				h.Delete(w, r)
				return
			}
			http.NotFound(w, r)
		case http.MethodPost:
			if len(segs) == 2 && segs[1] == "status" {
				r = r.WithContext(SetPathID(r.Context(), segs[0]))
				h.UpdateStatus(w, r)
				return
			}
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// taskDispatch 任务路径下：详情/状态变更/删除。
func taskDispatch(h *TaskHandler, prefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tail := PathTail(r.URL.Path, prefix)
		segs := SplitPath(tail)
		if len(segs) == 0 {
			http.NotFound(w, r)
			return
		}
		id := segs[0]
		sub := ""
		if len(segs) >= 2 {
			sub = segs[1]
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			switch sub {
			case "", "detail":
				r = r.WithContext(SetPathID(r.Context(), id))
				h.Detail(w, r)
			default:
				http.NotFound(w, r)
			}
		case http.MethodPut, http.MethodPatch, http.MethodPost:
			r = r.WithContext(SetPathID(r.Context(), id))
			h.Action(w, r)
		case http.MethodDelete:
			r = r.WithContext(SetPathID(r.Context(), id))
			h.Delete(w, r)
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// emptyPost 用于历史 POST 保留位。
func emptyPost(w http.ResponseWriter, r *http.Request) {
	_ = r
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

// maxUploadLimit 上传大小限制（+1MB 给 multipart header）。
func maxUploadLimit(cfg *config.Config) int64 {
	if cfg == nil {
		return 256*1024*1024 + 1<<20
	}
	return cfg.FirmwareMaxSize + 1<<20
}

// pathIDKey 路径 ID 在 context 中的键（私有）。
type pathIDKey struct{}

// SetPathID 将路径 ID 写入 context。
func SetPathID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, pathIDKey{}, id)
}

// PathID 从 context 中读取路径 ID。
func PathID(ctx context.Context) string {
	v, ok := ctx.Value(pathIDKey{}).(string)
	if !ok {
		return ""
	}
	return v
}

// 占位，防止包未被引用（logger/time 可能在某些精简构建被移除）。
var _ = logger.Info
var _ = time.Second
