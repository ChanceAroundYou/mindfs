// windows-setup 生成 Windows 边控端的一键安装器（自解包 exe）。
//
// 用法：
//
//	go run ./scripts/windows-setup -version v1.2.3 -dist dist
//
// 输入：`dist/mindfs_<version>_windows_amd64/`（scripts/build-all.sh 的产物，
// 含 mindfs.exe + web/ + agents.json）。
// 输出：`dist/mindfs_<version>_windows_amd64-setup.exe`：
//
//	[ 桩程序 (scripts/windows-setup-stub) ][ 载荷 zip ][ 8 字节长度 + 4 字节魔数 ]
//
// 为什么是个 Go 工具而不是一段 bash：产物里内嵌的 `setup.ps1` 有两条硬约束 ——
//
//   - **纯 ASCII**。Windows 按本地代码页（中文机器是 GBK）解码 .ps1，CJK 注释会
//     把下一行的换行吃掉、并进注释里，脚本静默少一行却不报错
//     （internal/deploy/install_script_test.go 的 TestWindowsInstallerStaysASCIISafe 钉住）。
//   - **任务 XML 是 UTF-16LE**。`schtasks /create /xml` 只吃 UTF-16，写成 UTF-8
//     会报「任务 XML 无效」；而 XML 里的 UserId 必须是本机 SID，只能运行期求值。
//
// 这两件事在 bash 里做要靠 iconv + 手写 CRLF，在 Go 里是几行 encoding/unicode。
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mindfs/internal/deploy"
)

func main() {
	version := flag.String("version", "", "版本号（决定输入目录名与输出文件名）")
	dist := flag.String("dist", "dist", "构建产物目录")
	out := flag.String("out", "", "输出文件（缺省 <dist>/mindfs_<version>_windows_amd64-setup.exe）")
	stub := flag.String("stub", "", "桩程序路径（缺省 <dist>/mindfs-setup-stub-windows-amd64.exe）")
	flag.Parse()

	if strings.TrimSpace(*version) == "" {
		fatalf("-version is required")
	}
	if err := run(*version, *dist, *out, *stub); err != nil {
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "windows-setup: "+format+"\n", args...)
	os.Exit(1)
}

func run(version, distDir, out, stubPath string) error {
	pkgName := "mindfs_" + version + "_windows_amd64"
	pkgDir := filepath.Join(distDir, pkgName)
	bundle := deploy.SetupBundle{
		PkgDir:  pkgDir,
		PS1Path: filepath.Join("scripts", "windows-setup", deploy.SetupPayloadEntryName),
		Layout:  deploy.DefaultSetupBundleLayout(),
	}
	if strings.TrimSpace(out) == "" {
		out = filepath.Join(distDir, pkgName+"-setup.exe")
	}
	if strings.TrimSpace(stubPath) == "" {
		stubPath = filepath.Join(distDir, deploy.SetupStubName)
	}
	payload, err := deploy.BuildSetupPayload(bundle)
	if err != nil {
		return err
	}
	if err := deploy.AppendSetupPayload(stubPath, out, payload); err != nil {
		return err
	}
	fmt.Printf("windows-setup: wrote %s (%d bytes payload)\n", out, len(payload))
	return nil
}
