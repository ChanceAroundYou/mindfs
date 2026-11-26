package deploy

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// Windows 边缘客户端的图标必须真的进包（Scope: G-SB）。
//
// 只靠「资源文件在仓库里」不够：Go 只链接与 GOOS/GOARCH 匹配的 .syso，安装器也要
// 显式拿到 ico 才会换掉 NSIS 默认图标。这里钉住三件事：
//   1. app.ico 是合法多尺寸图标（explorer/任务栏按尺寸挑，缺小尺寸会糊）；
//   2. 三个 .syso 都在，且机器类型与目标一致（放错架构等于没放）；
//   3. 安装器脚本把图标传给 makensis，.nsi 在拿到时定义 MUI_ICON/MUI_UNICON。

func TestWindowsAppIconHasAllSizes(t *testing.T) {
	data := []byte(readRepoFile(t, "cli/cmd/assets/app.ico"))
	if len(data) < 6 || binary.LittleEndian.Uint16(data[0:2]) != 0 || binary.LittleEndian.Uint16(data[2:4]) != 1 {
		t.Fatal("cli/cmd/assets/app.ico is not a valid ICO file")
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count < 4 || len(data) < 6+count*16 {
		t.Fatalf("ico has %d entries, want at least 4", count)
	}
	sizes := map[int]bool{}
	for i := 0; i < count; i++ {
		w := int(data[6+i*16])
		if w == 0 {
			w = 256
		}
		sizes[w] = true
	}
	for _, want := range []int{16, 32, 48, 256} {
		if !sizes[want] {
			t.Fatalf("app.ico missing %dx%d (sizes=%v)", want, want, sizes)
		}
	}
}

func TestWindowsResourceObjectsMatchTargets(t *testing.T) {
	cases := []struct {
		path string
		arch uint16
	}{
		{"cli/cmd/rsrc_windows_amd64.syso", 0x8664},
		{"cli/cmd/rsrc_windows_arm64.syso", 0xaa64},
		{"server/cmd/mindfs-setup-stub/rsrc_windows_amd64.syso", 0x8664},
	}
	for _, tc := range cases {
		data := []byte(readRepoFile(t, tc.path))
		if len(data) < 20 {
			t.Fatalf("%s is empty", tc.path)
		}
		if got := binary.LittleEndian.Uint16(data[0:2]); got != tc.arch {
			t.Fatalf("%s machine = %#x, want %#x (Go 只链接匹配架构的 .syso)", tc.path, got, tc.arch)
		}
		if !bytes.Contains(data, []byte(".rsrc")) {
			t.Fatalf("%s has no .rsrc section", tc.path)
		}
	}
}

func TestWindowsInstallerUsesAppIcon(t *testing.T) {
	script := readRepoFile(t, "scripts/build-windows-installer.sh")
	if !strings.Contains(script, `-DAPP_ICON="${ROOT}/cli/cmd/assets/app.ico"`) {
		t.Fatal("build-windows-installer.sh must pass -DAPP_ICON=<app.ico> to makensis")
	}
	nsi := readRepoFile(t, "scripts/windows-installer/mindfs.nsi")
	guard := "!ifdef APP_ICON"
	if !strings.Contains(nsi, guard) {
		t.Fatalf("mindfs.nsi must guard on APP_ICON so manual builds without the define still work")
	}
	after := nsi[strings.Index(nsi, guard):]
	if !strings.Contains(after, `!define MUI_ICON "${APP_ICON}"`) {
		t.Fatal("mindfs.nsi must use APP_ICON as the installer icon")
	}
	if !strings.Contains(after, `!define MUI_UNICON "${APP_ICON}"`) {
		t.Fatal("mindfs.nsi must use APP_ICON as the uninstaller icon")
	}
}

// 自解压安装器由 mindfs-setup-stub 编出来：那个包的 .syso 就是它的图标来源。
func TestWindowsSetupStubBuildsFromIconPackage(t *testing.T) {
	script := readRepoFile(t, "scripts/build-windows-setup.sh")
	if !strings.Contains(script, "./server/cmd/mindfs-setup-stub") {
		t.Fatal("build-windows-setup.sh must build the stub from the package that carries the icon .syso")
	}
}