package fs

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func (r *Registry) Upsert(root string) (RootInfo, error) {
	return r.UpsertWithMetaLocation(root, MetaLocationProject)
}

// snapshotLocked 记下 dirs/order 的副本，返回恢复函数。
//
// 凡是「先改内存、再 saveLocked()」的方法，落盘失败时都必须调用它回滚 ——
// 否则失败的写入会**留在列表里**：用户看到项目出现了、点进去却什么都没有。
// 只读空工作区（path==""，即「这个账户在本机没有数据」）正是这条路径的高频场景。
func (r *Registry) snapshotLocked() func() {
	prevDirs := make(map[string]RootInfo, len(r.dirs))
	for key, value := range r.dirs {
		prevDirs[key] = value
	}
	prevOrder := append([]string(nil), r.order...)
	return func() {
		r.dirs = prevDirs
		r.order = prevOrder
	}
}

func (r *Registry) UpsertWithMetaLocation(root, metaLocation string) (RootInfo, error) {
	if root == "" {
		return RootInfo{}, errors.New("root required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	name := filepath.Base(root)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return RootInfo{}, errors.New("invalid directory name")
	}
	rollback := r.snapshotLocked()
	dir, ok := r.dirs[name]
	if !ok {
		metaLocation, err := NormalizeMetaLocation(metaLocation)
		if err != nil {
			return RootInfo{}, err
		}
		dir = NewRootInfo(name, name, root)
		// MetaLocation 决定「共享 meta」（上传/文件批注）落在项目内还是 ~/.mindfs，
		// 两个账户必须算出一致结果，所以按正常的偏好走，不因账户而改。
		dir.MetaLocation = metaLocation
		dir.CreatedAt = now
		r.order = append(r.order, name)
	} else if !sameRegistryPath(dir.RootPath, root) {
		return RootInfo{}, fmt.Errorf("%w: %q is already managed at %s; rename the directory before adding %s", ErrRootNameConflict, name, dir.RootPath, root)
	}
	dir.UpdatedAt = now
	dir = r.stamp(dir)
	r.dirs[name] = dir
	if err := r.saveLocked(); err != nil {
		rollback()
		return RootInfo{}, err
	}
	return dir, nil
}

func sameRegistryPath(a, b string) bool {
	left := cleanRegistryPath(a)
	right := cleanRegistryPath(b)
	if runtime.GOOS == "windows" {
		left = strings.ToLower(left)
		right = strings.ToLower(right)
	}
	return left == right
}

func cleanRegistryPath(path string) string {
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if abs, err := filepath.Abs(cleaned); err == nil {
		return abs
	}
	return cleaned
}
