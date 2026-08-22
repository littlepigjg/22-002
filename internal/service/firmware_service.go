// Package service 固件管理业务服务。
package service

import (
	"context"
	"errors"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/idgen"
	"firmware-upgrade/pkg/logger"
	"firmware-upgrade/pkg/strutil"
	"firmware-upgrade/pkg/timeutil"
	"firmware-upgrade/pkg/validate"
)

// FirmwareService 固件服务。
type FirmwareService struct {
	store      store.FirmwareStore
	modelStore store.DeviceModelStore
	fileOp     *FileOpService
	cfg        *config.Config
}

// NewFirmwareService 创建固件服务。
func NewFirmwareService(s store.FirmwareStore, ms store.DeviceModelStore, fop *FileOpService, cfg *config.Config) *FirmwareService {
	if cfg == nil {
		cfg = config.Default()
	}
	return &FirmwareService{store: s, modelStore: ms, fileOp: fop, cfg: cfg}
}

// Create 新建固件记录。
func (s *FirmwareService) Create(ctx context.Context, req *model.CreateFirmwareRequest) (*model.Firmware, error) {
	if req == nil {
		return nil, model.ErrInvalidParam
	}
	if err := validate.Run(
		validate.Required("model_id", req.ModelID),
		validate.Required("version", req.Version),
		validate.Required("name", req.Name),
		validate.Required("md5", req.MD5),
		validate.Required("file_path", req.FilePath),
		validate.Required("file_name", req.FileName),
		validate.BetweenLen("version", req.Version, 2, 64),
		validate.BetweenLen("md5", req.MD5, 32, 32),
		validate.GreaterThan("size", req.Size, 0),
	); err != nil {
		return nil, err
	}
	if !strutil.IsSemVer(req.Version) {
		return nil, errors.New("version format invalid, expected semver like v1.2.3")
	}
	exist, err := s.modelStore.Exists(ctx, req.ModelID)
	if err != nil {
		return nil, err
	}
	if !exist {
		return nil, model.ErrModelNotFound
	}
	// 校验型号+版本唯一。
	if f, err := s.store.FindByModelAndVersion(ctx, req.ModelID, req.Version); err == nil && f != nil {
		return nil, errors.New("same version firmware already exists")
	} else if err != nil && !errors.Is(err, model.ErrFirmwareNotFound) {
		return nil, err
	}
	// 发布日期。
	releaseAt := timeutil.Now()
	if req.ReleaseDate != "" {
		if t, pe := timeutil.Parse(req.ReleaseDate); pe == nil {
			releaseAt = t
		} else if t, pe2 := timeutil.ParseDate(req.ReleaseDate); pe2 == nil {
			releaseAt = t
		}
	}
	now := timeutil.Now()
	f := &model.Firmware{
		ID:             idgen.NextID(),
		ModelID:        req.ModelID,
		Version:        req.Version,
		Name:           req.Name,
		Description:    req.Description,
		MD5:            req.MD5,
		Size:           req.Size,
		FilePath:       req.FilePath,
		FileName:       req.FileName,
		Status:         model.FirmwareDraft,
		ReleaseDate:    releaseAt,
		MinFromVersion: req.MinFromVersion,
		Signature:      req.Signature,
		CreatedBy:      req.CreatedBy,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := s.store.Create(ctx, f); err != nil {
		return nil, err
	}
	return s.store.Get(ctx, f.ID)
}

// Get 获取固件详情。
func (s *FirmwareService) Get(ctx context.Context, id string) (*model.Firmware, error) {
	if strutil.IsEmpty(id) {
		return nil, model.ErrInvalidParam
	}
	return s.store.Get(ctx, id)
}

// UpdateStatus 更新固件状态（发布/废弃/回退草稿）。
func (s *FirmwareService) UpdateStatus(ctx context.Context, id string, status model.FirmwareStatus) (*model.Firmware, error) {
	if strutil.IsEmpty(id) {
		return nil, model.ErrInvalidParam
	}
	if _, err := s.store.Get(ctx, id); err != nil {
		return nil, err
	}
	switch status {
	case model.FirmwareDraft, model.FirmwarePublished, model.FirmwareDeprecated:
	default:
		return nil, errors.New("invalid firmware status")
	}
	if err := s.store.SetStatus(ctx, id, status); err != nil {
		return nil, err
	}
	return s.store.Get(ctx, id)
}

// Delete 删除固件（同步删除磁盘文件）。
func (s *FirmwareService) Delete(ctx context.Context, id string) error {
	if strutil.IsEmpty(id) {
		return model.ErrInvalidParam
	}
	f, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	if f.FilePath != "" {
		if err2 := s.fileOp.Delete(f.FilePath); err2 != nil {
			logger.Warn("delete firmware file failed", "path", f.FilePath, "err", err2)
		}
	}
	return nil
}

// List 分页查询固件。
func (s *FirmwareService) List(ctx context.Context, req *model.ListFirmwareRequest) ([]*model.Firmware, int64, error) {
	if req == nil {
		req = &model.ListFirmwareRequest{}
	}
	return s.store.List(ctx, req.ModelID, req.Version, req.Keyword, req.Status, req.SortBy, req.SortOrder, req.PageNum, req.PageSize)
}

// FindByModelAndVersion 根据型号+版本查找。
func (s *FirmwareService) FindByModelAndVersion(ctx context.Context, modelID, version string) (*model.Firmware, error) {
	return s.store.FindByModelAndVersion(ctx, modelID, version)
}

// ListByModel 按型号列举固件。
func (s *FirmwareService) ListByModel(ctx context.Context, modelID string, status model.FirmwareStatus) ([]*model.Firmware, error) {
	if strutil.IsEmpty(modelID) {
		return nil, model.ErrInvalidParam
	}
	return s.store.ListByModel(ctx, modelID, status)
}

// 防止 time 未使用。
var _ = time.Second
