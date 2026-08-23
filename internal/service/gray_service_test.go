package service

import (
	"context"
	"testing"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/internal/store"
)

// newTestGray 构造一个使用内存设备存储的灰度服务，并预置设备。
func newTestGray(t *testing.T, devices ...*model.Device) *GrayService {
	t.Helper()
	ds := store.NewDeviceStore()
	for _, d := range devices {
		if err := ds.Create(context.Background(), d); err != nil {
			t.Fatalf("create device %s: %v", d.ID, err)
		}
	}
	return NewGrayService(ds, config.Default())
}

func mkDevice(id, group, modelID, version string) *model.Device {
	return &model.Device{ID: id, Group: group, ModelID: modelID, CurrentVersion: version}
}

// TestSelectDevices_GroupFilter 分组过滤：alpha 命中、beta 进 miss，且无切片别名污染。
func TestSelectDevices_GroupFilter(t *testing.T) {
	task := &model.UpgradeTask{
		ID:          "task-alpha",
		ModelID:     "M1",
		GroupFilter: []string{"alpha"},
		Strategy:    model.StrategyFull,
	}
	// 多放几台 beta，让别名覆盖更容易暴露（旧实现会把元素互相覆盖）。
	devs := []*model.Device{
		mkDevice("a1", "alpha", "M1", "1.0"),
		mkDevice("a2", "alpha", "M1", "1.0"),
		mkDevice("b1", "beta", "M1", "1.0"),
		mkDevice("b2", "beta", "M1", "1.0"),
		mkDevice("b3", "beta", "M1", "1.0"),
	}
	g := newTestGray(t, devs...)

	hit, miss, err := g.SelectDevices(context.Background(), task)
	if err != nil {
		t.Fatalf("SelectDevices: %v", err)
	}
	if len(hit) != 2 {
		t.Fatalf("hit count = %d, want 2 (alpha a1,a2); hit=%v", len(hit), hit)
	}
	if len(miss) != 3 {
		t.Fatalf("miss count = %d, want 3 (beta b1,b2,b3); miss=%v", len(miss), miss)
	}
	for _, d := range hit {
		if d.Group != "alpha" {
			t.Fatalf("hit contains non-alpha device %s (group=%s)", d.ID, d.Group)
		}
	}
	missIDs := map[string]bool{}
	for _, d := range miss {
		if d.Group != "beta" {
			t.Fatalf("miss contains non-beta device %s (group=%s)", d.ID, d.Group)
		}
		if missIDs[d.ID] {
			t.Fatalf("miss contains duplicate device %s (slice aliasing bug)", d.ID)
		}
		missIDs[d.ID] = true
	}
}

// TestSelectDevices_Stable 内存存储以 map 遍历，顺序随机；同一任务多次查询结果必须一致。
func TestSelectDevices_Stable(t *testing.T) {
	task := &model.UpgradeTask{
		ID:          "task-stable",
		ModelID:     "M1",
		GroupFilter: []string{"alpha"},
		Strategy:    model.StrategyFull,
	}
	devs := []*model.Device{
		mkDevice("a1", "alpha", "M1", "1.0"),
		mkDevice("a2", "alpha", "M1", "1.0"),
		mkDevice("a3", "alpha", "M1", "1.0"),
		mkDevice("a4", "alpha", "M1", "1.0"),
		mkDevice("b1", "beta", "M1", "1.0"),
		mkDevice("b2", "beta", "M1", "1.0"),
	}
	g := newTestGray(t, devs...)

	var firstHit, firstMiss []string
	for i := 0; i < 20; i++ {
		hit, miss, err := g.SelectDevices(context.Background(), task)
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if len(hit) != 4 {
			t.Fatalf("iter %d: hit = %d, want 4", i, len(hit))
		}
		if len(miss) != 2 {
			t.Fatalf("iter %d: miss = %d, want 2", i, len(miss))
		}
		// 命中按 ID 升序，确认稳定。
		for j := 1; j < len(hit); j++ {
			if hit[j-1].ID >= hit[j].ID {
				t.Fatalf("iter %d: hit not sorted: %s >= %s", i, hit[j-1].ID, hit[j].ID)
			}
		}
		if i == 0 {
			firstHit = ids(hit)
			firstMiss = ids(miss)
			continue
		}
		if !equal(firstHit, ids(hit)) {
			t.Fatalf("iter %d: hit set changed across calls: %v vs %v", i, firstHit, ids(hit))
		}
		if !equal(firstMiss, ids(miss)) {
			t.Fatalf("iter %d: miss set changed across calls: %v vs %v", i, firstMiss, ids(miss))
		}
	}
}

// TestSelectDevices_FromVersion 来源版本过滤：匹配的命中，不匹配的进 miss。
func TestSelectDevices_FromVersion(t *testing.T) {
	task := &model.UpgradeTask{
		ID:          "task-ver",
		ModelID:     "M1",
		FromVersion:  "1.0",
		Strategy:    model.StrategyFull,
	}
	devs := []*model.Device{
		mkDevice("v1", "alpha", "M1", "1.0"),
		mkDevice("v2", "alpha", "M1", "2.0"),
		mkDevice("v3", "alpha", "M1", "1.0"),
	}
	g := newTestGray(t, devs...)

	hit, miss, err := g.SelectDevices(context.Background(), task)
	if err != nil {
		t.Fatalf("SelectDevices: %v", err)
	}
	if len(hit) != 2 {
		t.Fatalf("hit = %d, want 2 (v1,v3 on 1.0); hit=%v", len(hit), hit)
	}
	if len(miss) != 1 || miss[0].ID != "v2" {
		t.Fatalf("miss = %v, want [v2]", miss)
	}
}

func ids(ds []*model.Device) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
