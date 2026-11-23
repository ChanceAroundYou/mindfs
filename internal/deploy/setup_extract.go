package deploy

import (
	"fmt"
	"os"
	"path/filepath"
)

// SetupStagingDirName 是解包用的临时目录名（在系统临时目录下）。
const SetupStagingDirName = "mindfs-setup"

// ExtractSetupPayloadToDir 从自解包 exe 里把载荷解到 dst，返回解包根目录。
//
// 这是安装器桩程序（scripts/windows-setup-stub）唯一依赖的逻辑，抽在这里是为了
// 能在 Linux 上端到端验：桩程序本身是 windows-only、只能在真机上跑，而这几步
// （切载荷区间 → 解 zip → 目录里有什么）恰恰是最容易错、且错了最没有线索的地方。
//
// 载荷里的相对路径就是 build-all.sh 产物目录的结构（mindfs.exe / web/ / agents.json
// / setup.ps1），setup.ps1 按这套相对路径去拷。
func ExtractSetupPayloadToDir(exePath, dst string) (string, error) {
	f, err := os.Open(exePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	section, n, err := ReadSetupPayload(f, info.Size())
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}
	if err := ExtractZipReader(section, n, dst); err != nil {
		return "", err
	}
	// 解包完必须能看见安装脚本：zip 正常但少了它，桩程序接下来会去跑一个
	// 不存在的文件，Windows 上报的是「系统找不到指定的文件」，看不出是包里缺东西。
	if _, err := os.Stat(filepath.Join(dst, SetupPayloadEntryName)); err != nil {
		return "", fmt.Errorf("payload is missing %s: %w", SetupPayloadEntryName, err)
	}
	return dst, nil
}
