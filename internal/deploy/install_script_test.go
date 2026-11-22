package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 这两个发布脚本在 Linux 上只能间接验证：install.ps1 需要 pwsh 与 Windows 目标机
// （本机没有），build-all.sh 的完整打包要交叉编译七个平台。所以这里钉两件守得住的事：
// 一是 build-all.sh 缺前端时**真的**在打包前失败（能跑，就真跑），
// 二是两个脚本里那些「装完打不开页面」的关键结构没有在后续改动里被削掉（源码契约）。

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Join("..", "..", rel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// TestBuildAllRefusesPackageWithoutWebAssets 真的跑一遍脚本：布局里没有
// web/dist/index.html 时必须**在编译之前**退出，而不是先编七个平台再说。
func TestBuildAllRefusesPackageWithoutWebAssets(t *testing.T) {
	script := readRepoFile(t, "scripts/build-all.sh")

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "web"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "scripts", "build-all.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", path, "v0.0.0-test", "dist")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("build-all.sh succeeded without web/dist, output:\n%s", out)
	}
	if _, ok := err.(*exec.ExitError); !ok {
		t.Skipf("bash 不可用，跳过执行验证: %v", err)
	}
	if !strings.Contains(string(out), "build-web") {
		t.Fatalf("失败信息没说清怎么修（应指向 make build-web）:\n%s", out)
	}
	if strings.Contains(string(out), "==> Building mindfs") {
		t.Fatalf("失败发生在开始编译之后：应当在打包前就退出\n%s", out)
	}
}

// TestBuildAllStampsFrontendBuildVersion 发布包里的 web/BUILD_VERSION 是边缘对账
// 「装进去的这一版是谁」的依据。没有它，T20 的版本切换只能靠猜首页内容。
func TestBuildAllStampsFrontendBuildVersion(t *testing.T) {
	script := readRepoFile(t, "scripts/build-all.sh")
	if !strings.Contains(script, `printf '%s\n' "${VERSION}" > "${ROOT}/${OUT_DIR}/web/BUILD_VERSION"`) {
		t.Fatal("build-all.sh 没有把构建版本写进包的 web/BUILD_VERSION")
	}
	// 位置必须紧跟拷贝：写在别的目录或写死版本号都等于没写。
	if !strings.Contains(script, `cp -r "${ROOT}/web/dist" "${ROOT}/${OUT_DIR}/web"`) {
		t.Fatal("build-all.sh 不再把 web/dist 拷进发布包")
	}
}

// TestWindowsInstallerVerifiesWebAssets 钉 install.ps1 的结构：前端是必需项，
// 拷贝之后逐项核对首页真的落地，缺一个就整体失败。
//
// 它挡住的是「二进制装好了、页面 404」这类半套安装 —— 在 control 节点上那等于整机不可用，
// 而安装器自己会打印 Done，用户没有任何线索。
func TestWindowsInstallerVerifiesWebAssets(t *testing.T) {
	script := readRepoFile(t, "scripts/install.ps1")

	webSrc := `$WebSrc = Join-Path $PkgDir "web"`
	copyTo := `Copy-Item -Recurse $WebSrc $WebDest`
	probe := `if (-not (Test-Path $Probe -PathType Leaf)) {`
	done := `Write-Host "Done. mindfs installed to $BinDir\mindfs.exe"`

	srcAt := strings.Index(script, webSrc)
	copyAt := strings.Index(script, copyTo)
	probeAt := strings.Index(script, probe)
	doneAt := strings.Index(script, done)
	if srcAt < 0 || copyAt < 0 || probeAt < 0 || doneAt < 0 {
		t.Fatalf("install.ps1 缺少前端安装/校验结构 (src=%d copy=%d probe=%d done=%d)", srcAt, copyAt, probeAt, doneAt)
	}
	// 顺序：先定位包里的 web，再拷贝，再校验，最后才允许打印 Done。
	if !(srcAt < copyAt && copyAt < probeAt && probeAt < doneAt) {
		t.Fatalf("校验顺序不对：必须在打印 Done 之前核对首页 (src=%d copy=%d probe=%d done=%d)", srcAt, copyAt, probeAt, doneAt)
	}
	// 两项都必须核对：index.html 是入口，favicon.svg 用来区分「拷了目录」与「拷了内容」。
	loop := script[copyAt:probeAt]
	for _, asset := range []string{`"index.html"`, `"favicon.svg"`} {
		if !strings.Contains(loop, asset) {
			t.Fatalf("安装器没有核对 %s", asset)
		}
	}
	// 包里根本没有 web 时也必须失败，而不是跳过整段当作「可选资源」。
	if !strings.Contains(script[srcAt:copyAt], "Web assets missing from archive") {
		t.Fatal("包里缺 web 时安装器没有失败")
	}
	// 校验失败必须整体退出：只打一行 Write-Error 然后继续装完，等于没校验。
	// 两条路径各要一次 —— 包里没有 web（scoped 到拷贝之前）与拷完核对不到（拷贝之后）。
	if strings.Count(script[srcAt:doneAt], "exit 1") < 2 {
		t.Fatal("前端缺失/校验失败时安装器没有 exit 1")
	}
}

// TestWindowsInstallerStaysASCIISafe 给 install.ps1 钉纯 ASCII 之外的底线：
// 这段脚本在 Windows 上会被按本地代码页（GBK）解码，中文注释会吃掉后面的换行，
// 把下一条语句并进注释里 —— 不报错、不生效，只在运行期表现为「某步被跳过了」。
func TestWindowsInstallerStaysASCIISafe(t *testing.T) {
	script := readRepoFile(t, "scripts/install.ps1")
	for i, line := range strings.Split(script, "\n") {
		for _, r := range line {
			if r >= 0x2E80 && r <= 0x9FFF || r >= 0xFF00 && r <= 0xFFEF {
				t.Fatalf("install.ps1:%d 含 CJK 字符 %q：Windows 按 GBK 解码会吃掉换行", i+1, r)
			}
		}
	}
}
