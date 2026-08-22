// Package handler 统计处理器。
package handler

import (
	"net/http"

	"firmware-upgrade/internal/service"
	"firmware-upgrade/pkg/response"
)

// StatsHandler 统计 HTTP 处理器。
type StatsHandler struct {
	svc *service.StatsService
}

// NewStatsHandler 创建统计处理器。
func NewStatsHandler(s *service.StatsService) *StatsHandler {
	return &StatsHandler{svc: s}
}

// Overview 返回全局统计总览。
func (h *StatsHandler) Overview(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.Get(r.Context())
	if err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, res)
}

// Daily 返回最近 N 天的每日升级统计。
func (h *StatsHandler) Daily(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days := QueryInt(q, "days", 14)
	if days <= 0 {
		days = 7
	}
	if days > 180 {
		days = 180
	}
	res, err := h.svc.Get(r.Context())
	if err != nil {
		WriteError(w, err)
		return
	}
	arr := res.DailyUpgradeHistory
	if len(arr) > days {
		arr = arr[len(arr)-days:]
	}
	response.OK(w, arr)
}
