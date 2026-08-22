// Package handler 升级任务处理器。
package handler

import (
	"net/http"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/pkg/response"
)

// TaskHandler 任务 HTTP 处理器。
type TaskHandler struct {
	svc *service.TaskService
}

// NewTaskHandler 创建任务处理器。
func NewTaskHandler(s *service.TaskService) *TaskHandler {
	return &TaskHandler{svc: s}
}

// Create 创建任务。
func (h *TaskHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req model.CreateTaskRequest
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

// List 任务列表。
func (h *TaskHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pn, ps := PageParam(q)
	req := &model.ListTaskRequest{
		Keyword:       QueryString(q, "keyword", ""),
		ModelID:       QueryString(q, "model_id", ""),
		Status:        QueryString(q, "status", ""),
		FirmwareID:    QueryString(q, "firmware_id", ""),
		TargetVersion: QueryString(q, "target_version", ""),
		SortBy:        QueryString(q, "sort_by", "created_at"),
		SortOrder:     QueryString(q, "sort_order", "desc"),
		PageNum:       pn,
		PageSize:      ps,
	}
	list, total, err := h.svc.List(r.Context(), req)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.Page(w, list, pn, ps, total)
}

// Detail 任务详情（含执行列表）。
func (h *TaskHandler) Detail(w http.ResponseWriter, r *http.Request) {
	id := PathID(r.Context())
	res, err := h.svc.Detail(r.Context(), id)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, res)
}

// Action 状态动作：pause/resume/cancel/finish。
func (h *TaskHandler) Action(w http.ResponseWriter, r *http.Request) {
	id := PathID(r.Context())
	var req model.UpdateTaskRequest
	_ = ParseJSONBody(w, r, &req)
	action := req.Action
	if action == "" {
		action = r.URL.Query().Get("action")
	}
	reason := req.Reason
	res, err := h.svc.UpdateStatus(r.Context(), id, action, reason)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, res)
}

// Delete 删除任务。
func (h *TaskHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := PathID(r.Context())
	if err := h.svc.Delete(r.Context(), id); err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, model.MessageResponse{Message: "deleted"})
}
