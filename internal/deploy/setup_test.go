package deploy

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// 拼接后的布局必须是 [桩程序][载荷][长度+魔数]，且能从自己身上原样切回来。
// 三个区间任何一个差一个字节，安装器解出来就是半个 zip —— 而 zip 的错误
// 信息指向「文件损坏」，不会提到拼接顺序。
func TestSetupPayloadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	stubPath := filepath.Join(dir, "stub.exe")
	stub := []byte("MZ fake pe stub")
	if err := os.WriteFile(stubPath, stub, 0o644); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	payload := bytes.Repeat([]byte("PAYLOAD"), 100)
	dst := filepath.Join(dir, "setup.exe")
	if err := AppendSetupPayload(stubPath, dst, payload); err != nil {
		t.Fatalf("append: %v", err)
	}

	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.HasPrefix(raw, stub) {
		t.Fatal("payload does not start with the stub bytes")
	}
	section, n, err := ReadSetupPayload(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if n != int64(len(payload)) {
		t.Fatalf("payload length = %d, want %d", n, len(payload))
	}
	got := make([]byte, n)
	if _, err := section.ReadAt(got, 0); err != nil && err != io.EOF {
		t.Fatalf("read section: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q, want %q", got, payload)
	}
	// 桩程序本体不能被当载荷带走（切错了起点时最典型的症状）。
	if bytes.Contains(got, stub) {
		t.Fatal("payload accidentally contains the stub program")
	}
}

// 裸二进制、坏长度、坏魔数都必须被拒：装错东西比装不上更难排查。
func TestReadSetupPayloadRejectsBadFiles(t *testing.T) {
	plain := []byte("just a normal mindfs.exe, no trailer here")
	if _, _, err := ReadSetupPayload(bytes.NewReader(plain), int64(len(plain))); err == nil {
		t.Fatal("ReadSetupPayload accepted a plain binary")
	}
	if _, _, err := ReadSetupPayload(bytes.NewReader(nil), 0); err == nil {
		t.Fatal("ReadSetupPayload accepted an empty file")
	}

	stubPath := filepath.Join(t.TempDir(), "stub")
	if err := os.WriteFile(stubPath, []byte("STUB"), 0o644); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "setup.exe")
	if err := AppendSetupPayload(stubPath, dst, []byte("0123456789")); err != nil {
		t.Fatalf("append: %v", err)
	}
	raw, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// 把长度改成一个**正数**但超出文件大小：必须拒绝，而不是越界读。
	// （写成全 0xFF 是不行的 —— 那当 int64 是 -1，会被「长度必须为正」那条拦下，
	// 测不到上界检查本身。）
	huge := append([]byte(nil), raw...)
	binary.BigEndian.PutUint64(huge[len(huge)-SetupTrailerSize:], 1<<40)
	if _, _, err := ReadSetupPayload(bytes.NewReader(huge), int64(len(huge))); err == nil {
		t.Fatal("ReadSetupPayload accepted an out-of-range payload length")
	}
}

