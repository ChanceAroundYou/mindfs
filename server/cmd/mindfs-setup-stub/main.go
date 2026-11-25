// mindfs-setup-stub 是 Windows 边控端一键安装器的「桩程序」。
//
// 安装包 = [本程序][载荷 zip][长度+魔数]（见 internal/deploy/setup.go）。
// 双击之后本程序做四件事，然后把剩下的交给载荷里的 setup.ps1：
//
//  1. 从自己的可执行文件尾部切出载荷，解到临时目录；
//  2. 由 %LOCALAPPDATA% / %APPDATA% 推出安装布局（deploy.SetupPathsFor）；
//  3. 把 control_sync.role=edge 与 update.auto_install=true 写进 config.json
//     （config.UpdateJSONFile：原子换名、未知键保留、数字不漂移）；
//  4. 求当前用户 SID，生成计划任务 XML 并以 **UTF-16LE** 落盘 —— schtasks 只吃
//     UTF-16，而 SID 只能运行期求值，这两件事都不适合放进 .ps1；
//
// 最后 `powershell -ExecutionPolicy Bypass -File setup.ps1` 干活：停旧进程、拷贝、
// 注册任务、改 PATH、起服务。交付给用户的 exe 只有这一个文件。
//
// 为什么拆成 Go 桩 + .ps1 两半：
//   - 路径推导、JSON 改写、XML 生成都是**纯逻辑**，放在 internal/deploy 里能让
//     Linux 门禁逐条钉住（这台机器上的 Go 测试跑不了 Windows）；
//   - 停进程、改用户 PATH、schtasks 这些只能是 Windows API/命令，放进 .ps1。
//
// 本包只有 windows 文件，`go build ./...` 在 Linux 上会静默跳过（实测退出码 0），
// 所以它不会拖累本机门禁；真正的验证在 scripts/build-windows-setup.sh 交叉编译。
//
//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"mindfs/internal/deploy"
	"mindfs/server/internal/config"
)

func main() {
	// 注册 S4U 计划任务（本安装器的核心动作：让 worker 开机就起、不需要有人登录）
	// 需要管理员令牌 —— Windows 不允许受限令牌写 Task Scheduler 里的 S4U 任务。
	// 从 SSH（管理员）装一直能成，双击（资源管理器给的受限令牌）却永远死在这里，
	// 这就是「从来没成功」的根因。安装器没有也拿不到 UAC 清单（载荷是运行时拼的），
	// 所以只能运行期自提权：用 runas 再拉起自己，UAC 是唯一的门槛。
	requireElevation()

	if err := run(); err != nil {
		fatal(err)
	}
}

// requireElevation 在非提权时用 ShellExecute(runas) 重新拉起自己并退出。
//
// 成功拉起后父进程必须静默退出：真正干活的是提权后的子进程，它会弹自己的结果
// 对话框；父进程再弹任何东西都是重复。用户取消 UAC 时给出明确指引而不是哑失败。
func requireElevation() {
	if isElevated() {
		return
	}
	self, err := os.Executable()
	if err != nil {
		fatal(fmt.Errorf("cannot locate this installer: %w", err))
	}
	if err := relaunchElevated(self); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			showMessage("MindFS setup needs administrator rights to register the background service.\n\n"+
				"The prompt was cancelled. Double-click the installer again and choose Yes when Windows asks for permission.", true)
			os.Exit(1)
		}
		fatal(fmt.Errorf("cannot restart with administrator rights: %w", err))
	}
	os.Exit(0)
}

func isElevated() bool {
	token := windows.GetCurrentProcessToken()
	return token.IsElevated()
}

func relaunchElevated(exe string) error {
	verb, err := windows.UTF16PtrFromString("runas")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return err
	}
	cwd, err := windows.UTF16PtrFromString(filepath.Dir(exe))
	if err != nil {
		cwd = nil
	}
	return windows.ShellExecute(0, verb, file, nil, cwd, windows.SW_SHOWNORMAL)
}

func fatal(err error) {
	// 安装器以 GUI 子系统启动（-H=windowsgui），没有控制台可打印 —— 落一个错误
	// 对话框，否则用户看到的是「双击了，什么都没发生」。
	showMessage("MindFS setup failed:\n\n"+err.Error(), true)
	os.Exit(1)
}

func run() error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot locate this installer: %w", err)
	}
	stage, err := deploy.ExtractSetupPayloadToDir(self, filepath.Join(os.TempDir(), deploy.SetupStagingDirName))
	if err != nil {
		return fmt.Errorf("cannot unpack this installer: %w", err)
	}
	defer os.RemoveAll(stage)

	localAppData := os.Getenv("LOCALAPPDATA")
	roamingAppData := os.Getenv("APPDATA")
	paths, err := deploy.SetupPathsFor(localAppData, roamingAppData)
	if err != nil {
		return err
	}

	if err := writeConfig(paths.ConfigPath); err != nil {
		return err
	}

	sid, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("cannot determine the current user SID: %w", err)
	}
	taskXML, err := deploy.BuildSetupTaskXML(deploy.SetupTaskOptions{
		TaskName: deploy.SetupTaskName,
		UserSID:  sid,
		Binary:   paths.BinaryPath,
	})
	if err != nil {
		return err
	}
	// 放在解包目录里：setup.ps1 用 schtasks /create /xml 直接吃这个文件，
	// 已经编码好 UTF-16LE，ps1 里绝不能再转一次码。
	xmlPath := filepath.Join(stage, "mindfs-task.xml")
	if err := os.WriteFile(xmlPath, deploy.EncodeUTF16LEFile(taskXML), 0o600); err != nil {
		return err
	}

	return runSetupScript(filepath.Join(stage, deploy.SetupPayloadEntryName), paths, xmlPath)
}

