package controlsync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/update"
)

// 边缘的前端资源镜像。
//
// 为什么要有它：第二台 control 节点存在的全部意义是「主节点不可达时还能出页面」。
// 页面本身是主节点构建出来的产物，边缘必须在自己还连得上主节点的时候把它搬到本机，
// 之后主节点挂了照样能打开。
//
// 三条硬规矩：
//  1. **校验通过才切换**。半份资源绝不能看起来像一份 —— 坏包一律丢弃，旧版本继续用。
//  2. **指针是普通文件**。不用符号链接：Windows 上创建符号链接要管理员或开发者模式，
//     失败时页面直接打不开，而换指针文件在任何平台都能做。
//  3. **新前端配旧后端会报错**。`min_server_build` 高于本机二进制版本时不激活，
//     否则页面会去调用本机不存在的接口，用户看到的是「打不开」，而不是「该升级了」。
const (
	// UIMirrorInterval 是两次检查之间的间隔。
	UIMirrorInterval = 5 * time.Minute
	// uiMirrorPointerName 是「当前该用哪一版」的指针文件，内容就是版本号。
	uiMirrorPointerName = "current"
	// uiMirrorCompleteMarker 放在版本目录里：没有它就不算一份完整资源。
	uiMirrorCompleteMarker = ".complete"
	// uiMirrorKeepVersions 是保留的完整版本数（含当前版本）。
	uiMirrorKeepVersions = 2
)

// ErrUIMirrorServerTooOld 表示新前端要求更高的后端版本：不激活、继续用本机旧构建。
var ErrUIMirrorServerTooOld = errors.New("controlsync: ui build requires newer server")

// UIMirrorOptions 是镜像的启动参数。
type UIMirrorOptions struct {
	// Dir 是镜像根目录（`<configDir>/ui`）。
	Dir        string
	PrimaryURL string
	Token      string
	InstanceID string
	CAFile     string
	// ServerBuild 是本机二进制版本，用于 min_server_build 判定。
	ServerBuild string
	Interval    time.Duration
	// Client 留空时按 CAFile 建一个只信系统根（或指定 CA）的 https 客户端。
	Client  *http.Client
	OnError func(error)
}

// UIMirror 负责把主节点的前端构建搬到本机并按版本原子切换。
type UIMirror struct {
	dir         string
	base        *url.URL
	client      *http.Client
	token       string
	instanceID  string
	serverBuild string
	interval    time.Duration
	onError     func(error)

	// mu 串行化切换：两个 SyncOnce 同时跑时，只有一个能下载并写指针，
	// 否则两个进程会各写一半、指针指向谁都不完整。
	mu sync.Mutex
	// current 是内存里的指针副本，供每次请求读取（不能每请求都读盘）。
	currentMu sync.RWMutex
	current   string
}

// NewUIMirror 构造镜像。primary_url 必须是 https（与同步通道同一条规矩）。
func NewUIMirror(opts UIMirrorOptions) (*UIMirror, error) {
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		return nil, errors.New("controlsync: ui mirror dir required")
	}
	raw := strings.TrimSpace(opts.PrimaryURL)
	if raw == "" {
		return nil, errors.New("controlsync: ui mirror primary_url required")
	}
	base, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("controlsync: parse ui mirror primary_url: %w", err)
	}
	if base.Scheme != "https" {
		return nil, fmt.Errorf("controlsync: ui mirror primary_url must be https, got %q", base.Scheme)
	}
	if base.Host == "" {
		return nil, errors.New("controlsync: ui mirror primary_url missing host")
	}
	client := opts.Client
	if client == nil {
		client, err = newSyncHTTPClient(opts.CAFile)
		if err != nil {
			return nil, err
		}
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = UIMirrorInterval
	}
	m := &UIMirror{
		dir:         dir,
		base:        base,
		client:      client,
		token:       opts.Token,
		instanceID:  strings.TrimSpace(opts.InstanceID),
		serverBuild: strings.TrimSpace(opts.ServerBuild),
		interval:    interval,
		onError:     opts.OnError,
	}
	// 重启后接着用上次的版本：指针在磁盘上，启动时读回内存。
	m.current = m.readPointer()
	return m, nil
}

