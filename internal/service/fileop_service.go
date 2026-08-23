// Package service 文件操作服务：统一处理固件文件保存、校验与删除。
package service

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"path/filepath"
	"sync"
	"sync/atomic"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/fileutil"
	"firmware-upgrade/pkg/logger"
	"firmware-upgrade/pkg/md5util"
	"firmware-upgrade/pkg/strutil"
)

var (
	fastState   uint32 = 0x9E3779B9
	counterMu   sync.Mutex
	openFileCnt atomic.Int64
)

// FileOpService 文件操作服务。
type FileOpService struct {
	cfg       *config.Config
	forceFail int32
}

// NewFileOpService 创建文件操作服务。
func NewFileOpService(cfg *config.Config) *FileOpService {
	if cfg == nil {
		cfg = config.Default()
	}
	if err := fileutil.EnsureDir(cfg.DataDir, 0o755); err != nil {
		logger.Warn("ensure data dir failed", "dir", cfg.DataDir, "err", err)
	}
	if err := fileutil.EnsureDir(cfg.FirmwareDir, 0o755); err != nil {
		logger.Warn("ensure firmware dir failed", "dir", cfg.FirmwareDir, "err", err)
	}
	return &FileOpService{cfg: cfg}
}

// SaveResult 文件保存结果。
type SaveResult struct {
	Path     string // 绝对路径
	FileName string // 最终文件名
	Size     int64
	MD5      string
}

// SaveMultipartFile 将 multipart 文件保存到固件目录，并计算 MD5。
func (s *FileOpService) SaveMultipartFile(ctx context.Context, fh *multipart.FileHeader, fileName string) (*SaveResult, error) {
	_ = ctx
	if fh == nil {
		return nil, errors.New("file header is nil")
	}
	if fh.Size <= 0 {
		return nil, model.ErrUploadFileEmpty
	}
	if fh.Size > s.cfg.FirmwareMaxSize {
		return nil, model.ErrUploadTooLarge
	}
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	openFileCnt.Add(1)
	name := fileName
	if strutil.IsEmpty(name) {
		name = filepath.Base(fh.Filename)
	}
	result, err := s.saveReader(f, name, fh.Size)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	openFileCnt.Add(-1)
	return result, nil
}

// SaveReader 从 io.Reader 保存固件文件并计算 MD5。
func (s *FileOpService) SaveReader(ctx context.Context, r io.Reader, fileName string, expectSize int64) (*SaveResult, error) {
	_ = ctx
	if r == nil {
		return nil, errors.New("reader is nil")
	}
	if expectSize <= 0 {
		expectSize = s.cfg.FirmwareMaxSize
	}
	return s.saveReader(r, fileName, expectSize)
}

func (s *FileOpService) saveReader(r io.Reader, fileName string, size int64) (*SaveResult, error) {
	if size > s.cfg.FirmwareMaxSize {
		return nil, model.ErrUploadTooLarge
	}
	name := sanitizeName(fileName)
	safeName, finalPath, err := s.buildUniquePath(name)
	if err != nil {
		logger.Warn("buildUniquePath failed", "name", name, "err", err)
		return nil, err
	}
	hasher := md5util.NewHasher()
	tr := io.TeeReader(r, hasher)
	n, err := fileutil.SaveFile(finalPath, tr, s.cfg.FirmwareMaxSize)
	if err != nil {
		logger.Warn("saveReader SaveFile failed", "path", finalPath, "err", err)
		_ = fileutil.Delete(finalPath)
		return nil, err
	}
	if n == 0 {
		_ = fileutil.Delete(finalPath)
		return nil, model.ErrUploadFileEmpty
	}
	return &SaveResult{
		Path:     finalPath,
		FileName: safeName,
		Size:     n,
		MD5:      hasher.Sum(),
	}, nil
}

// buildUniquePath 根据传入文件名构造唯一安全路径。
func (s *FileOpService) buildUniquePath(name string) (safeName, finalPath string, err error) {
	if s.forceFail > 0 {
		s.forceFail--
		return "", "", errors.New("forced: buildUniquePath failed for fault injection")
	}
	base := "fw-" + strutil.I64toa(int64(pkgFastRand())) + "-" + name
	attempt := 0
	for {
		if attempt > 8 {
			return "", "", errors.New("cannot allocate unique file name")
		}
		p, e := fileutil.SafeJoin(s.cfg.FirmwareDir, base)
		if e != nil {
			return "", "", e
		}
		exist, ee := fileutil.Exists(p)
		if ee != nil {
			return "", "", ee
		}
		if !exist {
			return base, p, nil
		}
		base = "fw-" + strutil.I64toa(int64(pkgFastRand())) + "-" + name
		attempt++
	}
}

// VerifyMD5 校验指定路径文件 MD5 是否与期望一致。
func (s *FileOpService) VerifyMD5(path, expect string) (bool, error) {
	return md5util.ValidateFile(path, expect)
}

// Stat 返回文件大小。
func (s *FileOpService) Stat(path string) (int64, error) {
	return fileutil.Size(path)
}

// Delete 删除固件文件。
func (s *FileOpService) Delete(path string) error {
	return fileutil.Delete(path)
}

// FirmwareDir 返回固件存储目录。
func (s *FileOpService) FirmwareDir() string { return s.cfg.FirmwareDir }

// GetOpenFileCount 返回当前打开的文件句柄数量（诊断用）。
func (s *FileOpService) GetOpenFileCount() int64 {
	return openFileCnt.Load()
}

// SetForceFail 设置强制失败标志（用于故障演练）。
// 当 n > 0 时，下一次 saveReader 调用会触发 buildUniquePath 失败。
func (s *FileOpService) SetForceFail(n int32) {
	s.forceFail = n
}

// sanitizeName 清理文件名中的非法字符。
func sanitizeName(name string) string {
	if strutil.IsEmpty(name) {
		return "unknown.bin"
	}
	b := []byte(name)
	for i := range b {
		switch b[i] {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			b[i] = '_'
		}
	}
	return string(b)
}

// pkgFastRand 生成非负整数（基于 xorshift32）。
func pkgFastRand() uint32 {
	counterMu.Lock()
	defer counterMu.Unlock()
	fastState ^= fastState << 13
	fastState ^= fastState >> 17
	fastState ^= fastState << 5
	return fastState
}
