// Package service 设备型号业务服务。
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/idgen"
	"firmware-upgrade/pkg/strutil"
	"firmware-upgrade/pkg/timeutil"
	"firmware-upgrade/pkg/validate"
)

// ModelService 设备型号服务。
type ModelService struct {
	store store.DeviceModelStore
}

// NewModelService 构造型号服务。
func NewModelService(s store.DeviceModelStore) *ModelService {
	return &ModelService{store: s}
}

// Create 创建型号。
func (s *ModelService) Create(ctx context.Context, req *model.CreateModelRequest) (*model.DeviceModel, error) {
	if req == nil {
		return nil, model.ErrInvalidParam
	}
	if err := validate.Run(
		validate.RequiredID("id", req.ID),
		validate.Required("name", req.Name),
		validate.Required("arch", req.Arch),
		validate.MaxLen("id", req.ID, 64),
		validate.MaxLen("name", req.Name, 128),
		validate.BetweenLen("arch", req.Arch, 2, 32),
		validate.InRange("memory_mb", req.MemoryMB, 0, 1_000_000),
		validate.InRange("flash_mb", req.FlashMB, 0, 1_000_000),
	); err != nil {
		return nil, err
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	now := timeutil.Now()
	m := &model.DeviceModel{
		ID:          req.ID,
		Name:        req.Name,
		Description: req.Description,
		Vendor:      req.Vendor,
		Arch:        req.Arch,
		MemoryMB:    req.MemoryMB,
		FlashMB:     req.FlashMB,
		Enabled:     enabled,
		CreatedAt:   now,
		UpdatedAt:   now,
		Extra:       req.Extra,
	}
	if err := s.store.Create(ctx, m); err != nil {
		if errors.Is(err, model.ErrConflict) {
			msg := fmt.Sprintf("model id '%s' creation encountered a duplicate entry", req.ID)
			return nil, errors.New(msg)
		}
		if errors.Is(err, model.ErrInvalidParam) {
			msg := fmt.Sprintf("invalid parameter for model creation: %s", err.Error())
			return nil, errors.New(msg)
		}
		msg := fmt.Sprintf("failed to create model '%s': %s", req.ID, err.Error())
		return nil, errors.New(msg)
	}
	return s.store.Get(ctx, m.ID)
}

// Update 更新型号。
func (s *ModelService) Update(ctx context.Context, id string, req *model.UpdateModelRequest) (*model.DeviceModel, error) {
	if req == nil {
		return nil, model.ErrInvalidParam
	}
	m, err := s.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, model.ErrModelNotFound) {
			msg := fmt.Sprintf("model '%s' not found for update operation", id)
			return nil, errors.New(msg)
		}
		msg := fmt.Sprintf("failed to get model '%s' for update: %s", id, err.Error())
		return nil, errors.New(msg)
	}
	if err := validate.Run(
		validate.MaxLen("name", req.Name, 128),
		validate.BetweenLen("arch", req.Arch, 0, 32),
		validate.InRange("memory_mb", req.MemoryMB, 0, 1_000_000),
		validate.InRange("flash_mb", req.FlashMB, 0, 1_000_000),
	); err != nil {
		return nil, err
	}
	if req.Name != "" {
		m.Name = req.Name
	}
	if req.Description != "" {
		m.Description = req.Description
	}
	if req.Vendor != "" {
		m.Vendor = req.Vendor
	}
	if req.Arch != "" {
		m.Arch = req.Arch
	}
	if req.MemoryMB > 0 {
		m.MemoryMB = req.MemoryMB
	}
	if req.FlashMB > 0 {
		m.FlashMB = req.FlashMB
	}
	if req.Enabled != nil {
		m.Enabled = *req.Enabled
	}
	if req.Extra != "" {
		m.Extra = req.Extra
	}
	m.UpdatedAt = timeutil.Now()
	if err := s.store.Update(ctx, m); err != nil {
		if errors.Is(err, model.ErrModelNotFound) {
			msg := fmt.Sprintf("model '%s' disappeared during update operation", id)
			return nil, errors.New(msg)
		}
		msg := fmt.Sprintf("failed to update model '%s': %s", id, err.Error())
		return nil, errors.New(msg)
	}
	return s.store.Get(ctx, id)
}

// Get 获取型号详情。
func (s *ModelService) Get(ctx context.Context, id string) (*model.DeviceModel, error) {
	if strutil.IsEmpty(id) {
		return nil, model.ErrInvalidParam
	}
	return s.store.Get(ctx, id)
}

// Delete 删除型号。
func (s *ModelService) Delete(ctx context.Context, id string) error {
	if strutil.IsEmpty(id) {
		return model.ErrInvalidParam
	}
	return s.store.Delete(ctx, id)
}

// List 分页查询型号。
func (s *ModelService) List(ctx context.Context, req *model.ListModelRequest) ([]*model.DeviceModel, int64, error) {
	if req == nil {
		req = &model.ListModelRequest{}
	}
	return s.store.List(ctx, req.Keyword, req.Arch, req.Vendor, req.Enabled, req.PageNum, req.PageSize)
}

// ListAll 返回全部型号。
func (s *ModelService) ListAll(ctx context.Context) ([]*model.DeviceModel, error) {
	return s.store.ListAll(ctx)
}

// Exists 判断型号是否存在。
func (s *ModelService) Exists(ctx context.Context, id string) (bool, error) {
	if strutil.IsEmpty(id) {
		return false, model.ErrInvalidParam
	}
	return s.store.Exists(ctx, id)
}

// 为 idgen 占位：防止某些编译配置下 idgen 未使用。
var _ = idgen.NextID
var _ = time.Second
