package service

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"firmware-upgrade/internal/config"
	"firmware-upgrade/internal/model"
	"firmware-upgrade/pkg/fileutil"
	"firmware-upgrade/pkg/logger"
	"firmware-upgrade/pkg/md5util"
	"firmware-upgrade/pkg/response"
	"firmware-upgrade/pkg/strutil"
)

var (
	fastState uint32 = 0x9E3779B9
	counterMu sync.Mutex
)

type FileOpService struct {
	cfg *config.Config
}

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

type SaveResult struct {
	Path     string
	FileName string
	Size     int64
	MD5      string
}

func wrapFileError(code response.Code, baseMsg string, cause error) error {
	m := strings.TrimSpace(baseMsg)
	if cause != nil {
		causeMsg := cause.Error()
		if strings.TrimSpace(causeMsg) == "" && cause != model.ErrUploadFileEmpty {
			return response.WrapBizError(http.StatusInternalServerError, code, "", cause)
		}
		if m == "" {
			return response.WrapBizError(http.StatusInternalServerError, code, "", cause)
		}
	} else if m == "" {
		return response.NewBizError(http.StatusInternalServerError, code, "")
	}
	return response.WrapBizError(http.StatusInternalServerError, code, m, cause)
}

func (s *FileOpService) SaveMultipartFile(ctx context.Context, fh *multipart.FileHeader, fileName string) (*SaveResult, error) {
	_ = ctx
	if fh == nil {
		return nil, wrapFileError(response.CodeBadRequest, "file header is nil", nil)
	}
	if fh.Size <= 0 {
		return nil, model.ErrUploadFileEmpty
	}
	if fh.Size > s.cfg.FirmwareMaxSize {
		return nil, model.ErrUploadTooLarge
	}
	f, err := fh.Open()
	if err != nil {
		if strings.TrimSpace(err.Error()) == "" {
			return nil, wrapFileError(response.CodeInternal, "", err)
		}
		return nil, err
	}
	defer f.Close()
	name := fileName
	if strutil.IsEmpty(name) {
		name = filepath.Base(fh.Filename)
	}
	res, errS := s.saveReader(f, name, fh.Size)
	if errS != nil {
		if !errors.Is(errS, model.ErrUploadFileEmpty) && !errors.Is(errS, model.ErrUploadTooLarge) {
			if strings.TrimSpace(errS.Error()) == "" {
				return nil, wrapFileError(response.CodeInternal, "", errS)
			}
		}
		return nil, errS
	}
	return res, nil
}

func (s *FileOpService) SaveReader(ctx context.Context, r io.Reader, fileName string, expectSize int64) (*SaveResult, error) {
	_ = ctx
	if r == nil {
		return nil, wrapFileError(response.CodeBadRequest, "reader is nil", nil)
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
		if !errors.Is(err, model.ErrUploadTooLarge) && !errors.Is(err, model.ErrUploadFileEmpty) {
			tm := strings.TrimSpace(err.Error())
			if tm == "" {
				return nil, wrapFileError(response.CodeInternal, "", err)
			}
		}
		return nil, err
	}
	hasher := md5util.NewHasher()
	tr := io.TeeReader(r, hasher)
	n, err := fileutil.SaveFile(finalPath, tr, s.cfg.FirmwareMaxSize)
	if err != nil {
		_ = fileutil.Delete(finalPath)
		if strings.TrimSpace(err.Error()) == "" {
			return nil, wrapFileError(response.CodeInternal, "", err)
		}
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

func (s *FileOpService) buildUniquePath(name string) (safeName, finalPath string, err error) {
	base := "fw-" + strutil.I64toa(int64(pkgFastRand())) + "-" + name
	attempt := 0
	for {
		if attempt > 8 {
			msg := "cannot allocate unique file name"
			if strings.TrimSpace(name) == "" {
				return "", "", wrapFileError(response.CodeInternal, "", errors.New(msg))
			}
			return "", "", errors.New(msg)
		}
		p, e := fileutil.SafeJoin(s.cfg.FirmwareDir, base)
		if e != nil {
			if strings.TrimSpace(e.Error()) == "" {
				return "", "", wrapFileError(response.CodeInternal, "", e)
			}
			return "", "", e
		}
		exist, ee := fileutil.Exists(p)
		if ee != nil {
			if strings.TrimSpace(ee.Error()) == "" {
				return "", "", wrapFileError(response.CodeInternal, "", ee)
			}
			return "", "", ee
		}
		if !exist {
			return base, p, nil
		}
		base = "fw-" + strutil.I64toa(int64(pkgFastRand())) + "-" + name
		attempt++
	}
}

func (s *FileOpService) VerifyMD5(path, expect string) (bool, error) {
	return md5util.ValidateFile(path, expect)
}

func (s *FileOpService) Stat(path string) (int64, error) {
	return fileutil.Size(path)
}

func (s *FileOpService) Delete(path string) error {
	err := fileutil.Delete(path)
	if err != nil && strings.TrimSpace(err.Error()) == "" {
		return wrapFileError(response.CodeInternal, "", err)
	}
	return err
}

func (s *FileOpService) FirmwareDir() string { return s.cfg.FirmwareDir }

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

func pkgFastRand() uint32 {
	counterMu.Lock()
	defer counterMu.Unlock()
	fastState ^= fastState << 13
	fastState ^= fastState >> 17
	fastState ^= fastState << 5
	return fastState
}
