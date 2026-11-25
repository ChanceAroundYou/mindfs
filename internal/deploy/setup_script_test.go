package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setup.ps1 与 build-windows-setup.sh 在 Linux 上跑不了（没有 powershell，
// 安装器只能在 Windows 真机上双击）。所以这里钉两件守得住的事：
//
//   - **纯 ASCII**：Windows 按本地代码页（GBK）解码 .ps1，CJK 注释会吃掉下一行换行。
//   - **结构与取值**：每一步的顺序、以及「路径一律由桩程序传进来」这条决定，
//     都不能在后续改动里被削掉 —— 它们错了的症状全都指向别处（页面 403、
//     任务起不来、PATH 被覆盖），不指向安装器。

func readSetupScript(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, filepath.Join("scripts", "windows-setup", SetupPayloadEntryName))
}

// TestWindowsSetupScriptStaysASCIISafe 与 install.ps1 同一条判据。
func TestWindowsSetupScriptStaysASCIISafe(t *testing.T) {
	script := readSetupScript(t)
	for i, line := range strings.Split(script, "\n") {
		for _, r := range line {
			if r >= 0x2E80 && r <= 0x9FFF || r >= 0xFF00 && r <= 0xFFEF {
				t.Fatalf("setup.ps1:%d 含 CJK 字符 %q：Windows 按 GBK 解码会吃掉换行", i+1, r)
			}
		}
	}
}

// TestWindowsSetupScriptTakesPathsFromStub 钉住「所有落地路径都由桩程序算好传入」。
//
// 如果 .ps1 自己再拼一遍 %LOCALAPPDATA%\Programs\mindfs，那就是第二个实现：
// 与 internal/deploy/setup.go::SetupPathsFor 漂移之后，装出来的是台
// 「服务在跑但页面 403」的机器，而两边单独看都对。
func TestWindowsSetupScriptTakesPathsFromStub(t *testing.T) {
	script := readSetupScript(t)
	for _, param := range []string{
		"$Prefix", "$BinDir", "$Binary", "$ShareDir", "$Agents", "$Web", "$TaskXml",
	} {
		if !strings.Contains(script, "[string]"+param) {
			t.Fatalf("setup.ps1 没有接收 %s 参数：路径必须由桩程序传入", param)
		}
	}
	for _, forbidden := range []string{
		"Join-Path $local 'Programs",
		`Join-Path $local "Programs`,
		"GetEnvironmentVariable('LOCALAPPDATA'",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("setup.ps1 自己拼了安装路径（%q）：布局的唯一来源是 deploy.SetupPathsFor", forbidden)
		}
	}
}

// TestWindowsSetupScriptOrder 钉住六步顺序。每一步都依赖前一步：
// 不停旧进程就拷不了二进制（文件被占用）；不先拷就注册任务会指向不存在的文件；
// 快捷方式指向装好的 exe，所以必须排在拷贝之后。
func TestWindowsSetupScriptOrder(t *testing.T) {
	script := readSetupScript(t)
	steps := []struct{ label, needle string }{
		{"stop", "Stop-RunningInstance $Binary"},
		{"install", "Install-Payload $StageDir"},
		{"shortcut", "Install-Shortcuts $Binary"},
		{"task", "Register-ScheduledTask $TaskName"},
		{"path", "Add-UserPath $BinDir"},
		{"start", "Start-AndWait $TaskName"},
	}
	prev, prevLabel := -1, ""
	for _, s := range steps {
		at := strings.Index(script, s.needle)
		if at < 0 {
			t.Fatalf("setup.ps1 缺少 %s 步骤（%q）", s.label, s.needle)
		}
		if at < prev {
			t.Fatalf("步骤顺序不对：%s 必须在 %s 之后", s.label, prevLabel)
		}
		prev, prevLabel = at, s.label
	}
}

