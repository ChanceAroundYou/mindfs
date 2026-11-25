//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"mindfs/internal/deploy"
)

// --register-service / --unregister-service 是安装器与卸载器唯一的提权动作。
//
// 为什么不让 NSIS 直接调 schtasks：注册 S4U 任务需要管理员令牌，而安装器是
// per-user（RequestExecutionLevel user）—— 直接调会在受限令牌下「拒绝访问」，
// 而且 schtasks 的任务 XML 必须是 UTF-16LE、UserId 必须是 SID（写用户名在
// 中文本地账户上会 0x80070534）。这三件事已经在 internal/deploy 里被钉住，
// 让 .nsi 重写一遍就是第二个实现。
//
// 提权由 NSIS 用 ShellExecute(runas) 发起，所以这里假定自己已经提权；没提权时
// schtasks 会失败并原样报出来，不会静默当成功。
func runServiceTaskCommand(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "--register-service":
		return true, runRegisterService(args[1:])
	case "--unregister-service":
		return true, runUnregisterService(args[1:])
	default:
		return false, nil
	}
}

type serviceTaskResult struct {
	OK     bool     `json:"ok"`
	Task   string   `json:"task"`
	Tasks  []string `json:"tasks,omitempty"`
	Error  string   `json:"error,omitempty"`
	Output string   `json:"output,omitempty"`
}

func runRegisterService(args []string) error {
	flags := flag.NewFlagSet("register-service", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	resultFlag := flags.String("result", "", "write the JSON result to this file")
	addrFlag := flags.String("addr", "0.0.0.0:7332", "listen address for the service")
	if err := flags.Parse(args); err != nil {
		return errors.New("usage: mindfs --register-service [--result FILE] [--addr HOST:PORT]")
	}

	result := serviceTaskResult{Task: deploy.SetupTaskName}
	tasks, err := registerServiceTasks(strings.TrimSpace(*addrFlag))
	result.Tasks = tasks
	if err != nil {
		result.Error = err.Error()
	} else {
		result.OK = true
	}
	writeServiceTaskResult(strings.TrimSpace(*resultFlag), result)
	if err != nil {
		return err
	}
	return nil
}

func runUnregisterService(args []string) error {
	flags := flag.NewFlagSet("unregister-service", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	resultFlag := flags.String("result", "", "write the JSON result to this file")
	if err := flags.Parse(args); err != nil {
		return errors.New("usage: mindfs --unregister-service [--result FILE]")
	}
	result := serviceTaskResult{Task: deploy.SetupTaskName}
	var firstErr error
	for _, name := range []string{deploy.SetupUpdateTaskName, deploy.SetupTaskName} {
		out, err := exec.Command("schtasks.exe", "/delete", "/tn", name, "/f").CombinedOutput()
		text := strings.TrimSpace(string(out))
		result.Output = text
		result.Tasks = append(result.Tasks, name)
		if err == nil {
			continue
		}
		// 任务本来就不存在时卸载不算失败：卸载器要能反复跑。
		if strings.Contains(text, "找不到") || strings.Contains(strings.ToLower(text), "cannot find") || strings.Contains(strings.ToLower(text), "does not exist") {
			continue
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("schtasks /delete /tn %s failed: %v: %s", name, err, text)
		}
	}
	if firstErr != nil {
		result.Error = firstErr.Error()
	} else {
		result.OK = true
	}
	writeServiceTaskResult(strings.TrimSpace(*resultFlag), result)
	return firstErr
}

// registerServiceTasks 注册两个任务：服务本身，和自动更新用的旁路任务。
//
// 更新任务必须在这里一起注册 —— 注册它需要管理员令牌，而自动更新时没有人能点 UAC。
// 安装时一次性提权，之后每次更新只是 schtasks /run。
func registerServiceTasks(addr string) ([]string, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot locate mindfs.exe: %w", err)
	}
	sid, err := currentProcessUserSID()
	if err != nil {
		return nil, fmt.Errorf("cannot determine the current user SID: %w", err)
	}
	staging := deploy.SetupUpdateStagingPath(filepath.Dir(filepath.Dir(self)))

	serviceXML, err := deploy.BuildSetupTaskXML(deploy.SetupTaskOptions{
		TaskName: deploy.SetupTaskName,
		UserSID:  sid,
		Binary:   self,
		Addr:     addr,
	})
	if err != nil {
		return nil, err
	}
	updateXML, err := deploy.BuildUpdateTaskXML(deploy.UpdateTaskOptions{
		TaskName:  deploy.SetupUpdateTaskName,
		UserSID:   sid,
		SetupPath: staging,
	})
	if err != nil {
		return nil, err
	}

	dir, err := os.MkdirTemp("", "mindfs-task-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	var created []string
	for _, task := range []struct{ name, xml string }{
		{deploy.SetupTaskName, serviceXML},
		{deploy.SetupUpdateTaskName, updateXML},
	} {
		xmlPath := filepath.Join(dir, task.name+".xml")
		if err := os.WriteFile(xmlPath, deploy.EncodeUTF16LEFile(task.xml), 0o600); err != nil {
			return created, err
		}
		out, err := exec.Command("schtasks.exe", "/create", "/tn", task.name, "/xml", xmlPath, "/f").CombinedOutput()
		if err != nil {
			return created, fmt.Errorf("schtasks /create /tn %s failed: %v: %s", task.name, err, strings.TrimSpace(string(out)))
		}
		created = append(created, task.name)
	}
	return created, nil
}

func currentProcessUserSID() (string, error) {
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

func writeServiceTaskResult(path string, result serviceTaskResult) {
	if path == "" {
		return
	}
	data, err := json.Marshal(result)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
	// 结果旁边再落一个 .ok 哨兵：安装器在 NSIS 里等的是「文件出现」，
	// 让它去解析 JSON 等于在 .nsi 里写第二个解析器。
	if result.OK {
		_ = os.WriteFile(path+".ok", []byte("ok"), 0o600)
	}
}
