package deploy

import (
	"path/filepath"
	"strings"
	"testing"
)

// NSIS 安装器在 Linux 上跑不起来（没有 Windows），所以这里钉结构。
//
// 每一条都对应「装出来才发现」的失败形态：
//   - 没有卸载器 / 没有 ARP 条目 → 用户不知道怎么删（这是用户原话）；
//   - RequestExecutionLevel admin → 自动更新的 /S 每次弹 UAC，等于没有自动更新；
//   - 用 taskkill 停服务 → 受限令牌下拒绝访问，安装中止（2026-11-25 根因）；
//   - 节点名页放在拷贝之前 → 查重要把 exe 再嵌一份，安装包翻倍。

func readInstallerScript(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, filepath.Join("scripts", "windows-installer", "mindfs.nsi"))
}

func TestWindowsInstallerIsAStandardWizard(t *testing.T) {
	script := readInstallerScript(t)
	for _, page := range []string{
		"MUI_PAGE_WELCOME",
		"MUI_PAGE_DIRECTORY",
		"MUI_PAGE_INSTFILES",
		"MUI_PAGE_FINISH",
	} {
		if !strings.Contains(script, page) {
			t.Fatalf("安装器缺少标准向导页 %s", page)
		}
	}
	if !strings.Contains(script, "Unicode true") {
		t.Fatal("安装器必须是 Unicode：中文路径与中文节点名在 ANSI 版里会乱码")
	}
	// 必须匹配指令本身：文件头注释里也写着「RequestExecutionLevel user」，
	// 用 strings.Contains 会被注释蒙过。
	if !strings.Contains(script, "\nRequestExecutionLevel user\n") {
		t.Fatal("安装器必须 per-user：整包提权会让自动更新的 /S 每次都弹 UAC")
	}
}

func TestWindowsInstallerShipsAnUninstaller(t *testing.T) {
	script := readInstallerScript(t)
	for _, needle := range []string{
		"WriteUninstaller \"$INSTDIR\\uninstall.exe\"",
		"UninstPage custom",
		"MUI_UNPAGE_INSTFILES",
		`HKCU "${UNINST_KEY}" "DisplayName"`,
		`HKCU "${UNINST_KEY}" "UninstallString"`,
		`HKCU "${UNINST_KEY}" "QuietUninstallString"`,
		"Section \"Uninstall\"",
	} {
		if !strings.Contains(script, needle) {
			t.Fatalf("安装器缺少卸载契约：%q", needle)
		}
	}
	// 数据删除必须是显式勾选，不能默认连会话库一起删掉。
	if !strings.Contains(script, "同时删除本机数据") {
		t.Fatal("卸载器没有提供「同时删除本机数据」的独立选项")
	}
	if !strings.Contains(script, "$DeleteUserData == ${BST_CHECKED}") {
		t.Fatal("卸载器删除数据前没有判断勾选状态")
	}
}

// 停服务只能走 schtasks /end：它由 Task Scheduler 服务执行，不受跨会话 ACL 限制。
// 但桌面壳不归计划任务管，所以还要按进程名兜底 —— 否则覆盖 bin\mindfs.exe 会「拒绝访问」。
func TestWindowsInstallerStopsThroughScheduler(t *testing.T) {
	script := readInstallerScript(t)
	body := functionBody(t, script, "StopRunningInstance")
	endAt := strings.Index(body, "schtasks /end /tn \"${TASK_NAME}\"")
	if endAt < 0 {
		t.Fatal("安装器没有用 schtasks /end 停服务")
	}
	killAt := strings.Index(body, "taskkill /f /im ${APP_EXE}")
	if killAt < 0 {
		t.Fatal("安装器缺少按进程名的兜底停止：桌面壳一直占着 exe")
	}
	if killAt < endAt {
		t.Fatal("必须先 schtasks /end，再按进程名兜底")
	}
	// 兜底必须无条件执行：旧版自解压安装没有计划任务，新版则可能留下桌面壳。
	closeIdx := strings.Index(body, "${EndIf}")
	if closeIdx < 0 || killAt < closeIdx {
		t.Fatal("taskkill 必须在计划任务查询分支之外无条件执行：桌面壳场景会漏掉")
	}
	if strings.Contains(body, "${Else}") {
		t.Fatal("停止流程不该再有 Else 分支：桌面壳与旧版安装都要兜底")
	}
}

