package controlsync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mindfs/server/internal/update"
)

// 一个跑在 TLS 上的「主节点」：吐清单与真实打包出来的 tar.gz，并校验每个请求都签名过。
//
// 用真实打包而不是手搓 tar：镜像能不能解开自己家的包，正是这条链路的核心，
// 夹具与实现共用 WriteUIBundle 才能保证「包变了但解包没跟着变」会被测出来。
type uiPrimary struct {
	srv          *httptest.Server
	manifest     []byte
	bundle       []byte
	bundleHits   atomic.Int32
	manifestHits atomic.Int32
	// bundleOverride 允许用例换掉包内容（模拟坏包）。
	bundleOverride func() []byte
	// manifestOverride 允许用例换掉清单（模拟要求更高后端 / 路径穿越 / 坏 JSON）。
	manifestOverride func() []byte
}

func newUIPrimary(t *testing.T, files map[string]string, buildVersion, minServerBuild string) *uiPrimary {
	t.Helper()
	dir := writeUIFixture(t, files)
	m, err := BuildUIManifest(dir, buildVersion, minServerBuild)
	if err != nil {
		t.Fatalf("BuildUIManifest: %v", err)
	}
	raw, err := MarshalUIManifest(m)
	if err != nil {
		t.Fatalf("MarshalUIManifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, UIManifestName), raw, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteUIBundle(&buf, dir, m); err != nil {
		t.Fatalf("WriteUIBundle: %v", err)
	}

	p := &uiPrimary{manifest: raw, bundle: buf.Bytes()}
	p.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 主节点侧一律要求签名：镜像不签名就等于把控制面产物挂在公网上。
		if err := VerifySyncRequest(r, nil, SyncVerifyOptions{Token: "tok"}); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/control-sync/ui/manifest":
			p.manifestHits.Add(1)
			body := p.manifest
			if p.manifestOverride != nil {
				body = p.manifestOverride()
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
		case "/api/control-sync/ui/bundle":
			p.bundleHits.Add(1)
			body := p.bundle
			if p.bundleOverride != nil {
				body = p.bundleOverride()
			}
			w.Header().Set("Content-Type", "application/gzip")
			w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *uiPrimary) mirror(t *testing.T, dir, serverBuild string) *UIMirror {
	t.Helper()
	m, err := NewUIMirror(UIMirrorOptions{
		Dir:         dir,
		PrimaryURL:  p.srv.URL,
		Token:       "tok",
		InstanceID:  "edge-test",
		ServerBuild: serverBuild,
		Client:      p.srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewUIMirror: %v", err)
	}
	return m
}

var uiFiles = map[string]string{
	"index.html":        "<html>v1</html>",
	"assets/index-a.js": "console.log('a')",
	"favicon.svg":       "<svg/>",
}

// 正常路径：下载 → 校验 → 落盘 → 指针切换，之后 CurrentDir 指向这一版。
func TestUIMirrorSyncSwitchesToVerifiedBundle(t *testing.T) {
	p := newUIPrimary(t, uiFiles, "v1.0.0", "v1.0.0")
	dir := filepath.Join(t.TempDir(), "ui")
	m := p.mirror(t, dir, "v1.0.0")

	if got := m.CurrentDir(); got != "" {
		t.Fatalf("首次同步前不该有可用目录，实际 %q", got)
	}
	if err := m.SyncOnce(context.Background()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	got := m.CurrentDir()
	if got != filepath.Join(dir, "v1.0.0") {
		t.Fatalf("CurrentDir = %q，应为 %q", got, filepath.Join(dir, "v1.0.0"))
	}
	// 内容真的落盘了，而且逐字节等于主节点那份。
	for name, want := range uiFiles {
		raw, err := os.ReadFile(filepath.Join(got, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("读 %s: %v", name, err)
		}
		if string(raw) != want {
			t.Fatalf("%s 内容 = %q，应为 %q", name, raw, want)
		}
	}
	// 指针是普通文件，不是符号链接：Windows 上建符号链接要特权，失败就是打不开页面。
	ptr := filepath.Join(dir, uiMirrorPointerName)
	info, err := os.Lstat(ptr)
	if err != nil {
		t.Fatalf("指针文件不存在: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("指针必须是普通文件 —— 符号链接在 Windows 上需要额外权限")
	}
	if body := strings.TrimSpace(string(mustRead(t, ptr))); body != "v1.0.0" {
		t.Fatalf("指针内容 = %q", body)
	}
	// 没有残留 tmp 目录。
	if _, err := os.Stat(filepath.Join(dir, "v1.0.0.tmp")); !os.IsNotExist(err) {
		t.Fatal("成功后不该留下 .tmp 目录")
	}
}

// 坏包：一个字节被改过，校验必须拦住，旧版本原封不动继续用。
func TestUIMirrorKeepsOldBuildOnBadBundle(t *testing.T) {
	p := newUIPrimary(t, uiFiles, "v1.0.0", "v1.0.0")
	dir := filepath.Join(t.TempDir(), "ui")
	m := p.mirror(t, dir, "v1.0.0")
	if err := m.SyncOnce(context.Background()); err != nil {
		t.Fatalf("首轮 SyncOnce: %v", err)
	}
	good := m.CurrentDir()

	// 主节点发了新版本，但包在传输中被改坏（同一个 gzip 里翻一个字节）。
	p.manifest = mustMarshal(t, &UIManifest{
		BuildVersion:   "v2.0.0",
		MinServerBuild: "v1.0.0",
		Files:          []UIFile{{Path: "index.html", Size: 15, SHA256: shaOf("<html>v2</html>")}},
	})
	broken := append([]byte(nil), p.bundle...)
	broken[len(broken)/2] ^= 0xff
	p.bundleOverride = func() []byte { return broken }

	if err := m.SyncOnce(context.Background()); err == nil {
		t.Fatal("坏包必须报错，不能静默切换")
	}
	if got := m.CurrentDir(); got != good {
		t.Fatalf("坏包之后应继续用旧版 %q，实际 %q", good, got)
	}
	if got := m.CurrentVersion(); got != "v1.0.0" {
		t.Fatalf("当前版本 = %q，应保持 v1.0.0", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "v2.0.0")); !os.IsNotExist(err) {
		t.Fatal("校验失败的版本不该留下正式目录")
	}
	if _, err := os.Stat(filepath.Join(dir, "v2.0.0.tmp")); !os.IsNotExist(err) {
		t.Fatal("校验失败必须清掉 .tmp，否则下次会误以为下了一半")
	}
}

// 清单内容对但哈希不对：这是最容易被「只验长度」放过去的一种坏包。
func TestUIMirrorRejectsHashMismatchEvenWhenLengthMatches(t *testing.T) {
	p := newUIPrimary(t, uiFiles, "v1.0.0", "v1.0.0")
	dir := filepath.Join(t.TempDir(), "ui")
	m := p.mirror(t, dir, "v1.0.0")

	// 用另一个等长产物替换 index.html，同时让清单仍描述原内容。
	other := writeUIFixture(t, map[string]string{
		"index.html":        "<html>v9</html>", // 与 <html>v1</html> 等长
		"assets/index-a.js": "console.log('a')",
		"favicon.svg":       "<svg/>",
	})
	om, err := BuildUIManifest(other, "v1.0.0", "v1.0.0")
	if err != nil {
		t.Fatalf("BuildUIManifest: %v", err)
	}
	raw := mustMarshal(t, om)
	if err := os.WriteFile(filepath.Join(other, UIManifestName), raw, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteUIBundle(&buf, other, om); err != nil {
		t.Fatalf("WriteUIBundle: %v", err)
	}
	// 包是 v9 的内容，但清单说大小 15、哈希是 v1 的 —— 长度完全一致。
	p.bundleOverride = func() []byte { return buf.Bytes() }

	err = m.SyncOnce(context.Background())
	if err == nil {
		t.Fatal("哈希不符必须报错")
	}
	if !strings.Contains(err.Error(), "verification") && !strings.Contains(err.Error(), "manifest does not match") {
		t.Fatalf("错误应指向校验失败，实际: %v", err)
	}
	if m.CurrentDir() != "" {
		t.Fatal("校验失败不该激活任何版本")
	}
}

// 新前端要求更高的后端：不激活，继续用本机现有的（这里是安装包自带的那份）。
func TestUIMirrorDoesNotActivateWhenServerTooOld(t *testing.T) {
	p := newUIPrimary(t, uiFiles, "v2.0.0", "v2.0.0")
	dir := filepath.Join(t.TempDir(), "ui")
	// 本机二进制落后于 min_server_build。
	m := p.mirror(t, dir, "v1.5.0")

	err := m.SyncOnce(context.Background())
	if !errors.Is(err, ErrUIMirrorServerTooOld) {
		t.Fatalf("应回 ErrUIMirrorServerTooOld，实际 %v", err)
	}
	if m.CurrentDir() != "" {
		t.Fatal("后端太旧时不该激活新前端 —— 页面会去调不存在的接口")
	}
	if p.bundleHits.Load() != 0 {
		t.Fatal("判定太旧应在下载之前，不该白拉一整个包")
	}

	// 构建号语义必须走同一套解析：v2.0.0 > v1.5.0 成立，反过来不成立。
	if !update.IsNewerVersion("v2.0.0", "v1.5.0") || update.IsNewerVersion("v1.5.0", "v2.0.0") {
		t.Fatal("版本比较方向反了")
	}
	if !update.IsNewerVersion("v0.5.5-1116", "v0.5.5-1115") {
		t.Fatal("本 fork 的构建号标签必须参与比较：1116 比 1115 新")
	}
}

// 已经是最新版本时不重复下载。
func TestUIMirrorSkipsWhenAlreadyCurrent(t *testing.T) {
	p := newUIPrimary(t, uiFiles, "v1.0.0", "v1.0.0")
	dir := filepath.Join(t.TempDir(), "ui")
	m := p.mirror(t, dir, "v1.0.0")
	if err := m.SyncOnce(context.Background()); err != nil {
		t.Fatalf("首轮: %v", err)
	}
	before := p.bundleHits.Load()
	if err := m.SyncOnce(context.Background()); err != nil {
		t.Fatalf("次轮: %v", err)
	}
	if p.bundleHits.Load() != before {
		t.Fatal("版本没变就不该再下一遍包")
	}
}

// 重启后接着用上次的版本：指针在磁盘上，新进程不必等一次同步就能出页面。
func TestUIMirrorReadsPointerOnStartup(t *testing.T) {
	p := newUIPrimary(t, uiFiles, "v1.0.0", "v1.0.0")
	dir := filepath.Join(t.TempDir(), "ui")
	if err := p.mirror(t, dir, "v1.0.0").SyncOnce(context.Background()); err != nil {
		t.Fatalf("首轮: %v", err)
	}
	// 新进程：只用同一个目录再构造一次，不碰网络（主节点关掉）。
	p.srv.Close()
	restarted := p.mirror(t, dir, "v1.0.0")
	if got := restarted.CurrentVersion(); got != "v1.0.0" {
		t.Fatalf("重启后应读回 v1.0.0，实际 %q", got)
	}
	if restarted.CurrentDir() == "" {
		t.Fatal("重启后应立刻有可用目录")
	}
}

// 指针指向不完整的目录（上次切换写了一半就崩了）：回落到安装目录，不能白屏。
func TestUIMirrorIgnoresPointerToIncompleteBuild(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ui")
	if err := os.MkdirAll(filepath.Join(dir, "v1.0.0"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, uiMirrorPointerName), []byte("v1.0.0\n"), 0o600); err != nil {
		t.Fatalf("write pointer: %v", err)
	}
	m, err := NewUIMirror(UIMirrorOptions{
		Dir:        dir,
		PrimaryURL: "https://primary.example",
		Token:      "tok",
	})
	if err != nil {
		t.Fatalf("NewUIMirror: %v", err)
	}
	if m.CurrentVersion() != "v1.0.0" {
		t.Fatalf("指针内容是 v1.0.0，实际 %q", m.CurrentVersion())
	}
	if got := m.CurrentDir(); got != "" {
		t.Fatalf("没有 .complete 的目录不能拿来服务，实际返回 %q", got)
	}
}

// 保留两版：切到新版本后，上一个版本还在（回滚与已打开标签页的懒加载都要它）。
func TestUIMirrorKeepsPreviousBuild(t *testing.T) {
	p := newUIPrimary(t, uiFiles, "v1.0.0", "v1.0.0")
	dir := filepath.Join(t.TempDir(), "ui")
	m := p.mirror(t, dir, "v1.0.0")
	if err := m.SyncOnce(context.Background()); err != nil {
		t.Fatalf("v1: %v", err)
	}
	// v2：清单与包都从真实产物生成，避免手写大小/哈希写错时把用例变成一个算术题。
	v2Dir := writeUIFixture(t, map[string]string{
		"index.html":        "<html>v2</html>",
		"assets/index-a.js": "console.log('a')",
		"favicon.svg":       "<svg/>",
	})
	v2Manifest, err := BuildUIManifest(v2Dir, "v2.0.0", "v1.0.0")
	if err != nil {
		t.Fatalf("BuildUIManifest v2: %v", err)
	}
	p.manifest = mustMarshal(t, v2Manifest)
	if err := os.WriteFile(filepath.Join(v2Dir, UIManifestName), p.manifest, 0o644); err != nil {
		t.Fatalf("write manifest v2: %v", err)
	}
	var v2Buf bytes.Buffer
	if err := WriteUIBundle(&v2Buf, v2Dir, v2Manifest); err != nil {
		t.Fatalf("WriteUIBundle v2: %v", err)
	}
	p.bundleOverride = func() []byte { return v2Buf.Bytes() }
	if err := m.SyncOnce(context.Background()); err != nil {
		t.Fatalf("v2: %v", err)
	}
	if m.CurrentVersion() != "v2.0.0" {
		t.Fatalf("应切到 v2.0.0，实际 %q", m.CurrentVersion())
	}
	for _, v := range []string{"v1.0.0", "v2.0.0"} {
		if !m.isComplete(filepath.Join(dir, v)) {
			t.Fatalf("%s 应仍然完整保留（保留两版）", v)
		}
	}
}

// 第三版到来时最老那版被清掉；残留的 .tmp 也一起清。
func TestUIMirrorPrunesBeyondTwoVersions(t *testing.T) {
	dir := t.TempDir()
	m, err := NewUIMirror(UIMirrorOptions{Dir: dir, PrimaryURL: "https://primary.example", Token: "tok"})
	if err != nil {
		t.Fatalf("NewUIMirror: %v", err)
	}
	writeComplete := func(v string, age time.Duration) {
		d := filepath.Join(dir, v)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		marker := filepath.Join(d, uiMirrorCompleteMarker)
		if err := os.WriteFile(marker, []byte(v), 0o600); err != nil {
			t.Fatalf("write marker: %v", err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(marker, at, at); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	writeComplete("v1", 3*time.Hour)
	writeComplete("v2", 2*time.Hour)
	writeComplete("v3", time.Hour)
	if err := os.MkdirAll(filepath.Join(dir, "v4.tmp"), 0o700); err != nil {
		t.Fatalf("mkdir tmp: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "half"), 0o700); err != nil {
		t.Fatalf("mkdir half: %v", err)
	}

	m.prune("v3")

	if !m.isComplete(filepath.Join(dir, "v3")) {
		t.Fatal("当前版本不能被清掉")
	}
	if !m.isComplete(filepath.Join(dir, "v2")) {
		t.Fatal("最近一份旧版本要保留")
	}
	if _, err := os.Stat(filepath.Join(dir, "v1")); !os.IsNotExist(err) {
		t.Fatal("第三份旧版本应被清掉")
	}
	if _, err := os.Stat(filepath.Join(dir, "v4.tmp")); !os.IsNotExist(err) {
		t.Fatal(".tmp 残留应被清掉")
	}
	if _, err := os.Stat(filepath.Join(dir, "half")); !os.IsNotExist(err) {
		t.Fatal("没有 .complete 的目录应被清掉")
	}
}

// 并发同步只有一个赢家：否则两个协程各下一半、指针指向谁都不完整。
func TestUIMirrorConcurrentSyncHasSingleWinner(t *testing.T) {
	p := newUIPrimary(t, uiFiles, "v1.0.0", "v1.0.0")
	dir := filepath.Join(t.TempDir(), "ui")
	m := p.mirror(t, dir, "v1.0.0")

	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.SyncOnce(context.Background()); err != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d 个并发同步失败", failures.Load())
	}
	if hits := p.bundleHits.Load(); hits != 1 {
		t.Fatalf("只该下载一次包，实际 %d 次", hits)
	}
	if m.CurrentVersion() != "v1.0.0" || !m.isComplete(filepath.Join(dir, "v1.0.0")) {
		t.Fatal("并发之后应正好切到 v1.0.0 且目录完整")
	}
}

// 清单本身不可信时一律不落盘：路径穿越、坏 JSON、不安全版本号。
func TestUIMirrorRejectsHostileManifest(t *testing.T) {
	cases := []struct {
		name     string
		manifest func() []byte
		wantErr  string
	}{
		{
			name: "路径穿越",
			manifest: func() []byte {
				return mustMarshal(t, &UIManifest{
					BuildVersion:   "v9",
					MinServerBuild: "v1",
					Files:          []UIFile{{Path: "../../etc/passwd", Size: 1, SHA256: shaOf("x")}},
				})
			},
			wantErr: "unsafe path",
		},
		{
			name:     "坏 JSON",
			manifest: func() []byte { return []byte("{not json") },
			wantErr:  "parse ui manifest",
		},
		{
			name: "不安全版本号",
			manifest: func() []byte {
				return mustMarshal(t, &UIManifest{
					BuildVersion:   "../escape",
					MinServerBuild: "v1",
					Files:          []UIFile{{Path: "index.html", Size: 1, SHA256: shaOf("x")}},
				})
			},
			wantErr: "unsafe ui build version",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newUIPrimary(t, uiFiles, "v1.0.0", "v1.0.0")
			p.manifestOverride = tc.manifest
			dir := filepath.Join(t.TempDir(), "ui")
			m := p.mirror(t, dir, "v1.0.0")
			err := m.SyncOnce(context.Background())
			if err == nil {
				t.Fatal("敌意清单必须被拒")
			}
			if tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("错误应包含 %q，实际 %v", tc.wantErr, err)
			}
			if m.CurrentDir() != "" {
				t.Fatal("失败时不该激活任何版本")
			}
			// 目录里不能出现被穿越写出去的东西。
			if _, statErr := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); !os.IsNotExist(statErr) {
				t.Fatal("版本号里的 ../ 不能被当成目录名")
			}
		})
	}
}

// 清单合法但包里塞了清单之外的文件 / 符号链接 / 少了文件：一律拒。
func TestUnpackUIBundleRejectsUnexpectedContent(t *testing.T) {
	base := func() *UIManifest {
		return &UIManifest{
			BuildVersion:   "v1",
			MinServerBuild: "v1",
			Files: []UIFile{
				{Path: "index.html", Size: 2, SHA256: shaOf("hi")},
			},
		}
	}
	pack := func(t *testing.T, entries func(tw *tar.Writer)) []byte {
		t.Helper()
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		entries(tw)
		if err := tw.Close(); err != nil {
			t.Fatalf("tar close: %v", err)
		}
		if err := gz.Close(); err != nil {
			t.Fatalf("gzip close: %v", err)
		}
		return buf.Bytes()
	}

	t.Run("清单外的文件", func(t *testing.T) {
		m := base()
		raw := pack(t, func(tw *tar.Writer) {
			writeTarEntry(t, tw, "index.html", "hi")
			writeTarEntry(t, tw, "extra.sh", "rm -rf /")
			writeTarEntry(t, tw, UIManifestName, string(mustMarshal(t, m)))
		})
		err := unpackUIBundle(bytes.NewReader(raw), t.TempDir(), m)
		if err == nil || !strings.Contains(err.Error(), "not in manifest") {
			t.Fatalf("清单外的条目必须被拒，实际 %v", err)
		}
	})

	t.Run("符号链接", func(t *testing.T) {
		m := base()
		raw := pack(t, func(tw *tar.Writer) {
			writeTarEntry(t, tw, "index.html", "hi")
			writeTarSymlink(t, tw, "link", "/etc/passwd")
			writeTarEntry(t, tw, UIManifestName, string(mustMarshal(t, m)))
		})
		err := unpackUIBundle(bytes.NewReader(raw), t.TempDir(), m)
		if err == nil || !strings.Contains(err.Error(), "unsupported type") {
			t.Fatalf("符号链接必须被拒，实际 %v", err)
		}
	})

	t.Run("少了文件", func(t *testing.T) {
		m := base()
		raw := pack(t, func(tw *tar.Writer) {
			writeTarEntry(t, tw, UIManifestName, string(mustMarshal(t, m)))
		})
		err := unpackUIBundle(bytes.NewReader(raw), t.TempDir(), m)
		if err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("缺文件必须被拒，实际 %v", err)
		}
	})

	t.Run("包里的清单与下载的不一致", func(t *testing.T) {
		m := base()
		other := &UIManifest{BuildVersion: "v2", MinServerBuild: "v1", Files: m.Files}
		raw := pack(t, func(tw *tar.Writer) {
			writeTarEntry(t, tw, "index.html", "hi")
			writeTarEntry(t, tw, UIManifestName, string(mustMarshal(t, other)))
		})
		err := unpackUIBundle(bytes.NewReader(raw), t.TempDir(), m)
		if err == nil || !strings.Contains(err.Error(), "manifest does not match") {
			t.Fatalf("两次请求之间产物被换过必须被拒，实际 %v", err)
		}
	})

	t.Run("路径穿越", func(t *testing.T) {
		m := &UIManifest{BuildVersion: "v1", MinServerBuild: "v1", Files: []UIFile{{Path: "index.html", Size: 2, SHA256: shaOf("hi")}}}
		raw := pack(t, func(tw *tar.Writer) {
			writeTarEntry(t, tw, "index.html", "hi")
			writeTarEntry(t, tw, "../../evil", "x")
			writeTarEntry(t, tw, UIManifestName, string(mustMarshal(t, m)))
		})
		err := unpackUIBundle(bytes.NewReader(raw), t.TempDir(), m)
		if err == nil || !strings.Contains(err.Error(), "unsafe path") {
			t.Fatalf("包内路径穿越必须被拒，实际 %v", err)
		}
	})

	t.Run("正常包能解开", func(t *testing.T) {
		m := base()
		raw := pack(t, func(tw *tar.Writer) {
			writeTarEntry(t, tw, "index.html", "hi")
			writeTarEntry(t, tw, UIManifestName, string(mustMarshal(t, m)))
		})
		dest := t.TempDir()
		if err := unpackUIBundle(bytes.NewReader(raw), dest, m); err != nil {
			t.Fatalf("正常包应能解开: %v", err)
		}
		if body := string(mustRead(t, filepath.Join(dest, "index.html"))); body != "hi" {
			t.Fatalf("内容 = %q", body)
		}
	})
}

// primary_url 必须是 https：这条通道搬的是整套前端产物，明文一律拒绝启动。
func TestNewUIMirrorRequiresHTTPS(t *testing.T) {
	if _, err := NewUIMirror(UIMirrorOptions{Dir: t.TempDir(), PrimaryURL: "http://primary.example", Token: "tok"}); err == nil {
		t.Fatal("http 必须被拒")
	}
	if _, err := NewUIMirror(UIMirrorOptions{Dir: t.TempDir(), PrimaryURL: "https://primary.example"}); err != nil {
		t.Fatalf("https 应通过: %v", err)
	}
	if _, err := NewUIMirror(UIMirrorOptions{PrimaryURL: "https://primary.example"}); err == nil {
		t.Fatal("缺目录必须被拒")
	}
}

func TestValidUIVersionName(t *testing.T) {
	good := []string{"v1.0.0", "v0.5.5-1201-70-gc8069963", "20261122_1200", "a"}
	for _, v := range good {
		if !validUIVersionName(v) {
			t.Fatalf("%q 应合法", v)
		}
	}
	bad := []string{"", ".", "..", "../x", "a/b", `a\b`, "a b", strings.Repeat("x", 65), "v1:2"}
	for _, v := range bad {
		if validUIVersionName(v) {
			t.Fatalf("%q 必须被拒", v)
		}
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return raw
}

func mustMarshal(t *testing.T, m *UIManifest) []byte {
	t.Helper()
	raw, err := MarshalUIManifest(m)
	if err != nil {
		t.Fatalf("MarshalUIManifest: %v", err)
	}
	return raw
}

func writeTarEntry(t *testing.T, tw *tar.Writer, name, body string) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Mode:     0o644,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
		Format:   tar.FormatPAX,
	}); err != nil {
		t.Fatalf("tar header %q: %v", name, err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatalf("tar write %q: %v", name, err)
	}
}

func writeTarSymlink(t *testing.T, tw *tar.Writer, name, target string) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{
		Name:     name,
		Linkname: target,
		Typeflag: tar.TypeSymlink,
		Format:   tar.FormatPAX,
	}); err != nil {
		t.Fatalf("tar symlink %q: %v", name, err)
	}
}