// TestWindowsSetupScriptInstallsStartMenuShortcut 钉住「装完能在开始菜单里找到」。
//
// 契约有三条，缺一条都会把入口还回去：
//  1. 落在 per-user 的开始菜单目录（%APPDATA%\...\Start Menu\Programs），
//     与安装前缀 %LOCALAPPDATA%\Programs 同属当前用户 —— 不需要提权；
//  2. 目标就是装好的那个 exe，参数为空（带 autostart 旗标会开成控制台）；
//  3. Save() 之后还要在盘上确认一次：COM 调用可以「成功」而没有文件。
func TestWindowsSetupScriptInstallsStartMenuShortcut(t *testing.T) {
	script := readSetupScript(t)
	at := strings.Index(script, "function Install-Shortcuts")
	if at < 0 {
		t.Fatal("setup.ps1 没有 Install-Shortcuts")
	}
	body := script[at:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	for _, needle := range []string{
		`Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs'`,
		"'MindFS.lnk'",
		// 整行匹配：`$sc.TargetPath = $Binary + ".bak"` 这种拼接收敛不了，
		// 目标必须就是装好的那个 exe 本身。
		"\n    $sc.TargetPath = $Binary\n",
		"$sc.Arguments = ''",
		"$sc.Save()",
	} {
		if !strings.Contains(body, needle) {
			t.Fatalf("Install-Shortcuts 缺少 %q", needle)
		}
	}
	// Save() 是 COM 调用，Start Menu 只读或配置目录被重定向时它会静默什么也不做。
	// 装完没有条目，用户看到的是「装好了但找不到」，脚本却照样打印 Done。
	if !strings.Contains(body, "if (-not (Test-Path $link -PathType Leaf)) { throw") {
		t.Fatal("Install-Shortcuts 没有在 Save() 之后确认 .lnk 真的落盘")
	}
	// 参数必须是空的：autostart 旗标会把快捷方式变成控制台服务。注释里也不能出现
	// 这个字面量，否则这条断言就没法分辨「写在注释里」与「写在 Arguments 里」。
	if strings.Contains(body, "internal-autostart") {
		t.Fatal("快捷方式带上了 autostart 旗标：无参才是「打开页面」")
	}
}

// TestWindowsSetupScriptFailsOnHalfInstall 钉住「二进制在、页面 404」必须整体失败。
// 与 install.ps1 同判据：index.html 是入口，favicon.svg 区分「拷了目录」与「拷了内容」。
func TestWindowsSetupScriptFailsOnHalfInstall(t *testing.T) {
	script := readSetupScript(t)
	if !strings.Contains(script, `@('index.html', 'favicon.svg')`) {
		t.Fatal("setup.ps1 没有核对首页锚点")
	}
	loop := "foreach ($anchor in @('index.html', 'favicon.svg'))"
	at := strings.Index(script, loop)
	if at < 0 {
		t.Fatal("setup.ps1 没有逐项核对 web 锚点")
	}
	// 核对不到必须 throw（$ErrorActionPreference='Stop' 下即整体失败），
	// 只写一行警告然后继续装完，等于没校验。
	//
	// 只扫循环体本身：脚本后面 Register-ScheduledTask 等处还有别的 throw，
	// 扫到文件尾会把它们算成这里的守卫（真踩过一次 —— 变异没变红）。
	body := script[at:]
	if end := strings.Index(body, "\n}"); end >= 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "throw") {
		t.Fatal("web 锚点缺失时 setup.ps1 没有失败")
	}
}

