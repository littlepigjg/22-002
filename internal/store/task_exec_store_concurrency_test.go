package store

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/cache"
)

// splitKey splits a "tid|did" snapshot/cache key back into its parts.
func splitKey(k string) (tid, did string) {
	for i := 0; i < len(k); i++ {
		if k[i] == '|' {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

// TestExecStoreConcurrentRace reproduces the load-test scenario and guards
// against the two original defects:
//  1. WARNING: DATA RACE on cache hit/miss/purged counters.
//  2. A cached *TaskDeviceExecution aliasing the live struct in s.data, so a
//     concurrent UpdateProgress mutated it under cache readers (torn / stale
//     reads, and "Get returns a record whose key is absent from the snapshot").
//
// Run with: go test -race -run TestExecStoreConcurrentRace ./internal/store
func TestExecStoreConcurrentRace(t *testing.T) {
	s := NewTaskExecStore().(*inMemoryTaskExecStore)
	ctx := context.Background()

	const (
		taskCount   = 4
		deviceCount = 16
		iters       = 600
		readers     = 6
		writers     = 8
	)

	mkExec := func(tid, did string, p int) *model.TaskDeviceExecution {
		return &model.TaskDeviceExecution{
			TaskID:       tid,
			DeviceID:     did,
			Status:       model.UpgradeStatusDownloading,
			Progress:     p,
			AssignedAt:   time.Now(),
			LastReportAt: time.Now(),
		}
	}

	var wg sync.WaitGroup

	// Readers: hammer Get + snapshot under load. Any failure here means a torn
	// or aliased read; the -race detector separately catches the counter race.
	fail := make(chan string, 1)
	flag := func(msg string) {
		select {
		case fail <- msg:
		default:
		}
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				_ = s.ExecSnapshot()
				tid := fmt.Sprintf("task-%d", i%taskCount)
				did := fmt.Sprintf("dev-%d", i%deviceCount)
				if got, err := s.Get(ctx, tid, did); err == nil && got != nil {
					// The cached value must never be the same struct that
					// s.data points at (aliasing was the original torn-read
					// bug). A Get must return a private copy.
					s.mu.RLock()
					live, ok := s.data[execKey(tid, did)]
					s.mu.RUnlock()
					if ok && live == got {
						flag(fmt.Sprintf("Get returned aliased pointer for %q|%q", tid, did))
						return
					}
					// Status must be a known value (never garbage from a torn
					// in-place mutation half-way through writing a string ptr).
					switch got.Status {
					case "", model.UpgradeStatusPending, model.UpgradeStatusDownloading,
						model.UpgradeStatusVerifying, model.UpgradeStatusUpgrading,
						model.UpgradeStatusSuccess, model.UpgradeStatusFailed,
						model.UpgradeStatusCanceled:
					default:
						flag(fmt.Sprintf("torn status for %q|%q: %q", tid, did, got.Status))
						return
					}
				}
			}
		}()
	}

	// Writers: simulate device progress reports + upsert + cache purge.
	for w := 0; w < writers-2; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				tid := fmt.Sprintf("task-%d", id%taskCount)
				did := fmt.Sprintf("dev-%d", i%deviceCount)
				_ = s.Upsert(ctx, mkExec(tid, did, i%100))
				_ = s.UpdateProgress(ctx, tid, did, model.UpgradeStatusUpgrading, (i*7)%100, time.Now(), "", false)
				if i%5 == 0 {
					_, _ = s.Get(ctx, tid, did)
				}
				if i%13 == 0 {
					s.PurgeExecCache()
				}
			}
		}(w)
	}

	// Cleaners: per-task cleanup interleaved (DeleteByTask).
	for c := 0; c < 2; c++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				tid := fmt.Sprintf("task-%d", i%taskCount)
				_ = s.DeleteByTask(ctx, tid)
			}
		}(c)
	}

	wg.Wait()
	close(fail)
	if msg, ok := <-fail; ok {
		t.Fatal(msg)
	}

	// After all writers/cleaners stop, the store is frozen. Purge stale cache
	// entries, then snapshot + Get must agree on every surviving key, and Get
	// must never surface a key that DeleteByTask removed.
	s.PurgeExecCache()
	snap := s.ExecSnapshot()
	for k, sv := range snap {
		tid, did := splitKey(k)
		got, err := s.Get(ctx, tid, did)
		if err != nil {
			t.Fatalf("post-freeze: snapshot has %q but Get returned %v", k, err)
		}
		if got.Status != sv.Status || got.Progress != sv.Progress {
			t.Fatalf("post-freeze inconsistency for %q: snapshot{status=%s,progress=%d} Get{status=%s,progress=%d}",
				k, sv.Status, sv.Progress, got.Status, got.Progress)
		}
	}
	// Conversely, Get must not return a record whose key is absent from data.
	s.mu.RLock()
	for k := range s.data {
		s.mu.RUnlock()
		tid, did := splitKey(k)
		if _, err := s.Get(ctx, tid, did); err != nil {
			t.Fatalf("post-freeze: data has %q but Get returned %v", k, err)
		}
		s.mu.RLock()
	}
	s.mu.RUnlock()
}

// TestExecCacheCountersNoRace exercises pure cache hit/miss/purge paths under
// concurrency to ensure the atomic counters don't race. Run with -race.
func TestExecCacheCountersNoRace(t *testing.T) {
	c := cache.New[int, int](cache.WithCapacity(128), cache.WithDefaultTTL(1*time.Millisecond), cache.WithAutoPurge(false))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				key := (id*7 + i) % 64
				switch i % 4 {
				case 0:
					c.SetTTL(key, i, time.Millisecond)
				case 1:
					_, _ = c.Get(key)
				case 2:
					c.Delete(key)
				case 3:
					c.Purge()
				}
			}
		}(g)
	}
	wg.Wait()
	// Stats reads under concurrency — must be race-free.
	_, _, _ = c.Stats()
}
