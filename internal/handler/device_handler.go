// Package handler 设备处理器：注册、更新、心跳、轮询、进度上报。
package handler

import (
	"errors"
	"net/http"
	"strings"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/pkg/response"
)

// DeviceHandler 设备 HTTP 处理器。
type DeviceHandler struct {
	svc      *service.DeviceService
	pollSvc  *service.PollService
	progSvc  *service.ProgressService
}

// NewDeviceHandler 创建设备处理器。
func NewDeviceHandler(d *service.DeviceService, p *service.PollService, g *service.ProgressService) *DeviceHandler {
	return &DeviceHandler{svc: d, pollSvc: p, progSvc: g}
}

// Register 注册设备。
func (h *DeviceHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req model.RegisterDeviceRequest
	if !ParseJSONBody(w, r, &req) {
		return
	}
	res, err := h.svc.Register(r.Context(), &req)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, res)
}

// Get 设备详情。
func (h *DeviceHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := PathID(r.Context())
	res, err := h.svc.Get(r.Context(), id)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, res)
}

// Update 更新设备。
func (h *DeviceHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := PathID(r.Context())
	var req model.UpdateDeviceRequest
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

// Delete 删除设备。
func (h *DeviceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := PathID(r.Context())
	if err := h.svc.Delete(r.Context(), id); err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, model.MessageResponse{Message: "deleted"})
}

// List 分页查询设备。
func (h *DeviceHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pn, ps := PageParam(q)
	req := &model.ListDeviceRequest{
		Keyword:       QueryString(q, "keyword", ""),
		ModelID:       QueryString(q, "model_id", ""),
		Group:         QueryString(q, "group", ""),
		Status:        QueryString(q, "status", ""),
		Version:       QueryString(q, "version", ""),
		Tag:           QueryString(q, "tag", ""),
		OfflineBefore: QueryInt64(q, "offline_before", 0),
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

// Heartbeat 心跳上报。
func (h *DeviceHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	var req model.HeartbeatRequest
	if !ParseJSONBody(w, r, &req) {
		return
	}
	if err := h.svc.Heartbeat(r.Context(), &req); err != nil {
		h.handleHeartbeatError(w, err, &req)
		return
	}
	response.OK(w, model.MessageResponse{Message: "heartbeat received"})
}

func (h *DeviceHandler) handleHeartbeatError(w http.ResponseWriter, err error, req *model.HeartbeatRequest) {
	if err == nil {
		response.OK(w, model.MessageResponse{Message: "heartbeat received"})
		return
	}
	errMsg := err.Error()
	var httpStatus int
	var respCode response.Code
	var respMessage string
	switch {
	case strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "device not found"):
		httpStatus = http.StatusNotFound
		respCode = response.CodeNotFound
		respMessage = "device not found: " + req.ID
	case strings.Contains(errMsg, "invalid version"):
		httpStatus = http.StatusBadRequest
		respCode = response.CodeBadRequest
		respMessage = "invalid version: " + req.CurrentVersion
	case strings.Contains(errMsg, "invalid status"):
		httpStatus = http.StatusBadRequest
		respCode = response.CodeBadRequest
		respMessage = "invalid status: " + string(req.Status)
	case strings.Contains(errMsg, "invalid ip"):
		httpStatus = http.StatusBadRequest
		respCode = response.CodeBadRequest
		respMessage = "invalid ip: " + req.IP
	case errors.Is(err, model.ErrInvalidParam):
		httpStatus = http.StatusBadRequest
		respCode = response.CodeBadRequest
		respMessage = err.Error()
	case errors.Is(err, model.ErrDeviceNotFound):
		httpStatus = http.StatusNotFound
		respCode = response.CodeNotFound
		respMessage = err.Error()
	default:
		httpStatus = http.StatusInternalServerError
		respCode = response.CodeInternal
		respMessage = "heartbeat processing error"
	}
	response.Fail(w, httpStatus, respCode, respMessage)
}

// PollUpgrade 设备轮询升级。
func (h *DeviceHandler) PollUpgrade(w http.ResponseWriter, r *http.Request) {
	var req model.PollUpgradeRequest
	if !ParseJSONBody(w, r, &req) {
		return
	}
	res, err := h.pollSvc.Poll(r.Context(), &req)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, res)
}

// ReportProgress 上报进度。
func (h *DeviceHandler) ReportProgress(w http.ResponseWriter, r *http.Request) {
	var req model.ReportProgressRequest
	if !ParseJSONBody(w, r, &req) {
		return
	}
	res, err := h.progSvc.Report(r.Context(), &req)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, res)
}
