package deploy

import (
	"strings"
	"testing"
)

// TestDeployScriptsInjectReleaseManifestKey 钉住「构建脚本有没有把发布清单公钥编进去」。
//
// update.Service.canAutoUpdate() 读的是 -X mindfs/server/internal/update.releaseManifestPublicKey，
// 缺了它 canAutoUpdate() 恒 false，/api/app/update 永远返回「需要配置公钥」——
// 症状只有「点更新什么都不发生」，日志里也只有一行 unsupported。2026-11-23 实测
// pc-win 一直停在这个状态：deploy-pcwin.sh 的 ldflags 漏了这一行。
//
// 因此要求两条路径同源：build-all.sh 打所有平台的包，deploy-pcwin.sh 单独推 pc-win，
// 两边都必须能在 MINDFS_RELEASE_PUBLIC_KEY 非空时注入同一个变量。
func TestDeployScriptsInjectReleaseManifestKey(t *testing.T) {
	const ldName = "mindfs/server/internal/update.releaseManifestPublicKey"
	for _, script := range []string{"scripts/build-all.sh", "scripts/deploy-pcwin.sh"} {
		body := readRepoFile(t, script)
		if !strings.Contains(body, ldName) {
			t.Fatalf("%s 没有注入 %s：这条路径上的二进制自更新会静默失效", script, ldName)
		}
		// 只写在注释里也算没有：必须出现在 -ldflags 的取值里。
		if !strings.Contains(body, "-X "+ldName) {
			t.Fatalf("%s 提到了 %s 但没有以 -X 形式交给 linker", script, ldName)
		}
		// 公钥是构建期参数，不该硬编码进脚本（发布私钥/公钥轮换时会变成假绿）。
		if strings.Contains(body, `<= "`+ldName) {
			t.Fatalf("%s 里公钥被硬编码了，应当只从 MINDFS_RELEASE_PUBLIC_KEY 读", script)
		}
	}
}

// TestWindowsDeployPassesGUIAndVersionFlags 钉 pc-win 交叉编译的两个既有事实：
// Windows 上必须是 GUI 子系统（否则每个节点都挂一个黑框），版本号必须打进去
// （否则 /api/app/update 拿 "dev" 比版本，永远认为自己是最新的）。
func TestWindowsDeployPassesGUIAndVersionFlags(t *testing.T) {
	body := readRepoFile(t, "scripts/deploy-pcwin.sh")
	for _, want := range []string{"-H windowsgui", "-X main.version=$VERSION"} {
		if !strings.Contains(body, want) {
			t.Fatalf("deploy-pcwin.sh 缺少 %q", want)
		}
	}
}

// TestControlPlaneDocCoversBothSides 钉文档缺口。
//
// 2026-11-23 之前的「离线 control 安装（Windows）」只写边缘侧的 config.json，
// 照它配会在启动期被拒绝（server/app/control_sync.go 要求 primary 必须有 token）。
// 文档必须同时给出主节点与边缘两侧，并说明两边 token 是同一个值。
func TestControlPlaneDocCoversBothSides(t *testing.T) {
	doc := readRepoFile(t, "docs/multi-node-control-plane.md")
	for _, want := range []string{
		`"role": "primary"`,
		`"role": "edge"`,
		"token_file",
		"/api/control-sync/head",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("docs/multi-node-control-plane.md 缺少 %q：只写一侧会让照做的人卡在启动期", want)
		}
	}
}