// 布局必须与 server/internal/update 的 installLayout 同形：
// <prefix>/bin/mindfs.exe + <prefix>/share/mindfs/{agents.json,web}。
// 装成别的形状的后果是二进制起得来、页面 404，且没有任何报错。
func TestSetupPathsMatchInstallLayout(t *testing.T) {
	got, err := SetupPathsFor(`C:\Users\u\AppData\Local`, `C:\Users\u\AppData\Roaming`)
	if err != nil {
		t.Fatalf("paths: %v", err)
	}
	sep := string(filepath.Separator)
	prefix := filepath.Join(`C:\Users\u\AppData\Local`, "Programs", "mindfs")
	want := SetupPaths{
		Prefix:     prefix,
		BinDir:     filepath.Join(prefix, "bin"),
		BinaryPath: filepath.Join(prefix, "bin", "mindfs.exe"),
		ShareDir:   filepath.Join(prefix, "share", "mindfs"),
		AgentsPath: filepath.Join(prefix, "share", "mindfs", "agents.json"),
		WebDir:     filepath.Join(prefix, "share", "mindfs", "web"),
		ConfigPath: filepath.Join(`C:\Users\u\AppData\Roaming`, "mindfs", "config.json"),
	}
	if got != want {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
	// 判据本身也要钉住：installLayout 只认「父目录名恰好是 bin」。
	if filepath.Base(filepath.Dir(got.BinaryPath)) != "bin" {
		t.Fatalf("binary parent = %q, want bin (installLayout would treat it as portable)",
			filepath.Base(filepath.Dir(got.BinaryPath)))
	}
	if !strings.Contains(got.WebDir, filepath.Join("share", "mindfs", "web")) {
		t.Fatalf("web dir = %q, want .../share/mindfs/web", got.WebDir)
	}
	for _, s := range []string{got.Prefix, got.BinDir, got.ShareDir, got.ConfigPath} {
		if strings.Contains(s, sep+sep) {
			t.Fatalf("path %q contains a doubled separator", s)
		}
	}
}

func TestSetupPathsRequireBothRoots(t *testing.T) {
	for _, c := range []struct{ local, roaming string }{
		{"", `C:\Roaming`},
		{`C:\Local`, ""},
		{"   ", `C:\Roaming`},
	} {
		if _, err := SetupPathsFor(c.local, c.roaming); err == nil {
			t.Fatalf("SetupPathsFor(%q,%q) accepted a missing root", c.local, c.roaming)
		}
	}
}

// 任务 XML 的每一个字段都是「少一项就出问题」的那一类。逐项断言，
// 因为它们的缺失症状全都不是「XML 语法错误」：
//   - 少了 S4U ⇒ 开机起不来进程（而托盘/脚本照样报成功）；
//   - 少了 ExecutionTimeLimit ⇒ 72 小时后被计划任务砍掉；
//   - IgnoreNew 写错 ⇒ 托盘重启必须等的那个前提不成立。
func TestBuildSetupTaskXMLCarriesRealMachineShape(t *testing.T) {
	xml, err := BuildSetupTaskXML(SetupTaskOptions{
		UserSID: "S-1-5-21-1-2-3-1001",
		Binary:  `C:\Users\u\AppData\Local\Programs\mindfs\bin\mindfs.exe`,
		Addr:    "0.0.0.0:7332",
	})
	if err != nil {
		t.Fatalf("xml: %v", err)
	}
	for _, want := range []string{
		`<URI>\mindfs</URI>`,
		`<UserId>S-1-5-21-1-2-3-1001</UserId>`,
		`<LogonType>S4U</LogonType>`,
		`<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>`,
		`<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>`,
		`<RestartOnFailure>`,
		`<Count>3</Count>`,
		`<Interval>PT1M</Interval>`,
		`<StartWhenAvailable>true</StartWhenAvailable>`,
		`<StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>`,
		`<BootTrigger />`,
		`<LogonTrigger />`,
		`<Command>C:\Users\u\AppData\Local\Programs\mindfs\bin\mindfs.exe</Command>`,
		`<Arguments>--internal-autostart --addr 0.0.0.0:7332</Arguments>`,
	} {
		if !strings.Contains(xml, want) {
			t.Fatalf("task XML missing %q\n%s", want, xml)
		}
	}
	if strings.Contains(xml, "\t") {
		t.Fatal("task XML uses tabs; schtasks' parser is happier with plain spaces")
	}
}

func TestBuildSetupTaskXMLRequiresInputs(t *testing.T) {
	for _, c := range []SetupTaskOptions{
		{UserSID: "", Binary: `C:\x\mindfs.exe`},
		{UserSID: "S-1-5-21", Binary: ""},
	} {
		if _, err := BuildSetupTaskXML(c); err == nil {
			t.Fatalf("BuildSetupTaskXML(%+v) accepted empty input", c)
		}
	}
	// 缺省值必须填上，否则装出来的是一个监听 0.0.0.0:7332 之外的端口的任务。
	if xml, err := BuildSetupTaskXML(SetupTaskOptions{UserSID: "S-1", Binary: `C:\b.exe`}); err != nil {
		t.Fatalf("xml: %v", err)
	} else if !strings.Contains(xml, `--addr 0.0.0.0:7332`) {
		t.Fatalf("default addr missing from %q", xml)
	}
}

// XML 里的 & / < / > 必须转义：用户名带 & 时拼出的是一份坏 XML，
// 而 schtasks 只会说「任务 XML 无效」。
func TestBuildSetupTaskXMLEscapesText(t *testing.T) {
	xml, err := BuildSetupTaskXML(SetupTaskOptions{
		UserSID: "S-1",
		Binary:  `C:\a&b\mindfs.exe`,
	})
	if err != nil {
		t.Fatalf("xml: %v", err)
	}
	if !strings.Contains(xml, `C:\a&amp;b\mindfs.exe`) {
		t.Fatalf("binary path not escaped:\n%s", xml)
	}
	if strings.Contains(xml, `a&b`) {
		t.Fatalf("raw & leaked into XML:\n%s", xml)
	}
}

// schtasks /create /xml 只吃 UTF-16LE。写成 UTF-8 时报的是「任务 XML 无效」，
// 那种错误信息不会告诉你编码错了。
func TestEncodeUTF16LEFile(t *testing.T) {
	got := EncodeUTF16LEFile("AB")
	want := []byte{0xFF, 0xFE, 'A', 0x00, 'B', 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("EncodeUTF16LEFile(AB) = % x, want % x", got, want)
	}
	// 非 BMP 字符要走代理对：计划任务名一旦被截断，schtasks 报的还是「XML 无效」。
	s := "A😀"
	units := utf16.Encode([]rune(s))
	if len(EncodeUTF16LEFile(s)) != 2+2*len(units) {
		t.Fatalf("surrogate pair not encoded: % x", EncodeUTF16LEFile(s))
	}
	if len(EncodeUTF16LEFile("")) != 2 {
		t.Fatal("empty text should still carry the BOM")
	}
}

// 用户 PATH 的合并有四条必须钉住的语义（每条都对应一次真实写坏 PATH 的方式）。
func TestMergeUserPathSemantics(t *testing.T) {
	bin := `C:\Users\u\AppData\Local\Programs\mindfs\bin`
	cases := []struct {
		name    string
		current string
		dir     string
		want    string
		added   bool
	}{
		{
			name:    "append when missing",
			current: `C:\Windows;C:\Windows\System32`,
			dir:     bin,
			want:    `C:\Windows;C:\Windows\System32;` + bin,
			added:   true,
		},
		{
			name:    "already present, different case",
			current: `C:\Windows;` + strings.ToLower(bin),
			dir:     bin,
			want:    `C:\Windows;` + strings.ToLower(bin),
			added:   false,
		},
		{
			name:    "already present with trailing separator",
			current: `C:\Windows;` + bin + `\`,
			dir:     bin,
			want:    `C:\Windows;` + bin + `\`,
			added:   false,
		},
		{
			name:    "empty PATH",
			current: "",
			dir:     bin,
			want:    bin,
			added:   true,
		},
		{
			name:    "duplicates and empty segments are dropped",
			current: `C:\Windows;;C:\Windows;`,
			dir:     bin,
			want:    `C:\Windows;` + bin,
			added:   true,
		},
		{
			name:    "empty dir is a no-op",
			current: `C:\Windows`,
			dir:     "   ",
			want:    `C:\Windows`,
			added:   false,
		},
	}
	for _, c := range cases {
		got, added := MergeUserPath(c.current, c.dir)
		if got != c.want || added != c.added {
			t.Fatalf("%s: MergeUserPath(%q,%q) = (%q,%v), want (%q,%v)",
				c.name, c.current, c.dir, got, added, c.want, c.added)
		}
	}
}

// 安装器写 config.json 的两个键，且必须原样保留用户已有的段。
// 键路径写错一个字符 = 一台谁也不跟随的孤儿边控端，而界面只会显示空的配对表单。
func TestApplySetupConfig(t *testing.T) {
	doc := map[string]any{
		"addr": "0.0.0.0:7332",
		"live_compact": map[string]any{
			"mode": "on",
		},
		"control_sync": map[string]any{
			"primary_url": "https://home.example",
		},
	}
	if err := ApplySetupConfig(doc); err != nil {
		t.Fatalf("apply: %v", err)
	}
	cs, ok := doc["control_sync"].(map[string]any)
	if !ok {
		t.Fatalf("control_sync = %#v, want an object", doc["control_sync"])
	}
	if cs["role"] != "edge" {
		t.Fatalf("control_sync.role = %#v, want edge", cs["role"])
	}
	if cs["primary_url"] != "https://home.example" {
		t.Fatalf("existing control_sync keys were dropped: %#v", cs)
	}
	up, ok := doc["update"].(map[string]any)
	if !ok {
		t.Fatalf("update = %#v, want an object", doc["update"])
	}
	if up["auto_install"] != true {
		t.Fatalf("update.auto_install = %#v, want true", up["auto_install"])
	}
	if doc["addr"] != "0.0.0.0:7332" {
		t.Fatalf("unrelated key was rewritten: %#v", doc["addr"])
	}
	if lc, ok := doc["live_compact"].(map[string]any); !ok || lc["mode"] != "on" {
		t.Fatalf("live_compact was dropped: %#v", doc["live_compact"])
	}
}

// 中间层不是对象时必须拒绝，而不是把它覆盖成对象 —— 那是用户手写的配置。
func TestApplySetupConfigRejectsNonObjectSections(t *testing.T) {
	for _, bad := range []map[string]any{
		{"control_sync": "edge"},
		{"update": []any{true}},
	} {
		if err := ApplySetupConfig(bad); err == nil {
			t.Fatalf("ApplySetupConfig(%#v) overwrote a non-object section", bad)
		}
	}
}

// 自动更新任务和服​​务任务共用 S4U/编码/转义这些坑，但语义相反：
// 它必须**没有触发器**、只跑一次、失败了不重试。每一条都对应一次真实故障形态。
func TestBuildUpdateTaskXML(t *testing.T) {
	xml, err := BuildUpdateTaskXML(UpdateTaskOptions{
		UserSID:   "S-1-5-21-1-2-3-1001",
		SetupPath: `C:\Users\u\AppData\Local\Programs\mindfs\update\mindfs-setup.exe`,
	})
	if err != nil {
		t.Fatalf("xml: %v", err)
	}
	for _, want := range []string{
		`<URI>\mindfs-update</URI>`,
		`<UserId>S-1-5-21-1-2-3-1001</UserId>`,
		`<LogonType>S4U</LogonType>`,
		`<RunLevel>LeastPrivilege</RunLevel>`,
		`<ExecutionTimeLimit>PT1H</ExecutionTimeLimit>`,
		`<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>`,
		`<Triggers />`,
		`<Command>C:\Users\u\AppData\Local\Programs\mindfs\update\mindfs-setup.exe</Command>`,
		`<Arguments>/S</Arguments>`,
	} {
		if !strings.Contains(xml, want) {
			t.Fatalf("update task XML missing %q\n%s", want, xml)
		}
	}
	// 有触发器就意味着每次开机都重跑安装包：停服务、拷文件、再起服务，一天一次。
	for _, forbidden := range []string{"BootTrigger", "LogonTrigger", "<CalendarTrigger", "RestartOnFailure"} {
		if strings.Contains(xml, forbidden) {
			t.Fatalf("update task XML must not contain %q\n%s", forbidden, xml)
		}
	}
	if strings.Contains(xml, "\t") {
		t.Fatal("task XML uses tabs; schtasks' parser is happier with plain spaces")
	}
}

func TestBuildUpdateTaskXMLRequiresInputs(t *testing.T) {
	for _, c := range []UpdateTaskOptions{
		{UserSID: "", SetupPath: `C:\u\setup.exe`},
		{UserSID: "S-1", SetupPath: "  "},
	} {
		if _, err := BuildUpdateTaskXML(c); err == nil {
			t.Fatalf("BuildUpdateTaskXML(%+v) accepted empty input", c)
		}
	}
	xml, err := BuildUpdateTaskXML(UpdateTaskOptions{UserSID: "S-1", SetupPath: `C:\a&b\setup.exe`})
	if err != nil {
		t.Fatalf("xml: %v", err)
	}
	if !strings.Contains(xml, `C:\a&amp;b\setup.exe`) {
		t.Fatalf("path is not escaped:\n%s", xml)
	}
}

func TestSetupUpdateStagingPathIsUnderPrefix(t *testing.T) {
	prefix := filepath.Join("Users", "u", "AppData", "Local", "Programs", "mindfs")
	got := SetupUpdateStagingPath(prefix)
	want := filepath.Join(prefix, "update", "mindfs-setup.exe")
	if got != want {
		t.Fatalf("staging path = %q, want %q", got, want)
	}
	// 必须在安装前缀里，而不是 %LOCALAPPDATA%\mindfs（用户数据目录：
	// 卸载勾了「删除数据」会连带清掉，而安装包属于程序）。
	if rel, err := filepath.Rel(prefix, got); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("staging path %q is not under the install prefix %q", got, prefix)
	}
}
