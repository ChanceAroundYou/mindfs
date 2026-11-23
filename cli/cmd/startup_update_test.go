package main

import (
	"os"
	"strings"
	"testing"
)

// update.auto_install 默认必须是 false：写错成默认开会让每台装了旧版的机器
// 自己把服务换掉并重启。只有 Windows 安装包显式写 true。
//
// 这条判据的代价不对称：漏检一次就是用户正在跑的 agent 回合被腰斩，而多写一个
// 测试只花几十毫秒，所以四条分支各写一行，不合并成表驱动之外的花活。
func TestUpdateAutoInstallDefaultsOff(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"没写 update 段", `{}`, false},
		{"写了 update 但没写 auto_install", `{"update":{}}`, false},
		{"显式关闭", `{"update":{"auto_install":false}}`, false},
		{"安装包打开", `{"update":{"auto_install":true}}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			writeConfig(t, c.body)
			cfg, err := loadStartupConfig("")
			if err != nil {
				t.Fatal(err)
			}
			if got := updateAutoInstall(cfg); got != c.want {
				t.Fatalf("update.auto_install = %t，期望 %t", got, c.want)
			}
		})
	}
}

// 上面测的是读取本身；这条钉住它真的被接进 server.Options —— 那段装配在一个巨大的
// 闭包里，没有可以注入的接缝，只能按源码契约核对（同 startup_harness_test.go）。
// 少了这一行，config.json 里写 auto_install=true 会静默不生效。
func TestServerOptionsCarryUpdateAutoInstall(t *testing.T) {
	raw, err := os.ReadFile("mindfs.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "UpdateAutoInstall:                  updateAutoInstall(startupCfg),") {
		t.Fatal("server.Options no longer receives updateAutoInstall(startupCfg)")
	}
}
