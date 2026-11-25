package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"mindfs/internal/configfile"
	"mindfs/internal/deploy"
)

// --init-config 是安装器写入初始配置的唯一入口。
//
// 旧的自解压桩把 control_sync.role=edge 与 update.auto_install=true 直接写进
// config.json；改成 NSIS 之后，同样的键必须由随包 exe 写 —— 让 .nsi 自己拼 JSON
// 会在「用户已有配置」时整段覆盖（保留未知键、数字不漂移这些约定都在 Go 侧）。
func runInitConfigCommand(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "--init-config" {
		return false, nil
	}
	flags := flag.NewFlagSet("init-config", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configFlag := flags.String("config", "", "mindfs config.json; defaults to the standard path")
	if err := flags.Parse(args[1:]); err != nil {
		return true, errors.New("usage: mindfs --init-config [--config FILE]")
	}
	path := effectiveConfigPath(*configFlag)
	if path == "" {
		return true, errors.New("找不到 config.json 路径")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return true, fmt.Errorf("创建配置目录失败: %w", err)
	}
	if err := configfile.Update(path, deploy.ApplySetupConfig); err != nil {
		return true, fmt.Errorf("写入 %s 失败: %w", path, err)
	}
	return true, nil
}
