package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"mindfs/server/app"
	"mindfs/server/internal/config"
	"mindfs/server/internal/nodeinfo"
)

var version = "dev"

func main() {
	addr := flag.String("addr", "127.0.0.1:7331", "listen address")
	noRelayer := flag.Bool("no-relayer", false, "disable relay integration")
	webPushFlag := flag.Bool("web-push", true, "enable PWA Web Push notifications")
	configFlag := flag.String("config", "", "mindfs startup config file; defaults to <config-dir>/config.json when present. Command-line flags override file values")
	agentConfigFlag := flag.String("agent-config", "", "extra agents.json file")
	notifyScriptFlag := flag.String("notify-script", "", "executable script for notification events; receives JSON payload on stdin")
	roleFlag := flag.String("role", "", "node role: control (default, serves UI + control plane) or worker (data plane only)")
	flag.Parse()
	explicitFlags := visitedFlags(flag.CommandLine)
	startupCfg, err := loadStartupConfig(*configFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	applyStartupConfig(startupCfg, explicitFlags, addr, noRelayer, webPushFlag, agentConfigFlag, notifyScriptFlag, roleFlag)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := app.Start(ctx, *addr, app.StartOptions{
		NoRelayer:       *noRelayer,
		Version:         version,
		Args:            os.Args[1:],
		AgentConfigPath: *agentConfigFlag,
		WebPushEnabled:  *webPushFlag,
		NotifyScript:    *notifyScriptFlag,
		Role:            nodeinfo.Normalize(*roleFlag),
	}); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

type startupConfig struct {
	Addr          *string `json:"addr"`
	NoRelayer     *bool   `json:"noRelayer"`
	NoRelayerFlag *bool   `json:"no-relayer"`
	WebPush       *bool   `json:"webPush"`
	WebPushFlag   *bool   `json:"web-push"`
	AgentConfig   *string `json:"agent-config"`
	NotifyScript  *string `json:"notify-script"`
	// Role 决定这台机器提不提供控制面与前端；worker 只提供数据面。
	// 不配置 = control = 改造前的行为。
	Role *string `json:"role"`
}

func loadStartupConfig(path string) (startupConfig, error) {
	explicit := strings.TrimSpace(path) != ""
	if !explicit {
		// 不传 -config 时读 <config-dir>/config.json —— 缺省路径是这套配置
		// 唯一的入口，否则「写个配置文件就能配」根本做不到（实测踩过：role 写进
		// config.json 后不生效，因为压根没人去读它）。
		resolved, ok := defaultStartupConfigPath()
		if !ok {
			return startupConfig{}, nil
		}
		path = resolved
	}
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// 显式 -config 指到不存在的文件仍然报错：那是操作失误，值得看见。
		// 缺省路径下的文件不存在是常态（大多数机器是单节点，没有 config.json）。
		if !explicit {
			return startupConfig{}, nil
		}
		return startupConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if err != nil {
		return startupConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg startupConfig
	if err := json.Unmarshal(payload, &cfg); err != nil {
		return startupConfig{}, fmt.Errorf("decode config %s: %w", path, err)
	}
	return cfg, nil
}

// defaultStartupConfigPath 返回 <config-dir>/config.json；拿不到配置目录时返回 ok=false
// （读不到就让零值生效，role 回到 control —— 与不配置时行为一致）。
func defaultStartupConfigPath() (string, bool) {
	dir, err := config.MindFSConfigDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(dir, "config.json"), true
}

func visitedFlags(flags *flag.FlagSet) map[string]bool {
	visited := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) {
		visited[f.Name] = true
	})
	return visited
}

func applyStartupConfig(cfg startupConfig, explicit map[string]bool, addr *string, noRelayer *bool, webPush *bool, agentConfig *string, notifyScript *string, role *string) {
	if cfg.Addr != nil && !explicit["addr"] {
		*addr = strings.TrimSpace(*cfg.Addr)
	}
	if value := firstBool(cfg.NoRelayer, cfg.NoRelayerFlag); value != nil && !explicit["no-relayer"] {
		*noRelayer = *value
	}
	if value := firstBool(cfg.WebPush, cfg.WebPushFlag); value != nil && !explicit["web-push"] {
		*webPush = *value
	}
	if cfg.AgentConfig != nil && !explicit["agent-config"] {
		*agentConfig = strings.TrimSpace(*cfg.AgentConfig)
	}
	if cfg.NotifyScript != nil && !explicit["notify-script"] {
		*notifyScript = strings.TrimSpace(*cfg.NotifyScript)
	}
	if cfg.Role != nil && !explicit["role"] {
		*role = strings.TrimSpace(*cfg.Role)
	}
}

func firstBool(values ...*bool) *bool {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
