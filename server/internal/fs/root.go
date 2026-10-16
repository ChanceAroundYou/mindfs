package fs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/apperr"
)

const (
	metaDirName         = ".mindfs"
	MetaLocationProject = "project"
	MetaLocationHome    = "home"
	stateFileName       = "state.json"
	defaultMaxReadBytes = 64 * 1024
	maxFullReadBytes    = 128 << 20
)

var (
	metaFileLocksMu sync.Mutex
	metaFileLocks   = map[string]*sync.Mutex{}
)

func metaFileLock(path string) *sync.Mutex {
	metaFileLocksMu.Lock()
	defer metaFileLocksMu.Unlock()
	if lock, ok := metaFileLocks[path]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	metaFileLocks[path] = lock
	return lock
}

type RootInfo struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	DisplayName  string    `json:"display_name,omitempty"`
	RootPath     string    `json:"root_path"`
	MetaLocation string    `json:"meta_location,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	// MetaRoot 非空表示这是某个非主账户的根：meta 一律落在该账户私有目录下
	// （MetaRoot/<rootID>），绝不碰项目里的 .mindfs。由 Registry 在装载时按账户盖章，
	// 不持久化（同一个项目被不同账户添加时路径必须不同）。
	MetaRoot string `json:"-"`
}

func (r RootInfo) EffectiveName() string {
	if v := strings.TrimSpace(r.DisplayName); v != "" {
		return v
	}
	return r.Name
}

func NewRootInfo(id, name, rootPath string) RootInfo {
	if id == "" {
		id = filepath.Base(rootPath)
	}
	if name == "" {
		name = id
	}
	return RootInfo{ID: id, Name: name, RootPath: rootPath}
}

func (r RootInfo) rootDir() (string, error) {
	if r.RootPath == "" {
		return "", errors.New("root required")
	}
	return filepath.Clean(r.RootPath), nil
}

func (r RootInfo) resolveRelativePath(relPath string) (string, error) {
	if relPath == "" {
		relPath = "."
	}
	if filepath.IsAbs(relPath) {
		return "", errors.New("absolute path not allowed")
	}
	root, err := r.rootDir()
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(filepath.Join(root, relPath))
	rel, err := filepath.Rel(root, clean)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("path outside root")
	}
	return clean, nil
}

func (r RootInfo) effectiveMetaLocation() string {
	// 非主账户的 meta 恒为「账户私有目录」，语义等同 home（0o700 + 身份文件），
	// 这样会话库/任务库落账户目录，而项目里的 .mindfs 留给共享内容。
	if strings.TrimSpace(r.MetaRoot) != "" {
		return MetaLocationHome
	}
	return r.persistedMetaLocation()
}

// persistedMetaLocation 是注册表里记的元数据位置，不看账户。
// 决定「共享 meta」（上传文件、文件批注）落在哪儿——两个账户必须算出同一个答案，
// 所以这里只能用共享的偏好与注册表信息，不能受 MetaRoot 影响。
func (r RootInfo) persistedMetaLocation() string {
	if strings.TrimSpace(r.MetaLocation) == MetaLocationHome {
		return MetaLocationHome
	}
	return MetaLocationProject
}

// EffectiveMetaLocation returns the persisted metadata location, treating the
// missing value used by older registry entries as project-local.
func (r RootInfo) EffectiveMetaLocation() string {
	return r.effectiveMetaLocation()
}

func validRootID(id string) bool {
	id = strings.TrimSpace(id)
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id &&
		!strings.ContainsAny(id, `/\\`)
}

var userHomeDir = os.UserHomeDir

func homeMetaDir(rootID string) (string, error) {
	if !validRootID(rootID) {
		return "", errors.New("invalid root id")
	}
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, metaDirName, rootID), nil
}

func (r RootInfo) relativeFromAbsolute(absPath string) (string, error) {
	root, err := r.rootDir()
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(absPath)
	rel, err := filepath.Rel(root, clean)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("path outside root")
	}
	return filepath.ToSlash(rel), nil
}

// RootDir returns the absolute root directory path.
func (r RootInfo) RootDir() (string, error) {
	return r.rootDir()
}

// ValidateRelativePath validates that relPath is inside this root.
func (r RootInfo) ValidateRelativePath(relPath string) error {
	_, err := r.resolveRelativePath(relPath)
	return err
}

// NormalizePath returns a slash-style path relative to the root.
// Input may be relative to root or an absolute path inside root.
func (r RootInfo) NormalizePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("path required")
	}
	root, err := r.rootDir()
	if err != nil {
		return "", err
	}
	path = strings.SplitN(path, "#", 2)[0]
	if path == "" {
		return "", errors.New("path required")
	}
	cleanPath := filepath.Clean(path)
	if filepath.IsAbs(cleanPath) {
		if r.effectiveMetaLocation() == MetaLocationHome {
			// 用共享 meta：上传回传的绝对路径两个账户都要能映射回同一个逻辑路径
			metaDir := r.SharedMetaDir()
			if metaDir != "" {
				if rel, err := filepath.Rel(metaDir, cleanPath); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return filepath.ToSlash(filepath.Join(metaDirName, rel)), nil
				}
			}
		}
		return r.relativeFromAbsolute(cleanPath)
	}
	rootSlash := filepath.ToSlash(root)
	pathSlash := filepath.ToSlash(cleanPath)
	rootTrimmed := strings.TrimPrefix(rootSlash, "/")
	if rootTrimmed != "" && (pathSlash == rootTrimmed || strings.HasPrefix(pathSlash, rootTrimmed+"/")) {
		return r.relativeFromAbsolute(string(filepath.Separator) + filepath.FromSlash(pathSlash))
	}
	resolved, err := r.resolveRelativePath(cleanPath)
	if err != nil {
		return "", err
	}
	return r.relativeFromAbsolute(resolved)
}

// SharedMetaDir 是**两个账户共用**的 meta 目录：上传的文件与文件批注放这里。
//
// 与 MetaDir() 的区别：MetaDir() 对非主账户指向账户私有目录（会话库/任务库按账户分），
// 而上传与批注要共享，所以这里按「注册表记的元数据位置」算，不受 MetaRoot 影响——
// 两个账户必须算出同一个答案。
func (r RootInfo) SharedMetaDir() string {
	if strings.TrimSpace(r.MetaRoot) == "" {
		return r.MetaDir() // 主账户两者相同
	}
	if r.persistedMetaLocation() == MetaLocationHome {
		dir, err := homeMetaDir(r.ID)
		if err != nil {
			return ""
		}
		return dir
	}
	rootAbs, err := r.rootDir()
	if err != nil {
		return ""
	}
	return filepath.Join(rootAbs, metaDirName)
}

func (r RootInfo) MetaDir() string {
	// 非主账户：会话库/任务库落在账户私有目录里，与其他账户、与项目内的 .mindfs 都无关。
	// 上传文件与文件批注**不走这里**（它们要共享），走 sharedMetaDir()。
	if metaRoot := strings.TrimSpace(r.MetaRoot); metaRoot != "" {
		if !validRootID(r.ID) {
			return ""
		}
		return filepath.Join(metaRoot, r.ID)
	}
	if r.effectiveMetaLocation() == MetaLocationHome {
		metaDir, err := homeMetaDir(r.ID)
		if err != nil {
			return ""
		}
		return metaDir
	}
	rootAbs, err := r.rootDir()
	if err != nil {
		return ""
	}
	return filepath.Join(rootAbs, metaDirName)
}

func (r RootInfo) StatRoot() (os.FileInfo, error) {
	rootAbs, err := r.rootDir()
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(rootAbs)
	if err != nil {
		return nil, apperr.Wrap("stat", rootAbs, err)
	}
	if !info.IsDir() {
		return nil, errors.New("root is not a directory")
	}
	return info, nil
}
