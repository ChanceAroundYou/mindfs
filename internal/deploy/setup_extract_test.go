package deploy

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestBundle 造一个形状与 build-all.sh 产物一致的最小包。
func writeTestBundle(t *testing.T) SetupBundle {
	t.Helper()
	dir := t.TempDir()
	pkg := filepath.Join(dir, "mindfs_v0_windows_amd64")
	for _, p := range []string{"web", "web/assets"} {
		if err := os.MkdirAll(filepath.Join(pkg, p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	files := map[string]string{
		"mindfs.exe":      "MZ fake exe",
		"agents.json":     `{"agents":[]}`,
		"web/index.html":  "<html></html>",
		"web/favicon.svg": "<svg/>",
		"web/assets/a.js": "console.log(1)",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(pkg, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	ps1 := filepath.Join(dir, SetupPayloadEntryName)
	if err := os.WriteFile(ps1, []byte("# installer\n"), 0o644); err != nil {
		t.Fatalf("write ps1: %v", err)
	}
	return SetupBundle{PkgDir: pkg, PS1Path: ps1, Layout: DefaultSetupBundleLayout()}
}

// TestBuildSetupPayloadRoundTripsThroughExtractor 是载荷的唯一端到端路径：
// 打包器写什么，解包器就得读出什么。这两半分别住在 setup.go 与 archive.go，
// 中间隔着 zip 的路径规范化，任何一边单独看都是对的。
func TestBuildSetupPayloadRoundTripsThroughExtractor(t *testing.T) {
	bundle := writeTestBundle(t)
	payload, err := BuildSetupPayload(bundle)
	if err != nil {
		t.Fatalf("BuildSetupPayload: %v", err)
	}

	// 走和桩程序完全一样的路径：拼一个带 trailer 的假 exe，再切回来解。
	exe := filepath.Join(t.TempDir(), "setup.exe")
	if err := AppendSetupPayload(writeStub(t, "stub-bytes"), exe, payload); err != nil {
		t.Fatalf("AppendSetupPayload: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "staging")
	root, err := ExtractSetupPayloadToDir(exe, dst)
	if err != nil {
		t.Fatalf("ExtractSetupPayloadToDir: %v", err)
	}
	if root != dst {
		t.Fatalf("extract root = %q, want %q", root, dst)
	}
	for name, want := range map[string]string{
		"mindfs.exe":          "MZ fake exe",
		"agents.json":         `{"agents":[]}`,
		"web/index.html":      "<html></html>",
		"web/favicon.svg":     "<svg/>",
		"web/assets/a.js":     "console.log(1)",
		SetupPayloadEntryName: "# installer\n",
	} {
		got, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

// TestExtractSetupPayloadToDirRequiresSetupScript 钉住「zip 正常但少了 ps1」。
// 少了它桩程序会去跑一个不存在的文件，Windows 报的是「系统找不到指定的文件」，
// 看不出是包里缺东西。
func TestExtractSetupPayloadToDirRequiresSetupScript(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("mindfs.exe")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := w.Write([]byte("exe")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	exe := filepath.Join(t.TempDir(), "setup.exe")
	if err := AppendSetupPayload(writeStub(t, "stub"), exe, buf.Bytes()); err != nil {
		t.Fatalf("AppendSetupPayload: %v", err)
	}
	_, err = ExtractSetupPayloadToDir(exe, filepath.Join(t.TempDir(), "staging"))
	if err == nil {
		t.Fatal("expected an error when setup.ps1 is missing from the payload")
	}
	if !strings.Contains(err.Error(), SetupPayloadEntryName) {
		t.Fatalf("error should name %s, got %v", SetupPayloadEntryName, err)
	}
}

// TestExtractSetupPayloadRejectsOrdinaryBinary 钉住「双击了普通 mindfs.exe」：
// 没有 trailer 魔数就必须拒绝，而不是把尾部字节当 zip 去解。
func TestExtractSetupPayloadRejectsOrdinaryBinary(t *testing.T) {
	exe := writeStub(t, strings.Repeat("not a setup", 100))
	_, err := ExtractSetupPayloadToDir(exe, filepath.Join(t.TempDir(), "staging"))
	if err == nil {
		t.Fatal("expected an error for a binary without the setup trailer")
	}
	if !strings.Contains(err.Error(), "magic") && !strings.Contains(err.Error(), "too small") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// writeStub 落一个假桩程序文件，返回路径。
func writeStub(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stub.exe")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return p
}

// TestReadSetupPayloadRejectsOversizedLength 钉上界校验：长度字段是一个正数但
// 超出文件本身。构造时要用正数 —— 用全 0xFF 会被解码成 -1，先命中 `length <= 0`
// 那条分支，测的就不是这里了（真踩过一次）。
func TestReadSetupPayloadRejectsOversizedLength(t *testing.T) {
	body := []byte("stub-and-payload-bytes")
	huge := make([]byte, SetupTrailerSize)
	binary.BigEndian.PutUint64(huge[:8], 1<<40)
	binary.BigEndian.PutUint32(huge[8:], SetupTrailerMagic)
	data := append(append([]byte{}, body...), huge...)
	if _, _, err := ReadSetupPayload(bytes.NewReader(data), int64(len(data))); err == nil {
		t.Fatal("expected an error when the declared payload length exceeds the file")
	}
}

// TestReadSetupPayloadSectionMatches 确认切出来的区间恰好是载荷，前后都不带桩程序。
func TestReadSetupPayloadSectionMatches(t *testing.T) {
	payload := []byte("payload!")
	stubPath := writeStub(t, "STUBBYTES")
	exe := filepath.Join(t.TempDir(), "setup.exe")
	if err := AppendSetupPayload(stubPath, exe, payload); err != nil {
		t.Fatalf("AppendSetupPayload: %v", err)
	}
	f, err := os.Open(exe)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	section, n, err := ReadSetupPayload(f, info.Size())
	if err != nil {
		t.Fatalf("ReadSetupPayload: %v", err)
	}
	if n != int64(len(payload)) {
		t.Fatalf("payload size = %d, want %d", n, len(payload))
	}
	got, err := io.ReadAll(io.NewSectionReader(section, 0, n))
	if err != nil {
		t.Fatalf("read section: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
}