// TestWindowsSetupScriptDoesNotReencodeTaskXML 钉住「XML 只由 Go 侧编码一次」。
//
// schtasks /create /xml 只吃 UTF-16LE。桩程序已经写成 UTF-16LE，如果 .ps1 再
// 写一遍（WriteAllText 默认 UTF-8，或者 Out-File 默认 UTF-16LE+BOM），
// 要么报「任务 XML 无效」，要么编码叠加成双字节乱码 —— 两种都没有可读线索。
func TestWindowsSetupScriptDoesNotReencodeTaskXML(t *testing.T) {
	script := readSetupScript(t)
	if !strings.Contains(script, "schtasks.exe /create /tn $TaskName /xml $TaskXml /f") {
		t.Fatal("setup.ps1 没有直接拿桩程序写好的 XML 去注册任务")
	}
	for _, forbidden := range []string{
		"WriteAllText($xml",
		"UnicodeEncoding",
		"Out-File",
		"Set-Content",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("setup.ps1 重新编码了任务 XML（%q）：只能读 $TaskXml", forbidden)
		}
	}
}

// TestWindowsSetupScriptMergesUserPathSafely 钉住 PATH 写法：比较大小写不敏感、
// 忽略尾部反斜杠、已存在就不要重复追加。写坏的表现是用户的整个 PATH 被覆盖
// （之后所有命令都找不到），或者同一条目堆成两行。
func TestWindowsSetupScriptMergesUserPathSafely(t *testing.T) {
	script := readSetupScript(t)
	for _, needle := range []string{
		`-ieq`,
		`TrimEnd('\', '/')`,
		`[Environment]::SetEnvironmentVariable('Path', $next, 'User')`,
	} {
		if !strings.Contains(script, needle) {
			t.Fatalf("setup.ps1 的 PATH 合并缺少 %q", needle)
		}
	}
	// 必须读回原值再追加，而不是直接 SetEnvironmentVariable($Dir)。
	if !strings.Contains(script, "GetEnvironmentVariable('Path', 'User')") {
		t.Fatal("setup.ps1 没有读回当前用户 PATH，追加会覆盖掉它")
	}
}

// TestWindowsSetupScriptStopsBeforeReplacingBinary 钉住「先停再拷」，
// 以及「停不下来就强杀」—— 半路失败会留下新二进制 + 旧进程在跑。
//
// 另外钉住 2026-11-25 的双击失败根因：服务由 S4U 计划任务托管在 Session 0，
// 受限令牌的交互式安装既不能 taskkill 也不能 Stop-Process（都报拒绝访问），
// 而 $ErrorActionPreference='Stop' 会把那个拒绝访问变成终止错误、整个安装就此失败。
// 正路是 schtasks /end 让 Task Scheduler 终止自己的 action；兜底强杀绝不能中断脚本。
func TestWindowsSetupScriptStopsBeforeReplacingBinary(t *testing.T) {
	script := readSetupScript(t)
	if !strings.Contains(script, "schtasks.exe /end /tn $TaskName") {
		t.Fatal("setup.ps1 没有用 schtasks /end 终止托管在 Session 0 的计划任务 —— 受限令牌下 taskkill/Stop-Process 会被拒绝")
	}
	if strings.Index(script, "schtasks.exe /end /tn $TaskName") > strings.Index(script, "& $Binary -stop") {
		t.Fatal("setup.ps1 必须先试 schtasks /end 再退回 mindfs -stop")
	}
	if !strings.Contains(script, "-stop") {
		t.Fatal("setup.ps1 没有调用 mindfs -stop")
	}
	if !strings.Contains(script, "Stop-Process -Force") {
		t.Fatal("setup.ps1 在进程停不下来时没有兜底强杀")
	}
	// 兜底那行必须吞掉自己的错误：跨会话拒绝访问不能再次变成终止错误（这正是原 bug）。
	at := strings.Index(script, "Stop-Process -Force")
	line := script[at:]
	if end := strings.Index(line, "\n"); end >= 0 {
		line = line[:end]
	}
	if !strings.Contains(line, "-ErrorAction SilentlyContinue") {
		t.Fatal("兜底 Stop-Process 没有 -ErrorAction SilentlyContinue：跨会话拒绝访问会再次中止整个安装")
	}
}

