// Package handler 升级历史处理器。
package handler

import (
	"net/http"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/pkg/response"
)

// HistoryHandler 升级历史处理器。
type HistoryHandler struct {
	svc *service.HistoryService
}

// NewHistoryHandler 创建历史处理器。
func NewHistoryHandler(s *service.HistoryService) *HistoryHandler {
	return &HistoryHandler{svc: s}
}

// List 分页历史。
func (h *HistoryHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pn, ps := PageParam(q)
	req := &model.ListHistoryRequest{
		TaskID:    QueryString(q, "task_id", ""),
		DeviceID:  QueryString(q, "device_id", ""),
		ModelID:   QueryString(q, "model_id", ""),
		Status:    QueryString(q, "status", ""),
		Keyword:   QueryString(q, "keyword", ""),
		SortBy:    QueryString(q, "sort_by", "started_at"),
		SortOrder: QueryString(q, "sort_order", "desc"),
		StartTs:   QueryInt64(q, "start_ts", 0),
		EndTs:     QueryInt64(q, "end_ts", 0),
		PageNum:   pn,
		PageSize:  ps,
	}
	list, total, err := h.svc.List(r.Context(), req)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.Page(w, list, pn, ps, total)
}