// 服务任务是 IgnoreNew：/run 一次不代表起来了。安装器必须等到进程真的出现。
func TestWindowsInstallerWaitsForServiceToStart(t *testing.T) {
	script := readInstallerScript(t)
	body := functionBody(t, script, "StartServiceTask")
	for _, needle := range []string{
		"schtasks /run /tn \"${TASK_NAME}\"",
		"tasklist /fi \"imagename eq ${APP_EXE}\"",
		"Goto start_retry",
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("StartServiceTask 缺少 %q：旧实例还没结束时 /run 会被静默吞掉", needle)
		}
	}
}

// 自动更新由旁路任务执行，安装器必须保证它存在（首次安装时一起注册）。
func TestWindowsInstallerRegistersUpdateTask(t *testing.T) {
	script := readInstallerScript(t)
	if !strings.Contains(script, `!define UPDATE_TASK_NAME "mindfs-update"`) {
		t.Fatal("安装器没有定义更新任务名")
	}
	body := functionBody(t, script, "EnsureServiceTask")
	if !strings.Contains(body, "schtasks /query /tn \"${UPDATE_TASK_NAME}\"") {
		t.Fatal("缺少更新任务时不会补注册：自动更新会在 schtasks /run 那步失败")
	}
	// 静默模式不能卡在 UAC 上：要么继续（只少了更新任务），要么明确失败。
	if !strings.Contains(body, "静默模式下无法提权注册") {
		t.Fatal("静默模式缺少更新任务时必须明确处理，不能等待一个永远不会出现的 UAC 对话框")
	}
	if !strings.Contains(body, "SetErrorLevel 1") {
		t.Fatal("连服务任务都没有时，静默模式必须失败退出而不是留下一个没人拉起服务的安装")
	}
}

func TestWindowsInstallerRegistersServiceWithElevation(t *testing.T) {
	script := readInstallerScript(t)
	if !strings.Contains(script, `ExecShell "runas"`) {
		t.Fatal("首次安装注册 S4U 任务必须自提权（受限令牌下 schtasks /create 拒绝访问）")
	}
	if !strings.Contains(script, "--register-service") {
		t.Fatal("安装器没有调用随包 exe 的 --register-service")
	}
	if !strings.Contains(script, "--unregister-service") {
		t.Fatal("卸载器没有调用随包 exe 的 --unregister-service")
	}
	// 升级路径不能每次都要管理员：任务已存在就直接跳过注册。
	// 只看 EnsureServiceTask 的函数体 —— 文件头的注释里也有 --register-service。
	body := functionBody(t, script, "EnsureServiceTask")
	query := strings.Index(body, "schtasks /query /tn \"${TASK_NAME}\"")
	reg := strings.Index(body, `ExecShell "runas"`)
	if query < 0 || reg < 0 || query > reg {
		t.Fatal("必须先查询计划任务是否存在，再决定是否提权注册")
	}
	if !strings.Contains(body[reg:], "--register-service") {
		t.Fatal("提权拉起的命令必须带 --register-service")
	}
}

// functionBody 截出 NSIS Function 到 FunctionEnd 之间的正文。
func functionBody(t *testing.T, script, name string) string {
	t.Helper()
	start := strings.Index(script, "Function "+name+"\n")
	if start < 0 {
		t.Fatalf("安装器缺少 Function %s", name)
	}
	end := strings.Index(script[start:], "\nFunctionEnd")
	if end < 0 {
		t.Fatalf("Function %s 没有 FunctionEnd", name)
	}
	return script[start : start+end]
}