// TestBuildWindowsSetupScriptStructure 钉住生成器脚本：先编译桩程序、再拼载荷，
// 且包不完整时在**编译前**就失败。
func TestBuildWindowsSetupScriptStructure(t *testing.T) {
	script := readRepoFile(t, filepath.Join("scripts", "build-windows-setup.sh"))
	// 用真正会产生产物/会退出的行做锚点：拿 "go build" 当锚点的话，
	// 把这一行挪到校验之前也不会变红（真踩过一次 —— 变异没变红）。
	stub := strings.Index(script, "go build -trimpath")
	assemble := strings.Index(script, "go run ./scripts/windows-setup")
	if stub < 0 || assemble < 0 {
		t.Fatalf("build-windows-setup.sh 缺少桩程序/生成器调用 (stub=%d assemble=%d)", stub, assemble)
	}
	if stub > assemble {
		t.Fatal("必须先编译桩程序，再拼载荷")
	}
	// 缺任何一件都要在拼装前 exit 1：装出一个「二进制在、页面 404」的安装包
	// 比没有安装包更糟 —— 用户看到的是双击后什么都没发生。
	checks := strings.Index(script, "for f in mindfs.exe agents.json web/index.html web/favicon.svg")
	if checks < 0 {
		t.Fatal("build-windows-setup.sh 没有核对包完整性")
	}
	if checks > stub {
		t.Fatal("包完整性校验必须发生在编译桩程序之前")
	}
	if !strings.Contains(script, "echo \"missing ${PKG_DIR}/${f}") {
		t.Fatal("包完整性校验没有点名缺了哪个文件")
	}
	if !strings.Contains(script, `-H=windowsgui`) {
		t.Fatal("桩程序没有 -H=windowsgui：双击会闪一个黑框")
	}
}

// TestWindowsSetupStubLivesUnderServerCmd 钉住桩程序的位置。
//
// Go 的 internal 包只能被同一棵 internal 树下的包导入 ——
// server/cmd 可以引 mindfs/server/internal/config，scripts/ 不行。
// 挪回 scripts/ 的后果是交叉编译直接失败（不是运行期问题，但会让发布链断掉）。
func TestWindowsSetupStubLivesUnderServerCmd(t *testing.T) {
	p := filepath.Join("server", "cmd", "mindfs-setup-stub", "main.go")
	data, err := os.ReadFile(filepath.Join("..", "..", p))
	if err != nil {
		t.Fatalf("桩程序不在 %s: %v", p, err)
	}
	src := string(data)
	if !strings.HasPrefix(strings.TrimSpace(src), "//") || !strings.Contains(src, "//go:build windows") {
		t.Fatal("桩程序必须是 windows-only 且有文件头说明")
	}
	// 只有 windows 文件时 Linux 上的 go build ./... 会静默跳过（退出码 0），
	// 所以这里点名它的两条真实依赖，避免它变成「编译不过也没人发现」。
	for _, needle := range []string{
		"deploy.ExtractSetupPayloadToDir",
		"deploy.BuildSetupTaskXML",
		"deploy.EncodeUTF16LEFile",
		"config.UpdateJSONFile",
	} {
		if !strings.Contains(src, needle) {
			t.Fatalf("桩程序没有用到 %s", needle)
		}
	}
}

// TestWindowsSetupStubCapturesChildOutput 钉住「子进程输出落盘、失败时带上尾部」。
//
// 桩程序是 -H=windowsgui，os.Stdout/Stderr 是从资源管理器继承的无效句柄：
// 把 PowerShell 的 stdout/stderr 直接挂上去等于扔掉。2026-11-25 那次
// 「每次双击都失败」的根因就藏在这段被丢掉的话里，而用户只看到 exit status 1。
func TestWindowsSetupStubCapturesChildOutput(t *testing.T) {
	p := filepath.Join("..", "..", "server", "cmd", "mindfs-setup-stub", "main.go")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读不到桩程序 %s: %v", p, err)
	}
	src := string(data)
	if strings.Contains(src, "cmd.Stdout = os.Stdout") || strings.Contains(src, "cmd.Stderr = os.Stderr") {
		t.Fatal("桩程序把子进程输出挂到 os.Stdout/Stderr：GUI 子系统下那是无效句柄，报错会被丢弃")
	}
	for _, needle := range []string{
		"cmd.Stdout = logFile",
		"cmd.Stderr = logFile",
		"func installLogTail(",
		"installLogTail(logPath)",
	} {
		if !strings.Contains(src, needle) {
			t.Fatalf("桩程序缺少 %q：失败对话框里必须能带出真实报错", needle)
		}
	}
}

