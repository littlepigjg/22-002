// Package handler 设备型号处理器。
package handler

import (
	"net/http"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/pkg/response"
)

// ModelHandler 型号 HTTP 处理器。
type ModelHandler struct {
	svc *service.ModelService
}

// NewModelHandler 创建型号处理器。
func NewModelHandler(s *service.ModelService) *ModelHandler {
	return &ModelHandler{svc: s}
}

// Create 创建型号。
func (h *ModelHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req model.CreateModelRequest
	if !ParseJSONBody(w, r, &req) {
		return
	}
	res, err := h.svc.Create(r.Context(), &req)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, res)
}

// Update 更新型号。
func (h *ModelHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := PathID(r.Context())
	var req model.UpdateModelRequest
	if !ParseJSONBody(w, r, &req) {
		return
	}
	res, err := h.svc.Update(r.Context(), id, &req)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, res)
}

// Get 获取型号详情。
func (h *ModelHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := PathID(r.Context())
	res, err := h.svc.Get(r.Context(), id)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, res)
}

// Delete 删除型号。
func (h *ModelHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := PathID(r.Context())
	if err := h.svc.Delete(r.Context(), id); err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, model.MessageResponse{Message: "deleted"})
}

// List 分页查询型号。
func (h *ModelHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pn, ps := PageParam(q)
	enabled := QueryBoolPtr(q, "enabled")
	req := &model.ListModelRequest{
		Keyword:  QueryString(q, "keyword", ""),
		Arch:     QueryString(q, "arch", ""),
		Vendor:   QueryString(q, "vendor", ""),
		Enabled:  enabled,
		PageNum:  pn,
		PageSize: ps,
	}
	list, total, err := h.svc.List(r.Context(), req)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.Page(w, list, pn, ps, total)
}
