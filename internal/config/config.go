// Package config 加载并管理本项目运行配置。
// 优先读取环境变量，缺失时使用默认值。
package config

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config 全量运行配置。
type Config struct {
	// App 基础配置。
	AppName    string
	Version    string
	ListenAddr string
	Port       int
	LogLevel   string
	Timezone   string

	// 存储与上传。
	DataDir        string // 数据根目录（内存存储的持久化目录 / 固件上传目录）
	FirmwareDir    string // 固件上传目录（绝对/相对 DataDir）
	EnablePersist  bool   // 是否启用 JSON 持久化
	PersistInterval int   // 持久化间隔秒

	// 业务参数。
	FirmwareMaxSize int64 // 固件最大上传大小（字节）
	HeartbeatTTL    int   // 心跳 TTL 秒
	DefaultTimeout  int   // 默认升级超时秒
	DefaultMaxRetry int   // 默认最大重试次数

	// HTTP。
	ReadTimeoutMs  int
	WriteTimeoutMs int
	IdleTimeoutMs  int
	ShutdownWaitMs int // 优雅关闭等待毫秒

	// Worker。
	BackgroundWorkerCount int // 后台 worker 数量（用于超时扫描等）
}

var (
	cached     *Config
	cachedOnce sync.Once
	cachedMu   sync.RWMutex
)

// Default 返回默认配置。
func Default() *Config {
	return &Config{
		AppName:               "firmware-upgrade",
		Version:               "1.0.0",
		ListenAddr:            "0.0.0.0",
		Port:                  8080,
		LogLevel:              "info",
		Timezone:              "Asia/Shanghai",
		DataDir:               "./data",
		FirmwareDir:           "./data/firmwares",
		EnablePersist:         true,
		PersistInterval:       60,
		FirmwareMaxSize:       256 * 1024 * 1024,
		HeartbeatTTL:          120,
		DefaultTimeout:        30 * 60,
		DefaultMaxRetry:       3,
		ReadTimeoutMs:         30_000,
		WriteTimeoutMs:        60_000,
		IdleTimeoutMs:         120_000,
		ShutdownWaitMs:        5_000,
		BackgroundWorkerCount: 4,
	}
}

// Load 读取环境变量并合并默认配置。
func Load() *Config {
	cachedOnce.Do(func() {
		cached = applyEnv(Default())
	})
	cachedMu.RLock()
	defer cachedMu.RUnlock()
	c := *cached
	return &c
}

// Reload 强制重新加载（测试用）。
func Reload() *Config {
	cachedMu.Lock()
	cachedOnce = sync.Once{}
	cachedMu.Unlock()
	return Load()
}

func applyEnv(c *Config) *Config {
	if v := os.Getenv("APP_NAME"); v != "" {
		c.AppName = v
	}
	if v := os.Getenv("APP_VERSION"); v != "" {
		c.Version = v
	}
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		c.ListenAddr = v
	}
	if v := os.Getenv("PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Port = n
		}
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		c.LogLevel = strings.ToLower(v)
	}
	if v := os.Getenv("TZ"); v != "" {
		c.Timezone = v
	}
	if v := os.Getenv("DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv("FIRMWARE_DIR"); v != "" {
		c.FirmwareDir = v
	}
	if v := os.Getenv("ENABLE_PERSIST"); v != "" {
		c.EnablePersist = parseBool(v, c.EnablePersist)
	}
	if v := os.Getenv("PERSIST_INTERVAL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.PersistInterval = n
		}
	}
	if v := os.Getenv("FIRMWARE_MAX_SIZE"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			c.FirmwareMaxSize = n
		}
	}
	if v := os.Getenv("HEARTBEAT_TTL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.HeartbeatTTL = n
		}
	}
	if v := os.Getenv("DEFAULT_TIMEOUT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.DefaultTimeout = n
		}
	}
	if v := os.Getenv("DEFAULT_MAX_RETRY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.DefaultMaxRetry = n
		}
	}
	if v := os.Getenv("READ_TIMEOUT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.ReadTimeoutMs = n
		}
	}
	if v := os.Getenv("WRITE_TIMEOUT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.WriteTimeoutMs = n
		}
	}
	if v := os.Getenv("IDLE_TIMEOUT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.IdleTimeoutMs = n
		}
	}
	if v := os.Getenv("SHUTDOWN_WAIT_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			c.ShutdownWaitMs = n
		}
	}
	if v := os.Getenv("BG_WORKER_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.BackgroundWorkerCount = n
		}
	}
	return c
}

// parseBool 解析布尔环境变量。
func parseBool(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "y", "t":
		return true
	case "0", "false", "no", "off", "n", "f":
		return false
	}
	return def
}

// Addr 返回监听地址。
func (c *Config) Addr() string {
	return c.ListenAddr + ":" + strconv.Itoa(c.Port)
}

// ReadTimeout 返回读超时。
func (c *Config) ReadTimeout() time.Duration {
	return time.Duration(c.ReadTimeoutMs) * time.Millisecond
}

// WriteTimeout 返回写超时。
func (c *Config) WriteTimeout() time.Duration {
	return time.Duration(c.WriteTimeoutMs) * time.Millisecond
}

// IdleTimeout 返回空闲超时。
func (c *Config) IdleTimeout() time.Duration {
	return time.Duration(c.IdleTimeoutMs) * time.Millisecond
}

// ShutdownWait 返回优雅关闭等待时间。
func (c *Config) ShutdownWait() time.Duration {
	return time.Duration(c.ShutdownWaitMs) * time.Millisecond
}

// PersistEvery 返回持久化间隔。
func (c *Config) PersistEvery() time.Duration {
	return time.Duration(c.PersistInterval) * time.Second
}