// Run 立刻检查一次，之后按间隔轮询，直到 ctx 结束。失败只上报，不动指针：
// 拿不到新版本时继续用本机已有的那一版，这正是「主节点不可达也能出页面」。
func (m *UIMirror) Run(ctx context.Context) {
	if m == nil {
		return
	}
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		if err := m.SyncOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			m.reportError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// CurrentDir 返回当前该用的静态目录；没有可用镜像时返回 ""（调用方回落到安装目录）。
func (m *UIMirror) CurrentDir() string {
	if m == nil {
		return ""
	}
	m.currentMu.RLock()
	v := m.current
	m.currentMu.RUnlock()
	if v == "" {
		return ""
	}
	dir := filepath.Join(m.dir, v)
	if !m.isComplete(dir) {
		// 指针指向一份不完整的资源 = 白屏。宁可回落到安装目录。
		return ""
	}
	return dir
}

// CurrentVersion 返回当前镜像版本（可能为空）。
func (m *UIMirror) CurrentVersion() string {
	if m == nil {
		return ""
	}
	m.currentMu.RLock()
	defer m.currentMu.RUnlock()
	return m.current
}

// SyncOnce 检查并（必要时）切换一次。
func (m *UIMirror) SyncOnce(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	manifest, err := m.fetchManifest(ctx)
	if err != nil {
		return err
	}
	// 版本号直接当目录名用，所以在碰磁盘之前先挡：`../x` 这类值能把资源写到配置目录之外。
	if !validUIVersionName(manifest.BuildVersion) {
		return fmt.Errorf("controlsync: unsafe ui build version %q", manifest.BuildVersion)
	}
	if manifest.BuildVersion == m.CurrentVersion() && m.isComplete(filepath.Join(m.dir, manifest.BuildVersion)) {
		return nil
	}
	if strings.TrimSpace(m.serverBuild) != "" && update.IsNewerVersion(manifest.MinServerBuild, m.serverBuild) {
		// 不激活：本机后端还服务不了这一版前端。旧构建继续用，页面照常能开。
		return fmt.Errorf("%w: need %s, have %s", ErrUIMirrorServerTooOld, manifest.MinServerBuild, m.serverBuild)
	}

	versionDir := filepath.Join(m.dir, manifest.BuildVersion)
	if !m.isComplete(versionDir) {
		if err := m.install(ctx, manifest); err != nil {
			return err
		}
	}
	if err := m.writePointer(manifest.BuildVersion); err != nil {
		return err
	}
	m.currentMu.Lock()
	m.current = manifest.BuildVersion
	m.currentMu.Unlock()
	m.prune(manifest.BuildVersion)
	return nil
}

// install 下载、校验、落盘。
//
// 顺序是不可换的：先写 `<version>.tmp`，全部校验通过后才 rename 成 `<version>` 并写
// `.complete`。任何一步失败都只留下一个 tmp 目录（下次覆盖），指针一动没动。
func (m *UIMirror) install(ctx context.Context, manifest *UIManifest) error {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return fmt.Errorf("controlsync: mkdir ui mirror: %w", err)
	}
	tmpDir := filepath.Join(m.dir, manifest.BuildVersion+".tmp")
	finalDir := filepath.Join(m.dir, manifest.BuildVersion)
	if err := os.RemoveAll(tmpDir); err != nil {
		return err
	}
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		return err
	}

	body, err := m.fetchBundle(ctx)
	if err != nil {
		os.RemoveAll(tmpDir)
		return err
	}
	if err := unpackUIBundle(bytes.NewReader(body), tmpDir, manifest); err != nil {
		os.RemoveAll(tmpDir)
		return err
	}
	if err := os.WriteFile(filepath.Join(tmpDir, uiMirrorCompleteMarker), []byte(manifest.BuildVersion+"\n"), 0o600); err != nil {
		os.RemoveAll(tmpDir)
		return err
	}
	// 目标目录已存在（上次下载完但没切成功）时整体替换：内容是同一版本，但宁可重来。
	if err := os.RemoveAll(finalDir); err != nil {
		os.RemoveAll(tmpDir)
		return err
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("controlsync: ui mirror rename: %w", err)
	}
	return nil
}

