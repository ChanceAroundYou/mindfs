package deploy

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// 解包出来的东西必须与条目内容一致 —— 「解开了但内容不对」是最难查的安装事故。
func TestExtractZipReaderWritesEntryContents(t *testing.T) {
	raw := zipBytes(t, map[string]string{
		"mindfs_windows_amd64/mindfs.exe":     "binary",
		"mindfs_windows_amd64/web/index.html": "<html></html>",
	})
	dst := t.TempDir()
	if err := ExtractZipReader(bytes.NewReader(raw), int64(len(raw)), dst); err != nil {
		t.Fatalf("extract: %v", err)
	}
	for _, c := range []struct{ rel, want string }{
		{"mindfs_windows_amd64/mindfs.exe", "binary"},
		{"mindfs_windows_amd64/web/index.html", "<html></html>"},
	} {
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(c.rel)))
		if err != nil {
			t.Fatalf("read %s: %v", c.rel, err)
		}
		if string(got) != c.want {
			t.Fatalf("%s = %q, want %q", c.rel, got, c.want)
		}
	}
}

// 符号链接条目一律拒绝：它能把解包目录指到任何地方，而安装包不需要符号链接。
// 真构造一个 symlink 条目 —— 只在代码里写守卫、不造条目的用例，删掉守卫也不会红。
func TestExtractZipReaderRejectsSymlinkEntries(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "mindfs_windows_amd64/link.exe"}
	hdr.SetMode(os.ModeSymlink | 0o777)
	if _, err := w.CreateHeader(hdr); err != nil {
		t.Fatalf("create header: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	raw := buf.Bytes()
	if err := ExtractZipReader(bytes.NewReader(raw), int64(len(raw)), t.TempDir()); err == nil {
		t.Fatal("ExtractZipReader accepted a symlink entry")
	}
}

// zip-slip 的四种写法都不能落到解包目录之外。少一条就等于给「解包用户提交的压缩包」
// 开了一条任意写路径 —— 而自解包 exe 处理的正是从网上下载回来的东西。
func TestSafeArchiveTargetRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"../evil",
		"a/../../evil",
		"..",
		".",
		"",
	} {
		if _, err := SafeArchiveTarget(root, name); err == nil {
			t.Fatalf("SafeArchiveTarget(%q) allowed an escape", name)
		}
	}
	for _, name := range []string{"mindfs_windows_amd64/mindfs.exe", "web/index.html"} {
		got, err := SafeArchiveTarget(root, name)
		if err != nil {
			t.Fatalf("SafeArchiveTarget(%q): %v", name, err)
		}
		if !strings.HasPrefix(got, root) {
			t.Fatalf("SafeArchiveTarget(%q) = %s, want under %s", name, got, root)
		}
	}
}

// 绝对路径（含 Windows 盘符形态）直接拒绝：解包目录是用户装的目录，条目名不该决定位置。
func TestSafeArchiveTargetRejectsAbsoluteNames(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"/etc/passwd", "/tmp/x"} {
		if _, err := SafeArchiveTarget(root, name); err == nil {
			t.Fatalf("SafeArchiveTarget(%q) allowed an absolute path", name)
		}
	}
}
