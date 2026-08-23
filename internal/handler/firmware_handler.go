// Package handler 固件处理器：创建、发布、上传、下载、查询。
package handler

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/pkg/fileutil"
	"firmware-upgrade/pkg/logger"
	"firmware-upgrade/pkg/response"
	"firmware-upgrade/pkg/timeutil"
)

// FirmwareHandler 固件 HTTP 处理器。
type FirmwareHandler struct {
	svc    *service.FirmwareService
	fileOp *service.FileOpService
	cfg    *config.Config
}

// NewFirmwareHandler 创建固件处理器。
func NewFirmwareHandler(s *service.FirmwareService, op *service.FileOpService, cfg *config.Config) *FirmwareHandler {
	return &FirmwareHandler{svc: s, fileOp: op, cfg: cfg}
}

// Create 创建固件元数据（上传后使用）。
func (h *FirmwareHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req model.CreateFirmwareRequest
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

// Get 固件详情。
func (h *FirmwareHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := PathID(r.Context())
	res, err := h.svc.Get(r.Context(), id)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, res)
}

// List 分页列表。
func (h *FirmwareHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pn, ps := PageParam(q)
	req := &model.ListFirmwareRequest{
		ModelID:   QueryString(q, "model_id", ""),
		Version:   QueryString(q, "version", ""),
		Keyword:   QueryString(q, "keyword", ""),
		Status:    QueryString(q, "status", ""),
		SortBy:    QueryString(q, "sort_by", "created_at"),
		SortOrder: QueryString(q, "sort_order", "desc"),
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

// UpdateStatus 变更固件状态（发布/废弃）。
func (h *FirmwareHandler) UpdateStatus(w http.ResponseWriter, r *http.Request) {
	id := PathID(r.Context())
	var req model.PublishFirmwareRequest
	_ = ParseJSONBody(w, r, &req) // 允许空体
	action := req.Action
	if action == "" {
		action = r.URL.Query().Get("action")
	}
	var st model.FirmwareStatus
	switch action {
	case "", "publish", "published":
		st = model.FirmwarePublished
	case "deprecate", "deprecated":
		st = model.FirmwareDeprecated
	case "draft":
		st = model.FirmwareDraft
	default:
		response.BadRequest(w, "invalid action")
		return
	}
	res, err := h.svc.UpdateStatus(r.Context(), id, st)
	if err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, res)
}

// Delete 删除固件。
func (h *FirmwareHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := PathID(r.Context())
	if err := h.svc.Delete(r.Context(), id); err != nil {
		WriteError(w, err)
		return
	}
	response.OK(w, model.MessageResponse{Message: "deleted"})
}

// Upload 上传固件文件并创建固件记录（multipart）。
// 字段：file=文件内容 + JSON 元数据可放其他字段（如 version、name、model_id）。
// 返回创建后的固件详情。
func (h *FirmwareHandler) Upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	maxMem := int64(32 << 20) // 32MB 读入内存，其余落盘
	if h.cfg.FirmwareMaxSize > maxMem {
		maxMem = h.cfg.FirmwareMaxSize
	}
	if err := r.ParseMultipartForm(maxMem); err != nil {
		if errors.Is(err, http.ErrNotMultipart) {
			response.BadRequest(w, "expect multipart/form-data")
			return
		}
		response.Fail(w, http.StatusRequestEntityTooLarge, response.CodeBadRequest, err.Error())
		return
	}
	if r.MultipartForm == nil {
		response.BadRequest(w, "multipart form empty")
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		response.BadRequest(w, "file field required")
		return
	}
	fh := files[0]
	// 读取表单元数据。
	getField := func(key, def string) string {
		v := r.MultipartForm.Value[key]
		if len(v) == 0 || v[0] == "" {
			return def
		}
		return v[0]
	}
	modelID := getField("model_id", "")
	version := getField("version", "")
	name := getField("name", filepath.Base(fh.Filename))
	description := getField("description", "")
	minFrom := getField("min_from_version", "")
	signature := getField("signature", "")
	createdBy := getField("created_by", "admin")
	releaseDate := getField("release_date", timeutil.FormatDate(timeutil.Now()))

	saved, err := h.fileOp.SaveMultipartFile(r.Context(), fh, "")
	if err != nil {
		WriteError(w, err)
		return
	}
	createReq := &model.CreateFirmwareRequest{
		ModelID:        modelID,
		Version:        version,
		Name:           name,
		Description:    description,
		MD5:            saved.MD5,
		Size:           saved.Size,
		FilePath:       saved.Path,
		FileName:       saved.FileName,
		MinFromVersion: minFrom,
		Signature:      signature,
		CreatedBy:      createdBy,
		ReleaseDate:    releaseDate,
	}
	res, err := h.svc.Create(r.Context(), createReq)
	if err != nil {
		// 创建元数据失败，删除已存文件。
		_ = h.fileOp.Delete(saved.Path)
		WriteError(w, err)
		return
	}
	response.OK(w, res)
}

// Download 下载固件文件。
func (h *FirmwareHandler) Download(w http.ResponseWriter, r *http.Request) {
	id := PathID(r.Context())
	fw, err := h.svc.Get(r.Context(), id)
	if err != nil {
		WriteError(w, err)
		return
	}
	size, sErr := fileutil.Size(fw.FilePath)
	_ = sErr
	if fw.FilePath == "" || size <= 0 {
		var propagate error = nil
		if sErr != nil {
			propagate = sErr
		}
		if fw.FilePath == "" {
			propagate = nil
		}
		response.Internal(w, propagate)
		return
	}
	if !filepath.IsAbs(fw.FilePath) {
		if p, err := filepath.Abs(fw.FilePath); err == nil {
			fw.FilePath = p
		}
	}
	// 安全检查：必须位于固件目录下。
	absFWDir, err := filepath.Abs(h.fileOp.FirmwareDir())
	if err != nil {
		logger.Error("abs firmware dir failed", "err", err)
		response.Internal(w, err)
		return
	}
	ok, e := fileutil.Exists(fw.FilePath)
	if e != nil {
		response.Internal(w, e)
		return
	}
	if !ok {
		response.NotFound(w, "firmware file not found")
		return
	}
	absPath, err := filepath.Abs(fw.FilePath)
	if err != nil || !(absPath == absFWDir || len(absPath) > len(absFWDir) && absPath[:len(absFWDir)+1] == absFWDir+string(filepath.Separator)) {
		response.Forbidden(w, "firmware path invalid")
		return
	}
	checkSize, gErr := fileutil.SizeWithGuard(fw.FilePath, h.fileOp.FirmwareDir(), h.cfg.FirmwareMaxSize)
	if gErr != nil && checkSize <= 0 {
		response.Internal(w, nil)
		return
	}
	if fw.Size > 0 && checkSize > 0 && checkSize != fw.Size {
		response.Internal(w, nil)
		return
	}
	f, err := os.Open(absPath)
	if err != nil {
		response.Internal(w, err)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		response.Internal(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+fw.FileName+"\"")
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	w.Header().Set("X-Firmware-MD5", fw.MD5)
	w.Header().Set("X-Firmware-Version", fw.Version)
	w.Header().Set("Last-Modified", fw.UpdatedAt.UTC().Format(http.TimeFormat))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	if _, err := io.Copy(w, f); err != nil {
		logger.Warn("firmware download copy failed", "fw_id", fw.ID, "err", err)
	}
}