func (m *UIMirror) fetchManifest(ctx context.Context) (*UIManifest, error) {
	raw, err := m.get(ctx, "/api/control-sync/ui/manifest", 4<<20)
	if err != nil {
		return nil, err
	}
	var manifest UIManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("controlsync: parse ui manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (m *UIMirror) fetchBundle(ctx context.Context) ([]byte, error) {
	// 上限比清单允许的总量大一点：多出来的是 tar/gzip 头与填充。
	raw, err := m.get(ctx, "/api/control-sync/ui/bundle", UIMaxBundleBytes+(8<<20))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, errors.New("controlsync: empty ui bundle")
	}
	return raw, nil
}

// get 发一个签名过的 GET。签名覆盖空 body，与主节点的校验口径一致。
func (m *UIMirror) get(ctx context.Context, path string, limit int64) ([]byte, error) {
	u := *m.base
	u.Path = joinSyncPath(m.base.Path, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if err := SignSyncRequest(req, m.token, m.instanceID, nil, time.Now()); err != nil {
		return nil, fmt.Errorf("controlsync: sign ui %s: %w", path, err)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("controlsync: ui %s -> %d", path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("controlsync: ui %s exceeds %d bytes", path, limit)
	}
	return body, nil
}

func (m *UIMirror) isComplete(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, uiMirrorCompleteMarker))
	return err == nil && info.Mode().IsRegular()
}

func (m *UIMirror) readPointer() string {
	raw, err := os.ReadFile(filepath.Join(m.dir, uiMirrorPointerName))
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(raw))
	if !validUIVersionName(v) {
		return ""
	}
	return v
}

// writePointer 原子替换指针文件。
//
// 先写 `current.new` 再 rename：读指针的一方要么看到旧内容、要么看到新内容，
// 不会读到半行。Windows 上 rename 不能覆盖已存在的目标，退化成「先删后改名」——
// 有一个极短的空窗，但指针缺失的后果是回落到安装目录，不是白屏。
func (m *UIMirror) writePointer(version string) error {
	path := filepath.Join(m.dir, uiMirrorPointerName)
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(version+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err == nil {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("controlsync: ui mirror pointer: %w", err)
	}
	return nil
}

// prune 只保留当前版本与最近一份完整版本，并清掉所有残留的 tmp 目录。
func (m *UIMirror) prune(keep string) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	type candidate struct {
		name string
		at   time.Time
	}
	var complete []candidate
	for _, e := range entries {
		if !e.IsDir() {
			if strings.HasSuffix(e.Name(), ".tmp") {
				// 文件形态的 tmp（指针写入中断）也清掉。
				os.RemoveAll(filepath.Join(m.dir, e.Name()))
			}
			continue
		}
		name := e.Name()
		dir := filepath.Join(m.dir, name)
		if strings.HasSuffix(name, ".tmp") {
			os.RemoveAll(dir)
			continue
		}
		if name == keep || !m.isComplete(dir) {
			if name != keep && !m.isComplete(dir) {
				// 没写完的版本目录留着只会占空间，也不会被任何指针选中。
				os.RemoveAll(dir)
			}
			continue
		}
		info, statErr := os.Stat(filepath.Join(dir, uiMirrorCompleteMarker))
		at := time.Time{}
		if statErr == nil {
			at = info.ModTime()
		}
		complete = append(complete, candidate{name: name, at: at})
	}
	sort.Slice(complete, func(i, j int) bool { return complete[i].at.After(complete[j].at) })
	for i, c := range complete {
		if i < uiMirrorKeepVersions-1 {
			continue
		}
		os.RemoveAll(filepath.Join(m.dir, c.name))
	}
}

func (m *UIMirror) reportError(err error) {
	if m.onError != nil {
		m.onError(err)
	}
}