// TestWindowsSetupStubSelfElevates 钉住「双击也能装」。
//
// 注册 S4U 计划任务需要管理员令牌，而双击拿到的是资源管理器给的受限令牌：
// 2026-11-25 实测同一份 XML、同一个用户 SID，Session 1 受限令牌 schtasks /create
// 报「拒绝访问」，SSH 管理员令牌 EXIT=0。SSH 一直能装、双击从来没成功，分裂就在这里。
// 桩程序没有 UAC 清单（exe 是运行期拼的），所以必须运行期用 runas 自提权。
func TestWindowsSetupStubSelfElevates(t *testing.T) {
	p := filepath.Join("..", "..", "server", "cmd", "mindfs-setup-stub", "main.go")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读不到桩程序 %s: %v", p, err)
	}
	src := string(data)
	for _, needle := range []string{
		"requireElevation()",
		"func requireElevation(",
		"windows.ShellExecute(",
		`"runas"`,
		"token.IsElevated()",
	} {
		if !strings.Contains(src, needle) {
			t.Fatalf("桩程序缺少 %q：双击（受限令牌）注册不了 S4U 任务，安装永远失败", needle)
		}
	}
	// requireElevation 必须在 run() 之前：先提权，再解包/写盘/注册。
	if strings.Index(src, "requireElevation()") > strings.Index(src, "if err := run(); err != nil") {
		t.Fatal("requireElevation() 必须在 run() 之前调用")
	}
}

// TestReleaseChainShipsTheWindowsInstaller 钉住发布链。
//
// 三种漏法都**不会有任何报错**，只是 GitHub release 里少了那个文件：
//  1. `RELEASE_ARTIFACTS` 没有它 → `make release` 不传；
//  2. `build-all` 不生成它 → 上传前那条 `test -f` 才失败（发布半途崩）；
//  3. 签名清单不认它 → 客户端拿到一个不在清单里的安装包，
//     Windows 上「静默自动更新」（D4）永远用不上安装器这条路。
func TestReleaseChainShipsTheWindowsInstaller(t *testing.T) {
	makefile := readRepoFile(t, "Makefile")
	if !strings.Contains(makefile, "mindfs_$(TAG)_windows_amd64-setup.exe") {
		t.Fatal("Makefile 的 RELEASE_ARTIFACTS 没有带上 Windows 安装器")
	}
	buildAll := makefile[strings.Index(makefile, "\nbuild-all:"):]
	if end := strings.Index(buildAll[1:], "\n\n"); end >= 0 {
		buildAll = buildAll[:end+1]
	}
	if !strings.Contains(buildAll, "scripts/build-windows-setup.sh") {
		t.Fatal("build-all 没有生成 Windows 安装器：发布时会在 test -f 那步才失败")
	}

	// 清单枚举必须认这个后缀。注意 setup exe 以 `.exe` 结尾，
	// 而 `.zip` 那条 glob 匹配不到它 —— 漏了就是「包里没有、清单里没有、没人发现」。
	manifest := readRepoFile(t, filepath.Join("scripts", "sign-release-manifest.go"))
	if !strings.Contains(manifest, `mindfs_%s_*setup.exe`) {
		t.Fatal("签名清单的 artifactPaths 没有枚举 Windows 安装器")
	}
}
