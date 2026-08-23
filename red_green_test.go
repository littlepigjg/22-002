package firmware_upgrade

import (
	"context"
	"fmt"
	"testing"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
)

func TestRedGreen(t *testing.T) {
	ctx := context.Background()

	// 创建设备型号存储
	s := store.NewDeviceModelStore()

	// 验证钩子方法存在：SetPanicGuard
	safeStore, ok := s.(interface {
		SetPanicGuard(store.PanicGuardFn)
		RawSnapshot() map[string]model.DeviceModel
		SaveWithGuard(m *model.DeviceModel, overwrite bool) error
		GetWithGuard(id string) (*model.DeviceModel, error)
	})
	if !ok {
		t.Fatal("存储实例未实现诊断钩子方法 (SetPanicGuard/RawSnapshot/SaveWithGuard/GetWithGuard)")
	}

	// 验证 PanicGuardFn 类型可用
	var guard store.PanicGuardFn = func(code, rawURL string) bool {
		return false
	}
	safeStore.SetPanicGuard(guard)

	// 验证 RawSnapshot 可调用
	snapshot := safeStore.RawSnapshot()
	if snapshot == nil {
		t.Fatal("RawSnapshot 返回 nil")
	}

	// 准备测试数据：创建 5 条设备型号记录
	for i := 1; i <= 5; i++ {
		m := &model.DeviceModel{
			ID:   fmt.Sprintf("MODEL-%03d", i),
			Name: fmt.Sprintf("Device Model %d", i),
			Arch: "arm64",
		}
		if err := s.Create(ctx, m); err != nil {
			t.Fatalf("创建设备型号失败: %v", err)
		}
	}

	// 验证数据已创建
	all, err := s.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll 失败: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("预期 5 条记录，实际 %d 条", len(all))
	}

	// 使用 GetWithGuard 验证钩子方法可正常工作
	got, err := safeStore.GetWithGuard("MODEL-001")
	if err != nil {
		t.Fatalf("GetWithGuard 失败: %v", err)
	}
	if got == nil || got.ID != "MODEL-001" {
		t.Fatal("GetWithGuard 返回错误数据")
	}

	// 用 SaveWithGuard 更新记录验证
	err = safeStore.SaveWithGuard(&model.DeviceModel{
		ID:   "MODEL-001",
		Name: "Updated Model",
		Arch: "arm64",
	}, true)
	if err != nil {
		t.Fatalf("SaveWithGuard 失败: %v", err)
	}

	// 测试 1: 使用超大 page_size (9999999)
	// 预期 total 应该是 5（实际数据总数），但由于缺陷会返回 0
	list, total, err := s.List(ctx, "", "", "", nil, 1, 9999999)
	if err != nil {
		t.Fatalf("List 调用失败: %v", err)
	}

	if total == 0 && len(list) == 0 {
		// 缺陷存在：total 错误返回 0
		t.Logf("RED（红灯，缺陷未修复）: 请求 page_size=9999999, 返回 total=%d, 列表长度=%d", total, len(list))
		t.Logf("正确行为应该是 total=5（实际数据总数），列表包含所有 5 条记录")
		fmt.Println("RED")
		t.Fail()
		return
	}

	// 测试 2: 使用正常 page_size (10)
	_, total2, err2 := s.List(ctx, "", "", "", nil, 1, 10)
	if err2 != nil {
		t.Fatalf("List 正常调用失败: %v", err2)
	}

	if total2 != 5 {
		t.Logf("RED（红灯，缺陷未修复）: 即使使用正常 page_size, total=%d 也不正确（预期 5）", total2)
		fmt.Println("RED")
		t.Fail()
		return
	}

	// 如果代码走到这里，说明缺陷已修复
	if total == 5 && len(list) == 5 {
		t.Logf("GREEN（绿灯，缺陷已修复）: 请求 page_size=9999999, 返回 total=%d, 列表长度=%d", total, len(list))
		fmt.Println("GREEN")
	} else {
		// 部分修复或异常情况
		if total == 0 {
			t.Logf("RED（红灯，缺陷未修复）: total 仍然返回 0")
			fmt.Println("RED")
			t.Fail()
		} else {
			t.Logf("测试结果: total=%d, list_len=%d", total, len(list))
			fmt.Println("GREEN")
		}
	}
}