// validUIVersionName 限定版本目录名：版本号来自对端，直接当目录名用就是路径穿越。
func validUIVersionName(v string) bool {
	if v == "" || len(v) > 64 || v == "." || v == ".." {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		ok := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '.' || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// unpackUIBundle 解包到 destDir，逐文件校验哈希，并拒绝清单之外的任何东西。
//
// 这是唯一一条「把对端字节写到本机磁盘」的路径，所以规则必须是白名单：
// 名字要在清单里、路径要安全、类型只能是普通文件或目录、大小与 sha256 都要对得上、
// 总数与总量都有上限。包里的 ui-manifest.json 单独处理：它必须与刚下载的清单一致。
func unpackUIBundle(r io.Reader, destDir string, m *UIManifest) error {
	gz, err := gzip.NewReader(io.LimitReader(r, UIMaxBundleBytes+(8<<20)))
	if err != nil {
		return fmt.Errorf("controlsync: ui bundle gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	want := make(map[string]UIFile, len(m.Files))
	for _, f := range m.Files {
		want[f.Path] = f
	}
	seen := make(map[string]struct{}, len(m.Files))
	manifestBytes, err := MarshalUIManifest(m)
	if err != nil {
		return err
	}
	manifestSeen := false
	files := 0
	var total int64

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("controlsync: ui bundle tar: %w", err)
		}
		name := hdr.Name
		if !ValidUIFilePath(name) {
			return fmt.Errorf("controlsync: ui bundle unsafe path %q", name)
		}
		files++
		if files > UIMaxFiles+1 {
			return fmt.Errorf("controlsync: ui bundle has more than %d files", UIMaxFiles)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(filepath.Join(destDir, filepath.FromSlash(name)), 0o700); err != nil {
				return err
			}
			continue
		case tar.TypeReg:
		default:
			// 符号链接 / 硬链接 / 设备 / FIFO：解包即等于把本机文件系统交给对端。
			return fmt.Errorf("controlsync: ui bundle entry %q has unsupported type %d", name, hdr.Typeflag)
		}

		body, err := io.ReadAll(io.LimitReader(tr, hdr.Size+1))
		if err != nil {
			return fmt.Errorf("controlsync: ui bundle read %q: %w", name, err)
		}
		if int64(len(body)) != hdr.Size {
			return fmt.Errorf("controlsync: ui bundle entry %q truncated", name)
		}

		if name == UIManifestName {
			if !bytes.Equal(body, manifestBytes) {
				// 包里的清单与刚下载的清单不是同一份：说明两次请求之间产物被换过，
				// 继续解包只会得到一份自相矛盾的资源。
				return errors.New("controlsync: ui bundle manifest does not match")
			}
			manifestSeen = true
			if err := os.WriteFile(filepath.Join(destDir, UIManifestName), body, 0o600); err != nil {
				return err
			}
			continue
		}

		wantFile, ok := want[name]
		if !ok {
			return fmt.Errorf("controlsync: ui bundle entry %q not in manifest", name)
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("controlsync: ui bundle duplicate entry %q", name)
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != wantFile.SHA256 || int64(len(body)) != wantFile.Size {
			return fmt.Errorf("controlsync: ui bundle %q failed verification", name)
		}
		seen[name] = struct{}{}
		total += int64(len(body))
		if total > UIMaxBundleBytes {
			return fmt.Errorf("controlsync: ui bundle larger than %d bytes", UIMaxBundleBytes)
		}
		full := filepath.Join(destDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(full, body, 0o600); err != nil {
			return err
		}
	}

	// 清单里列了、包里没给：这份资源是残缺的，激活后页面会缺文件。
	for _, f := range m.Files {
		if f.Path == UIManifestName {
			continue
		}
		if _, ok := seen[f.Path]; !ok {
			return fmt.Errorf("controlsync: ui bundle missing %q", f.Path)
		}
	}
	if !manifestSeen {
		return errors.New("controlsync: ui bundle missing manifest")
	}
	return nil
}
