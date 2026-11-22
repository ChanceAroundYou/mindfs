package controlsync

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 前端资源镜像的清单与打包。
//
// 清单是**安全相关**的产物：边缘拿它决定激活哪一版前端，并逐文件校验哈希。
// 所以这里只有一份实现 —— 主节点生成、边缘解析、测试构造夹具，全部走同一套
// 路径校验与哈希规则。多写一份 Node 版生成器迟早会与 Go 侧漂移，而漂移的表现是
// 「边缘把每一版都判成坏包，永远停在随包旧前端」，排查起来毫无线索。
const (
	// UIManifestName 是随构建产物一起发布的清单文件名，也是包内唯一可信的索引。
	UIManifestName = "ui-manifest.json"
	// UIMaxBundleBytes 是单个前端包的上限。前端产物在 10 MB 量级，200 MB 是防呆。
	UIMaxBundleBytes int64 = 200 << 20
	// UIMaxFiles 是一次打包/解包允许的文件数上限。
	UIMaxFiles = 5000
)

// UIFile 是清单里的一条产物。
type UIFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// UIManifest 是主节点发布的构建清单。
//
// MinServerBuild 是「能正确服务这一版前端的后端最低版本」：边缘二进制比它旧就
// 不激活，否则新前端会去调用旧后端没有的接口，页面报错而没有任何提示。
type UIManifest struct {
	BuildVersion   string   `json:"build_version"`
	MinServerBuild string   `json:"min_server_build"`
	Files          []UIFile `json:"files"`
}

// ValidUIFilePath 判断清单里的相对路径是否安全。
//
// 这套规则同时被三处使用：主节点打包（写进 tar 的 Name）、边缘解包（决定写到哪）、
// 以及清单校验。任何一处单独放宽都会变成路径穿越 —— 一个 `../` 就能写到配置目录之外。
func ValidUIFilePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") {
		return false
	}
	// 反斜杠在 Windows 上是分隔符，在 tar 里却只是普通字符：同一份清单在两端
	// 会解析成不同路径，必须直接拒绝。冒号同理（`C:` 盘符与 NTFS 备用数据流）。
	if strings.ContainsAny(p, "\\:\x00") {
		return false
	}
	// 分段规则：空段（`a//b`、`a/`）、当前目录、上跳目录一律拒绝。
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "." || seg == "" {
			return false
		}
	}
	// 收尾再归一化一次：上面的分段规则已经能挡住所有已知写法，这条是给
	// 「以后有人放宽分段规则」留的警报线 —— 任何规范化后会变短的路径都不该被接受。
	return path.Clean(p) == p
}

