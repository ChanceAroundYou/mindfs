package controlsync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 写一个最小的「前端产物」目录。清单是安全相关产物，用例必须用真实文件跑哈希，
// 不能用常量 sha 糊过去 —— 那样「哈希算错了」和「哈希没算」都测不出来。
func writeUIFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func shaOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestBuildUIManifestRecordsEveryArtifact(t *testing.T) {
	dir := writeUIFixture(t, map[string]string{
		"index.html":        "<html>hi</html>",
		"assets/index-a.js": "console.log(1)",
		"favicon.svg":       "<svg/>",
	})

	m, err := BuildUIManifest(dir, "v1.2.3", "")
	if err != nil {
		t.Fatalf("BuildUIManifest: %v", err)
	}
	if m.BuildVersion != "v1.2.3" || m.MinServerBuild != "v1.2.3" {
		t.Fatalf("版本没写对: %+v", m)
	}
	if len(m.Files) != 3 {
		t.Fatalf("文件数 = %d，应为 3: %+v", len(m.Files), m.Files)
	}
	// 顺序必须稳定：清单是拿去校验包的，两次生成的顺序不同会让「清单变了」和
	// 「产物变了」混在一起。
	if m.Files[0].Path != "assets/index-a.js" || m.Files[1].Path != "favicon.svg" || m.Files[2].Path != "index.html" {
		t.Fatalf("清单顺序应为字典序: %+v", m.Files)
	}
	for _, f := range m.Files {
		switch f.Path {
		case "index.html":
			if f.SHA256 != shaOf("<html>hi</html>") || f.Size != int64(len("<html>hi</html>")) {
				t.Fatalf("index.html 的大小/哈希不对: %+v", f)
			}
		}
	}

	// 清单本身能落盘再读回，且解析后仍然合法。
	raw, err := MarshalUIManifest(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, UIManifestName), raw, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	back, err := LoadUIManifest(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(back.Files) != 3 || back.MinServerBuild != "v1.2.3" {
		t.Fatalf("读回不一致: %+v", back)
	}
}

func TestBuildUIManifestRejectsUnusableBuilds(t *testing.T) {
	t.Run("没有 index.html", func(t *testing.T) {
		dir := writeUIFixture(t, map[string]string{"assets/a.js": "x"})
		if _, err := BuildUIManifest(dir, "v1", ""); err == nil {
			t.Fatal("没有 index.html 的构建必须被拒 —— 激活后就是白页")
		}
	})
	t.Run("空目录", func(t *testing.T) {
		if _, err := BuildUIManifest(t.TempDir(), "v1", ""); err == nil {
			t.Fatal("空目录必须被拒")
		}
	})
	t.Run("没有版本号", func(t *testing.T) {
		dir := writeUIFixture(t, map[string]string{"index.html": "x"})
		if _, err := BuildUIManifest(dir, "  ", ""); err == nil {
			t.Fatal("没有构建版本必须被拒：边缘靠它判断要不要切")
		}
	})
	t.Run("符号链接", func(t *testing.T) {
		dir := writeUIFixture(t, map[string]string{"index.html": "x"})
		if err := os.Symlink(filepath.Join(dir, "index.html"), filepath.Join(dir, "link.html")); err != nil {
			t.Skipf("本机不支持符号链接: %v", err)
		}
		if _, err := BuildUIManifest(dir, "v1", ""); err == nil {
			t.Fatal("符号链接必须被拒：它在 tar 里会变成指向目标的另一份内容")
		}
	})
}

// 清单不能把自己列进去：自哈希是不动点。这条只在「目录里已经有清单」时才可观测，
// 而这正是重复构建（`make build-web` 跑第二遍）与手工重算清单时的实际形状。
func TestBuildUIManifestNeverListsItself(t *testing.T) {
	dir := writeUIFixture(t, map[string]string{"index.html": "x"})
	if err := os.WriteFile(filepath.Join(dir, UIManifestName), []byte(`{"stale":true}`), 0o644); err != nil {
		t.Fatalf("write stale manifest: %v", err)
	}
	m, err := BuildUIManifest(dir, "v1", "")
	if err != nil {
		t.Fatalf("BuildUIManifest: %v", err)
	}
	for _, f := range m.Files {
		if f.Path == UIManifestName {
			t.Fatal("清单把自己列进了 files —— 那条哈希永远对不上")
		}
	}
}

func TestValidUIFilePathBoundaries(t *testing.T) {
	good := []string{"index.html", "assets/index-a.js", "a/b/c.png", ".well-known/x"}
	for _, p := range good {
		if !ValidUIFilePath(p) {
			t.Fatalf("%q 应合法", p)
		}
	}
	bad := []string{
		"", "/etc/passwd", "../evil", "a/../../evil", "a/../b", "./a", "a/", "a//b",
		"a\\b", "C:/x", "a\x00b", "..", ".",
	}
	for _, p := range bad {
		if ValidUIFilePath(p) {
			t.Fatalf("%q 必须被拒（路径穿越 / 跨平台歧义）", p)
		}
	}
}

func TestUIManifestValidateRejectsUnsafeOrInconsistentEntries(t *testing.T) {
	base := func() *UIManifest {
		return &UIManifest{
			BuildVersion:   "v1",
			MinServerBuild: "v1",
			Files:          []UIFile{{Path: "index.html", Size: 1, SHA256: shaOf("x")}},
		}
	}
	cases := []struct {
		name   string
		mutate func(*UIManifest)
	}{
		{"空版本", func(m *UIManifest) { m.BuildVersion = "" }},
		{"空 min_server_build", func(m *UIManifest) { m.MinServerBuild = "" }},
		{"没有文件", func(m *UIManifest) { m.Files = nil }},
		{"路径穿越", func(m *UIManifest) { m.Files[0].Path = "../evil" }},
		{"重复路径", func(m *UIManifest) { m.Files = append(m.Files, m.Files[0]) }},
		{"坏哈希", func(m *UIManifest) { m.Files[0].SHA256 = "zz" }},
		{"大写哈希", func(m *UIManifest) { m.Files[0].SHA256 = strings.ToUpper(m.Files[0].SHA256) }},
		{"负大小", func(m *UIManifest) { m.Files[0].Size = -1 }},
		{"缺 index.html", func(m *UIManifest) { m.Files[0].Path = "other.html" }},
		{"超过 200 MB", func(m *UIManifest) { m.Files[0].Size = UIMaxBundleBytes + 1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base()
			tc.mutate(m)
			if err := m.Validate(); err == nil {
				t.Fatalf("%s 必须被 Validate 拒绝", tc.name)
			}
		})
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("基线清单应当合法: %v", err)
	}
}

// 包的内容由清单决定：清单里没有的文件不会被打进去，清单里的文件一个不能少。
func TestWriteUIBundleFollowsManifest(t *testing.T) {
	dir := writeUIFixture(t, map[string]string{
		"index.html":  "<html></html>",
		"assets/a.js": "aaa",
		"extra.txt":   "not in manifest",
	})
	m, err := BuildUIManifest(dir, "v9", "")
	if err != nil {
		t.Fatalf("BuildUIManifest: %v", err)
	}
	// 手工从清单里摘掉一个文件：包必须跟着少，而不是把目录整个塞进去。
	var trimmed []UIFile
	for _, f := range m.Files {
		if f.Path != "assets/a.js" {
			trimmed = append(trimmed, f)
		}
	}
	m.Files = trimmed

	var buf bytes.Buffer
	if err := WriteUIBundle(&buf, dir, m); err != nil {
		t.Fatalf("WriteUIBundle: %v", err)
	}
	got := readTarGz(t, buf.Bytes())
	if len(got) != 2 {
		t.Fatalf("包内文件数 = %d，应为 2: %v", len(got), keys(got))
	}
	if string(got["index.html"]) != "<html></html>" {
		t.Fatalf("index.html 内容不对: %q", got["index.html"])
	}
	if _, ok := got["assets/a.js"]; ok {
		t.Fatal("清单外的文件不该被打进包里 —— 那会让「包里有、清单没列」成为可能")
	}
	// 目录里的其它文件在清单里（BuildUIManifest 会把产物全收进来），所以照常打包；
	// 「不该打包」只由清单独占决定，不由目录内容决定。
	if string(got["extra.txt"]) != "not in manifest" {
		t.Fatalf("清单里列出的文件必须原样进包: %v", keys(got))
	}
}

// 生成清单之后目录被改过（部署换了一半）：包必须失败，而不是发一份注定校验不过的资源。
func TestWriteUIBundleDetectsDriftSinceManifest(t *testing.T) {
	dir := writeUIFixture(t, map[string]string{"index.html": "original"})
	m, err := BuildUIManifest(dir, "v1", "")
	if err != nil {
		t.Fatalf("BuildUIManifest: %v", err)
	}
	// 等长改写：只靠 CopyN 是发现不了的（长度对得上），必须靠哈希。
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("originaL"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	var buf bytes.Buffer
	if err := WriteUIBundle(&buf, dir, m); err == nil {
		t.Fatal("产物在生成清单后变了，打包必须报错")
	}
}

func TestWriteUIBundleRejectsUnsafeManifest(t *testing.T) {
	dir := writeUIFixture(t, map[string]string{"index.html": "x"})
	m := &UIManifest{
		BuildVersion:   "v1",
		MinServerBuild: "v1",
		Files:          []UIFile{{Path: "../outside.txt", Size: 1, SHA256: shaOf("x")}},
	}
	var buf bytes.Buffer
	if err := WriteUIBundle(&buf, dir, m); err == nil {
		t.Fatal("清单里的路径穿越必须在打包前被拒")
	}
}

func TestLoadUIManifestErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadUIManifest(dir); !os.IsNotExist(err) {
		t.Fatalf("缺清单应回 ErrNotExist，实际 %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, UIManifestName), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadUIManifest(dir); err == nil {
		t.Fatal("坏 JSON 必须报错")
	}
	// 结构合法但内容不安全（远端/陈旧清单可能长这样）也必须拒。
	evil, _ := json.Marshal(&UIManifest{
		BuildVersion:   "v1",
		MinServerBuild: "v1",
		Files:          []UIFile{{Path: "/etc/passwd", Size: 1, SHA256: shaOf("x")}},
	})
	if err := os.WriteFile(filepath.Join(dir, UIManifestName), evil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadUIManifest(dir); err == nil {
		t.Fatal("绝对路径必须被拒")
	}
}

func readTarGz(t *testing.T, raw []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			t.Fatalf("包内只应有普通文件，实际 %q type=%d", hdr.Name, hdr.Typeflag)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("tar read %q: %v", hdr.Name, err)
		}
		out[hdr.Name] = body
	}
	return out
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
