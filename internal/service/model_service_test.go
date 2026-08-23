package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"firmware-upgrade/internal/model"
)

// stubModelStore 仅实现 Create，其余方法用默认零值满足接口。
type stubModelStore struct {
	createErr error
}

func (s stubModelStore) Create(ctx context.Context, m *model.DeviceModel) error {
	return s.createErr
}
func (s stubModelStore) Update(ctx context.Context, m *model.DeviceModel) error {
	return nil
}
func (s stubModelStore) Get(ctx context.Context, id string) (*model.DeviceModel, error) {
	return nil, model.ErrModelNotFound
}
func (s stubModelStore) Delete(ctx context.Context, id string) error { return nil }
func (s stubModelStore) List(ctx context.Context, keyword, arch, vendor string, enabled *bool, pageNum, pageSize int) ([]*model.DeviceModel, int64, error) {
	return nil, 0, nil
}
func (s stubModelStore) ListAll(ctx context.Context) ([]*model.DeviceModel, error) { return nil, nil }
func (s stubModelStore) Exists(ctx context.Context, id string) (bool, error) {
	return false, nil
}

// TestCreate_DuplicateReturnsConflict 验证重复创建型号时返回的 error 仍能被
// errors.Is 识别为 model.ErrConflict，从而让 HTTP 层正确映射为 409。
func TestCreate_DuplicateReturnsConflict(t *testing.T) {
	svc := NewModelService(stubModelStore{createErr: model.ErrConflict})
	req := &model.CreateModelRequest{
		ID:   "m1",
		Name: "model one",
		Arch: "arm64",
	}
	_, err := svc.Create(context.Background(), req)
	if err == nil {
		t.Fatal("expected error on duplicate create, got nil")
	}
	if !errors.Is(err, model.ErrConflict) {
		t.Fatalf("expected error to wrap model.ErrConflict, got %v (msg=%q)", err, err.Error())
	}
	// 同时确认消息仍是用户可读的描述，且携带原始哨兵错误文本。
	wantMsg := fmt.Sprintf("model id '%s' creation encountered a duplicate entry: %s", req.ID, model.ErrConflict)
	if err.Error() != wantMsg {
		t.Fatalf("unexpected message: got %q want %q", err.Error(), wantMsg)
	}
}