func validUISHA256(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// BuildUIManifest 遍历构建产物生成清单。
//
// 只接受普通文件：符号链接、设备文件、子目录一律报错。产物理应是纯文件树，
// 遇到别的类型说明这次构建不干净，宁可在打包时失败，也不要让边缘去猜。
func BuildUIManifest(dir, buildVersion, minServerBuild string) (*UIManifest, error) {
	dir = strings.TrimSpace(dir)
	buildVersion = strings.TrimSpace(buildVersion)
	minServerBuild = strings.TrimSpace(minServerBuild)
	if dir == "" {
		return nil, errors.New("ui manifest: empty dir")
	}
	if buildVersion == "" {
		return nil, errors.New("ui manifest: empty build version")
	}
	if minServerBuild == "" {
		minServerBuild = buildVersion
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("ui manifest: stat dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("ui manifest: %s is not a directory", dir)
	}

	manifest := &UIManifest{BuildVersion: buildVersion, MinServerBuild: minServerBuild}
	var total int64
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		slash := filepath.ToSlash(rel)
		// 清单不能把自己列进去：那要求文件内容包含自己的 sha256，是个不动点。
		// 它在包里单独作为一个条目出现（见 WriteUIBundle），边缘按「索引文件」处理。
		if slash == UIManifestName {
			return nil
		}
		if !ValidUIFilePath(slash) {
			return fmt.Errorf("ui manifest: unsafe path %q", slash)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("ui manifest: unsupported file type at %q", slash)
		}
		fi, statErr := d.Info()
		if statErr != nil {
			return statErr
		}
		sum, hashErr := hashFile(p)
		if hashErr != nil {
			return hashErr
		}
		manifest.Files = append(manifest.Files, UIFile{Path: slash, Size: fi.Size(), SHA256: sum})
		total += fi.Size()
		if int64(len(manifest.Files)) > UIMaxFiles {
			return fmt.Errorf("ui manifest: more than %d files", UIMaxFiles)
		}
		if total > UIMaxBundleBytes {
			return fmt.Errorf("ui manifest: larger than %d bytes", UIMaxBundleBytes)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return manifest, nil
}

// Validate 做结构校验，不读文件。解析远端清单时同样要用它 —— 清单来自对端，
// 在拿它去拼路径之前必须先确认每条路径是安全的相对路径。
func (m *UIManifest) Validate() error {
	if m == nil {
		return errors.New("ui manifest: nil")
	}
	if strings.TrimSpace(m.BuildVersion) == "" {
		return errors.New("ui manifest: empty build_version")
	}
	if strings.TrimSpace(m.MinServerBuild) == "" {
		return errors.New("ui manifest: empty min_server_build")
	}
	if len(m.Files) == 0 {
		return errors.New("ui manifest: no files")
	}
	if len(m.Files) > UIMaxFiles {
		return fmt.Errorf("ui manifest: more than %d files", UIMaxFiles)
	}
	seen := make(map[string]struct{}, len(m.Files))
	hasIndex := false
	var total int64
	for _, f := range m.Files {
		if !ValidUIFilePath(f.Path) {
			return fmt.Errorf("ui manifest: unsafe path %q", f.Path)
		}
		if _, dup := seen[f.Path]; dup {
			return fmt.Errorf("ui manifest: duplicate path %q", f.Path)
		}
		seen[f.Path] = struct{}{}
		if !validUISHA256(f.SHA256) {
			return fmt.Errorf("ui manifest: bad sha256 for %q", f.Path)
		}
		if f.Size < 0 {
			return fmt.Errorf("ui manifest: negative size for %q", f.Path)
		}
		total += f.Size
		if total > UIMaxBundleBytes {
			// 在下载之前就拒绝：等边沿开始收 200 MB 再回 413，只是把错误推迟。
			return fmt.Errorf("ui manifest: larger than %d bytes", UIMaxBundleBytes)
		}
		if f.Path == "index.html" {
			hasIndex = true
		}
	}
	if !hasIndex {
		// 没有 index.html 的「前端」激活后就是一张白页，比不更新更糟。
		return errors.New("ui manifest: missing index.html")
	}
	return nil
}

// LoadUIManifest 读取并校验磁盘上的清单。
func LoadUIManifest(dir string) (*UIManifest, error) {
	p := filepath.Join(strings.TrimSpace(dir), UIManifestName)
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var m UIManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("ui manifest: parse: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// MarshalUIManifest 生成清单文件的字节（缩进两格，便于人工核对与 diff）。
func MarshalUIManifest(m *UIManifest) ([]byte, error) {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// WriteUIBundle 按清单把构建产物打成 tar.gz。
//
// **包的内容由清单决定**，不是「把目录整个塞进去」：这样「包里有清单没列的文件」
// 在结构上就不可能发生（清单自己除外，它是包的索引，见下方），边缘也就不需要处理
// 「多出来的文件该不该信任」这种问题。
// 每个文件的哈希在流式写出的同时重算一遍，与清单不符就中断 —— 那说明目录在
// 生成清单之后被改过，此时发出的包必然通不过边缘校验。
func WriteUIBundle(w io.Writer, dir string, m *UIManifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	now := time.Unix(0, 0).UTC()
	var total int64
	for _, f := range m.Files {
		full := filepath.Join(dir, filepath.FromSlash(f.Path))
		// 二次确认拼出来的路径仍在 dir 之内（清单已在 Validate 里校验过，
		// 这里防的是「以后有人放宽了 ValidUIFilePath」）。
		rel, relErr := filepath.Rel(dir, full)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("ui bundle: path escapes dir: %q", f.Path)
		}
		src, openErr := os.Open(full)
		if openErr != nil {
			return fmt.Errorf("ui bundle: open %q: %w", f.Path, openErr)
		}
		hdr := &tar.Header{
			Name:     f.Path,
			Mode:     0o644,
			Size:     f.Size,
			Typeflag: tar.TypeReg,
			ModTime:  now,
			Format:   tar.FormatPAX,
		}
		if headErr := tw.WriteHeader(hdr); headErr != nil {
			src.Close()
			return headErr
		}
		sum := sha256.New()
		n, copyErr := io.CopyN(io.MultiWriter(tw, sum), src, f.Size)
		src.Close()
		if copyErr != nil {
			return fmt.Errorf("ui bundle: copy %q: %w", f.Path, copyErr)
		}
		if n != f.Size || hex.EncodeToString(sum.Sum(nil)) != f.SHA256 {
			return fmt.Errorf("ui bundle: %q changed since manifest", f.Path)
		}
		total += n
		if total > UIMaxBundleBytes {
			return fmt.Errorf("ui bundle: larger than %d bytes", UIMaxBundleBytes)
		}
	}
	// 清单本身也进包：解包后的目录是自描述的，一台机器改成 primary 之后仍能
	// 直接吐出 /ui/manifest。它没有列在自己里面（见 BuildUIManifest 的说明），
	// 是包里唯一「不是载荷」的条目。
	manifestPath := filepath.Join(dir, UIManifestName)
	if info, statErr := os.Stat(manifestPath); statErr == nil && info.Mode().IsRegular() {
		if err := appendTarFile(tw, manifestPath, UIManifestName, info.Size()); err != nil {
			return err
		}
		total += info.Size()
		if total > UIMaxBundleBytes {
			return fmt.Errorf("ui bundle: larger than %d bytes", UIMaxBundleBytes)
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// appendTarFile 把一个具体文件写进 tar（不校验哈希：调用方要么已经校验过，
// 要么写的是清单自己）。
func appendTarFile(tw *tar.Writer, full, name string, size int64) error {
	src, err := os.Open(full)
	if err != nil {
		return fmt.Errorf("ui bundle: open %q: %w", name, err)
	}
	defer src.Close()
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Mode:     0o644,
		Size:     size,
		Typeflag: tar.TypeReg,
		ModTime:  time.Unix(0, 0).UTC(),
		Format:   tar.FormatPAX,
	}); err != nil {
		return err
	}
	if _, err := io.CopyN(tw, src, size); err != nil {
		return fmt.Errorf("ui bundle: copy %q: %w", name, err)
	}
	return nil
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
