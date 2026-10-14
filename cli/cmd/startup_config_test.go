package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig 在临时的 XDG 配置目录里放一份 config.json。
func writeConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "mindfs", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// applyRole 只跑 applyStartupConfig 的 role 分支，其余 flag 传 nil
// （它们只在对应字段非 nil 时才被解引用，传 nil 安全）。
func applyRole(cfg startupConfig, explicit map[string]bool) string {
	role := ""
	applyStartupConfig(cfg, explicit, nil, nil, nil, nil, nil, nil, nil, nil, &role)
	return role
}

// 缺省（不传 -config）必须读 <config-dir>/config.json。
// 这条是踩过坑才写下来的：role 写进 config.json 却不生效，因为
// loadStartupConfig 原来在 path 为空时直接返回，永远不读文件。
// 而且测试必须落在 cli/cmd —— 这才是 make build 装出去的入口；
// server/cmd/mindfs-server 只有 make dev-backend 用，发布二进制里没有它。
func TestLoadStartupConfigReadsDefaultPath(t *testing.T) {
	writeConfig(t, `{"role":"worker"}`)

	cfg, err := loadStartupConfig("")
	if err != nil {
		t.Fatalf("loadStartupConfig(\"\") 出错：%v", err)
	}
	if cfg.Role == nil || *cfg.Role != "worker" {
		t.Fatalf("role 未从缺省 config.json 读到：%+v", cfg.Role)
	}
	if got := applyRole(cfg, map[string]bool{}); got != "worker" {
		t.Fatalf("role 未落到 flag 变量：%q", got)
	}
}

// 缺省路径下文件不存在是常态（单节点机器没有 config.json），不能因此启动失败。
func TestLoadStartupConfigMissingDefaultIsNotAnError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, err := loadStartupConfig("")
	if err != nil {
		t.Fatalf("缺省 config.json 不存在却报错：%v", err)
	}
	if cfg.Role != nil {
		t.Fatalf("应为零值配置，却拿到 role=%q", *cfg.Role)
	}
}

// 显式 -config 指向不存在的文件仍然报错 —— 那是操作失误，值得看见。
// 不能与上面那条合并：两者都是 ErrNotExist，但语义相反。
func TestLoadStartupConfigExplicitMissingIsAnError(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "nope.json")

	if _, err := loadStartupConfig(missing); err == nil {
		t.Fatal("显式 -config 指向缺失文件应当报错，实际静默通过")
	}
}

// flag 优先于文件：显式传 -role 时文件里的值必须被忽略。
// 这是 -config 帮助文本承诺的语义（"command-line flags override file values"）。
func TestExplicitFlagOverridesConfigFile(t *testing.T) {
	writeConfig(t, `{"role":"worker"}`)

	cfg, err := loadStartupConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if got := applyRole(cfg, map[string]bool{"role": true}); got != "" {
		t.Fatalf("显式 flag 应胜出（保留原值），却拿到 role=%q", got)
	}
}

// 未知 role 值原样传给 app：归一化（回落 control）在 server/app 里做，
// 这里够不着 nodeinfo（Go internal 规则，根 module 不能 import server/internal）。
// 这条钉住的是「cli/cmd 不自己猜 role」，归一化本身的语义由 nodeinfo 的测试守。
func TestUnknownRoleValuePassesThroughUnchanged(t *testing.T) {
	writeConfig(t, `{"role":"nonsense"}`)

	cfg, err := loadStartupConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if got := applyRole(cfg, map[string]bool{}); got != "nonsense" {
		t.Fatalf("cli/cmd 应原样传递 role 由 app 归一化，却拿到 %q", got)
	}
}

// 发布构建固定从 cli/cmd 进入；server/cmd/mindfs-server 只用于 dev-backend。
// 该边界曾导致启动配置修在错误入口、单测绿但发布二进制不生效。
func TestReleaseAndDevelopmentEntrypointsStaySeparated(t *testing.T) {
	makefile, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	text := string(makefile)
	build := makeTargetBody(text, "build")
	if !strings.Contains(build, "./cli/cmd") {
		t.Fatalf("release build must use cli/cmd, got:\n%s", build)
	}
	if strings.Contains(build, "server/cmd/mindfs-server") {
		t.Fatalf("release build must not use dev-only server/cmd entrypoint:\n%s", build)
	}
	devBackend := makeTargetBody(text, "dev-backend")
	if !strings.Contains(devBackend, "./server/cmd/mindfs-server") {
		t.Fatalf("dev-backend must use server/cmd/mindfs-server, got:\n%s", devBackend)
	}
	buildAll, err := os.ReadFile(filepath.Join("..", "..", "scripts", "build-all.sh"))
	if err != nil {
		t.Fatalf("read scripts/build-all.sh: %v", err)
	}
	if !strings.Contains(string(buildAll), "./cli/cmd") {
		t.Fatal("release build-all must use cli/cmd")
	}
}

func makeTargetBody(makefile, target string) string {
	var lines []string
	active := false
	for _, line := range strings.Split(makefile, "\n") {
		if !active {
			active = strings.HasPrefix(line, target+":")
			continue
		}
		if line == "" || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "#") {
			lines = append(lines, line)
			continue
		}
		break
	}
	return strings.Join(lines, "\n")
}
