package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"mindfs/internal/deploy"
)

// Windows 上「装过」的节点用安装包更新，而不是换 zip 里的二进制。
//
// 原因不是偏好，是计划任务的 job 语义：任务实例结束时，它拉起的子孙进程一起被终结。
// 服务自己下载 zip、起一个 PowerShell 去「等旧进程退出 → 覆盖文件 → 启动新进程」时，
// 那个 PowerShell 是服务的子进程，服务一退出它就死，新进程也就起不来 —— 表现是
// 「更新完成，服务没了」，而且没有任何日志。安装包由**另一个任务实例**（mindfs-update）
// 执行，不在服务那个 job 里，所以能活到把服务重新拉起来。
//
// 顺带的好处：卸载器、开始菜单快捷方式、注册表里的版本号都跟着更新，不会出现
// 「程序已经是新版，卸载器还是旧版」的漂移。

// runUpdateTask 是测试接缝：默认走真实的 schtasks。
var runUpdateTask = platformRunUpdateTask

// runtimeGOOS 同样是测试接缝：产物名按运行平台决定，Linux 上也要能验 Windows 分支。
var runtimeGOOS = runtime.GOOS

// updateAssetSuffix 决定这台机器该下哪种产物。
//
// 只有 Windows + 安装布局才用 setup.exe：portable 目录（开发机、直接跑二进制）
// 没有安装树，安装包会把文件铺到 %LOCALAPPDATA%\Programs 去，反而把原目录留在那儿。
func (s *Service) updateAssetSuffix() string {
	if runtimeGOOS != "windows" {
		return ".tar.gz"
	}
	if layout, err := s.installLayout(); err == nil && layout.Mode == "installed" {
		return "-setup.exe"
	}
	return ".zip"
}

func isInstallerAsset(name string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(name)), "-setup.exe")
}

// stageInstallerUpdate 下载→验签→落到固定路径→让更新任务去跑。
//
// 落点固定（见 deploy.SetupUpdateStagingPath）是因为任务 XML 在安装时就把路径写死了；
// 每次更新只是覆盖同一个文件。
func (s *Service) stageInstallerUpdate(ctx context.Context, asset releaseAsset, artifact manifestArtifact) error {
	layout, err := s.installLayout()
	if err != nil {
		return err
	}
	if layout.Mode != "installed" {
		return fmt.Errorf("安装包更新需要安装布局，当前是 %s", layout.Mode)
	}
	staging := deploy.SetupUpdateStagingPath(layout.Prefix)
	if err := os.MkdirAll(filepath.Dir(staging), 0o755); err != nil {
		return err
	}

	part := staging + ".part"
	_ = os.Remove(part)
	if err := s.downloadReleaseAsset(ctx, asset, part); err != nil {
		_ = os.Remove(part)
		return err
	}
	if err := verifyFileSHA256(part, artifact.SHA256, artifact.Size); err != nil {
		_ = os.Remove(part)
		return err
	}
	// 校验通过才替换：旧的安装包留着没用，但半个文件会被更新任务直接执行。
	if err := replaceFile(part, staging, 0o755); err != nil {
		_ = os.Remove(part)
		return err
	}
	_ = os.Remove(part)

	s.updateStatus(func(st *Status) {
		st.Status = "installing"
		st.Message = "Applying update..."
	})
	if err := runUpdateTask(); err != nil {
		// 说清楚下一步：这条路径失败时用户只有一个动作可选。
		return fmt.Errorf("无法启动安装程序：%w（可以手动运行 %s）", err, staging)
	}
	s.updateStatus(func(st *Status) {
		st.Status = "restarting"
		st.Message = "Restarting service..."
	})

	// 留一点时间把状态写回、响应发出去，然后让安装包接管：
	// 它会 schtasks /end 掉本服务、覆盖文件、再 schtasks /run 起来。
	go func() {
		delay := s.exitDelay
		if delay <= 0 {
			delay = 800 * time.Millisecond
		}
		time.Sleep(delay)
		s.exit(0)
	}()
	return nil
}
