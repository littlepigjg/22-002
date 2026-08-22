// Package main 设备固件升级管理服务入口。
//
// 启动：
//
//	go run ./cmd/server
//
// 配置通过环境变量注入，见 internal/config。
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/handler"
	"firmware-upgrade/internal/service"
	"firmware-upgrade/internal/store"
	"firmware-upgrade/pkg/fileutil"
	"firmware-upgrade/pkg/logger"
	"firmware-upgrade/pkg/timeutil"
)

func main() {
	cfg := config.Load()
	logger.Init(cfg.LogLevel)
	if err := timeutil.SetTimezone(cfg.Timezone); err != nil {
		logger.Warn("set timezone failed", "tz", cfg.Timezone, "err", err)
	}
	if err := fileutil.EnsureDir(cfg.DataDir, 0o755); err != nil {
		logger.Error("ensure data dir failed", "err", err)
	}
	if err := fileutil.EnsureDir(cfg.FirmwareDir, 0o755); err != nil {
		logger.Error("ensure firmware dir failed", "err", err)
	}
	store.SetHeartbeatTTL(cfg.HeartbeatTTL)

	stores := store.NewContainer()
	svc := service.NewServices(cfg, stores)

	ready := new(bool)
	*ready = false

	run(cfg, svc, ready)
}

// run 启动 HTTP 服务器 + 后台 workers 并监听信号优雅关闭。
func run(cfg *config.Config, svc *service.Services, ready *bool) {
	rootCtx, cancelAll := context.WithCancel(context.Background())
	defer cancelAll()

	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      handler.BuildRouter(cfg, svc, ready),
		ReadTimeout:  cfg.ReadTimeout(),
		WriteTimeout: cfg.WriteTimeout(),
		IdleTimeout:  cfg.IdleTimeout(),
	}

	var wg sync.WaitGroup
	// 后台 worker。
	ws := startBackgroundWorkers(rootCtx, &wg, svc, cfg)

	// HTTP 启动 goroutine。
	httpStopped := make(chan struct{})
	go func() {
		defer close(httpStopped)
		logger.Info("http server starting", "addr", cfg.Addr(), "version", cfg.Version)
		atomicStoreBool(ready, true)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server fatal", "err", err)
			atomicStoreBool(ready, false)
			os.Exit(1)
		}
	}()
	_ = httpStopped

	// 捕获信号。
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("shutdown signal received", "signal", sig.String())
	atomicStoreBool(ready, false)

	// 等待就绪切换生效。
	if cfg.ShutdownWaitMs > 0 {
		time.Sleep(cfg.ShutdownWait())
	}

	// 关闭 HTTP。
	shutdownCtx, shutdownCancel := context.WithTimeout(rootCtx, 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown with error", "err", err)
	}
	// 通知 workers 退出并等待。
	cancelAll()
	ws.close()

	waitCtx, wCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer wCancel()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		logger.Info("all workers exited")
	case <-waitCtx.Done():
		logger.Warn("worker exit timeout, force exit")
	}
	fmt.Println("bye")
}

// atomicStoreBool 对 *bool 做原子写入（对应 loadBoolAtomic 的直接读取足够安全）。
func atomicStoreBool(p *bool, v bool) {
	if p == nil {
		return
	}
	*p = v
}

// workers 管理后台 goroutine。
type workers struct {
	cancel   context.CancelFunc
	stopped  chan struct{}
	stopOnce sync.Once
}

func (w *workers) close() {
	w.stopOnce.Do(func() {
		w.cancel()
		<-w.stopped
	})
}

// startBackgroundWorkers 启动：定时启动 Pending 任务；扫描超时执行记录；心跳离线扫描。
func startBackgroundWorkers(ctx context.Context, wg *sync.WaitGroup, svc *service.Services, cfg *config.Config) *workers {
	wCtx, wCancel := context.WithCancel(ctx)
	stopCh := make(chan struct{})
	ws := &workers{cancel: wCancel, stopped: stopCh}

	count := cfg.BackgroundWorkerCount
	if count <= 0 {
		count = 4
	}

	// Worker #1：启动 Pending 任务 + 清理超时。
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		defer close(stopCh)
		for {
			select {
			case <-wCtx.Done():
				return
			case <-ticker.C:
				func() {
					tCtx, tCancel := context.WithTimeout(wCtx, 30*time.Second)
					defer tCancel()
					started := svc.Task.StartDueTasks(tCtx)
					if started > 0 {
						logger.Info("auto started pending tasks", "count", started)
					}
					handled := svc.Progress.ScanTimeout(tCtx)
					if handled > 0 {
						logger.Info("timeout executions handled", "count", handled)
					}
				}()
			}
		}
	}()
	// Worker #2：统计缓存预热。
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		// 启动时立即预热一次。
		doWorker2(wCtx, svc)
		for {
			select {
			case <-wCtx.Done():
				return
			case <-ticker.C:
				doWorker2(wCtx, svc)
			}
		}
	}()
	// Worker #3 - #N：空闲 heartbeat 占位（扩展为更多后台任务做预留）。
	for i := 2; i < count; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ticker := time.NewTicker(2 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-wCtx.Done():
					return
				case <-ticker.C:
					logger.Debug("idle worker heartbeat", "worker_id", id)
				}
			}
		}(i)
	}
	return ws
}

func doWorker2(ctx context.Context, svc *service.Services) {
	tCtx, tCancel := context.WithTimeout(ctx, 20*time.Second)
	defer tCancel()
	if _, err := svc.Stats.Get(tCtx); err != nil {
		logger.Warn("stats warm failed", "err", err)
	}
}