func TestWindowsInstallerNodeNameComesAfterFiles(t *testing.T) {
	script := readInstallerScript(t)
	inst := strings.Index(script, "MUI_PAGE_INSTFILES")
	nodePage := strings.Index(script, "Page custom NodeNamePageShow")
	finish := strings.Index(script, "MUI_PAGE_FINISH")
	if inst < 0 || nodePage < 0 || finish < 0 {
		t.Fatal("安装器缺少节点名页面")
	}
	if !(inst < nodePage && nodePage < finish) {
		t.Fatal("节点名页面必须排在 INSTFILES 之后、FINISH 之前：查重要调用已安装的 exe")
	}
	for _, needle := range []string{"--check-node-name", "--set-node-name", "--init-config"} {
		if !strings.Contains(script, needle) {
			t.Fatalf("安装器没有调用 %s", needle)
		}
	}
	// 安装已完成，Back 会重跑安装段；页面必须禁用 Back。
	if !strings.Contains(script, "GetDlgItem $0 $HWNDPARENT 3") ||
		!strings.Contains(script, "EnableWindow $0 0") {
		t.Fatal("节点名页面没有禁用 Back")
	}
}

func TestWindowsInstallerChecksWebView2InAllScopes(t *testing.T) {
	script := readInstallerScript(t)
	if !strings.Contains(script, "${WEBVIEW2_GUID}") {
		t.Fatal("安装器没有检查 WebView2 运行时")
	}
	if got := strings.Count(script, `EdgeUpdate\Clients\${WEBVIEW2_GUID}" "pv"`); got < 3 {
		t.Fatalf("WebView2 检测覆盖 %d 处，必须覆盖 HKCU / HKLM 32 位视图（WOW6432Node）/ HKLM 64 位视图", got)
	}
	if !strings.Contains(script, "SetRegView 64") {
		t.Fatal("缺少 64 位注册表视图：64 位系统上机器级安装的运行时会被漏掉")
	}
	if !strings.Contains(script, "SetRegView 32") {
		t.Fatal("读完 64 位视图必须切回 32 位：安装器是 32 位进程，后续注册表写入都依赖默认视图")
	}
	if !strings.Contains(script, "/silent /install") {
		t.Fatal("没有静默安装内嵌的 Evergreen Bootstrapper")
	}
	// 失败时用户要能重试、打开下载页或跳过继续安装。
	for _, label := range []string{"wv2_retry:", "wv2_download:", "wv2_skip:"} {
		if !strings.Contains(script, label) {
			t.Fatalf("WebView2 失败分支缺少 %s", label)
		}
	}
	if !strings.Contains(script, "${If} ${Silent}") {
		t.Fatal("静默模式必须跳过对话框，否则自动更新会卡在无人点击的提示上")
	}
}

func TestWindowsInstallerBuildScript(t *testing.T) {
	script := readRepoFile(t, filepath.Join("scripts", "build-windows-installer.sh"))
	for _, needle := range []string{
		"for f in mindfs.exe agents.json web/index.html web/favicon.svg",
		"VERSION4=",
		"makensis",
		"MicrosoftEdgeWebView2Setup.exe",
		`"${PAYLOAD}/bin"`,
		`"${PAYLOAD}/share/mindfs"`,
	} {
		if !strings.Contains(script, needle) {
			t.Fatalf("构建脚本缺少 %q", needle)
		}
	}
	// 包不完整必须在调用 makensis 之前失败：否则发布出来的是一个
	// 「服务能跑、页面 404」的安装包。
	check := strings.Index(script, "for f in mindfs.exe agents.json")
	make := strings.Index(script, `"${MAKENSIS}" -V2`)
	if check < 0 || make < 0 || check > make {
		t.Fatal("包完整性校验必须发生在 makensis 之前")
	}
	if !strings.Contains(script, `-DOUTFILE="${OUT}"`) {
		t.Fatal("构建脚本没有把输出路径传给 makensis")
	}
}