func writeConfig(configPath string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return err
	}
	if err := config.UpdateJSONFile(configPath, deploy.ApplySetupConfig); err != nil {
		return fmt.Errorf("cannot write %s: %w", configPath, err)
	}
	return nil
}

// runSetupScript 把全部路径预先算好传进去：让 .ps1 再拼一遍是第二次机会犯同一个错，
// 而那时拼错的表现是「服务在跑但页面 403」，与安装器毫无关联。
//
// 子进程的输出必须落盘：安装器是 -H=windowsgui，os.Stdout/Stderr 只是从双击它的
// 资源管理器继承来的无效句柄，PowerShell 的报错会被直接丢弃。丢失的代价是真实的
// —— 一次「每次双击都失败」的根因（跨会话终止被拒）就藏在那段被扔掉的话里，
// 而用户只看到 "exit status 1"。写到安装前缀旁边的 setup.log，失败时把尾部带上。
func runSetupScript(ps1 string, paths deploy.SetupPaths, xmlPath string) error {
	if _, err := os.Stat(ps1); err != nil {
		return fmt.Errorf("payload is missing setup.ps1: %w", err)
	}
	args := []string{
		"-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass",
		"-File", ps1,
		"-StageDir", filepath.Dir(ps1),
		"-Prefix", paths.Prefix,
		"-BinDir", paths.BinDir,
		"-Binary", paths.BinaryPath,
		"-ShareDir", paths.ShareDir,
		"-Agents", paths.AgentsPath,
		"-Web", paths.WebDir,
		"-TaskXml", xmlPath,
		"-TaskName", deploy.SetupTaskName,
	}
	// 日志放在安装前缀下（%LOCALAPPDATA%\Programs\mindfs\setup.log）：重启/重装
	// 都会被下一次覆盖，但它就是「装坏了去哪儿看」的固定位置。目录写不了时退回
	// 临时目录，绝不因为日志写不了而让安装失败。
	logPath := filepath.Join(paths.Prefix, "setup.log")
	if err := os.MkdirAll(paths.Prefix, 0o755); err != nil {
		logPath = filepath.Join(os.TempDir(), deploy.SetupStagingDirName+"-setup.log")
	}
	logFile, logErr := os.Create(logPath)
	if logErr != nil {
		logPath = filepath.Join(os.TempDir(), deploy.SetupStagingDirName+"-setup.log")
		if f, err := os.Create(logPath); err == nil {
			logFile = f
		} else {
			logPath = ""
		}
	}

	cmd := exec.Command("powershell.exe", args...)
	if logFile != nil {
		defer logFile.Close()
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	runErr := cmd.Run()
	if runErr == nil {
		return nil
	}
	return fmt.Errorf("setup.ps1 failed: %v%s", runErr, installLogTail(logPath))
}

// installLogTail 把 setup.log 的末尾几行接进错误消息。空路径或读不到时返回空串：
// 诊断信息是加分项，绝不能因为它自己出错而改变失败本身。
func installLogTail(logPath string) string {
	if logPath == "" {
		return ""
	}
	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		return ""
	}
	const maxTail = 1500
	text := strings.TrimRight(string(data), "\r\n")
	if len(text) > maxTail {
		text = "..." + text[len(text)-maxTail:]
	}
	return "\n\n--- setup.log (tail) ---\n" + text + "\n--- end setup.log ---"
}

// currentUserSID 取当前进程令牌里的用户 SID（形如 S-1-5-21-...-1001）。
//
// 计划任务的 <UserId> 必须是它：写名字（domain\user）在 S4U 下有本地化歧义，
// 而 SID 逐字稳定。用**进程令牌**而不是 LookupAccount，是因为安装器总是以目标
// 用户的身份运行 —— 提权时求到的是管理员，那正好也是要注册任务的账户。
func currentUserSID() (string, error) {
	proc, err := windows.GetCurrentProcess()
	if err != nil {
		return "", err
	}
	var token windows.Token
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &token); err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	if user == nil || user.User.Sid == nil {
		return "", errors.New("token has no user SID")
	}
	return user.User.Sid.String(), nil
}

// showMessage 用 MessageBoxW 报错。x/sys 没有导出它，裸取 user32 的符号。
//
// 走 MessageBoxW 而不是弹一个 PowerShell：安装失败的现场往往连 powershell 都
// 没跑起来（载荷坏、路径不对），那时唯一的用户可见面就是这张对话框。
func showMessage(text string, isError bool) {
	if strings.TrimSpace(text) == "" {
		return
	}
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("MessageBoxW")
	title, _ := windows.UTF16PtrFromString("MindFS setup")
	body, _ := windows.UTF16PtrFromString(text)
	flags := uintptr(0x00000040) // MB_ICONINFORMATION
	if isError {
		flags = 0x00000010 // MB_ICONERROR
	}
	_, _, _ = proc.Call(0, uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), flags)
}
