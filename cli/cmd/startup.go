package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type startupConfig struct {
	Addr         *string `json:"addr"`
	WebPush      *bool   `json:"webPush"`
	WebPushFlag  *bool   `json:"web-push"`
	Foreground   *bool   `json:"foreground"`
	TLS          *bool   `json:"tls"`
	Cert         *string `json:"cert"`
	Key          *string `json:"key"`
	AgentConfig  *string `json:"agent-config"`
	NotifyScript *string `json:"notify-script"`
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
// 复用 autoStartConfigDir：它算的正是同一个 <UserConfigDir>/mindfs。
func defaultStartupConfigPath() (string, bool) {
	dir, err := autoStartConfigDir()
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

func applyStartupConfig(
	cfg startupConfig,
	explicit map[string]bool,
	addr *string,
	webPush *bool,
	foreground *bool,
	tlsFlag *bool,
	cert *string,
	key *string,
	agentConfig *string,
	notifyScript *string,
	role *string,
) {
	if cfg.Addr != nil && !explicit["addr"] {
		*addr = strings.TrimSpace(*cfg.Addr)
	}
	if value := firstBool(cfg.WebPush, cfg.WebPushFlag); value != nil && !explicit["web-push"] {
		*webPush = *value
	}
	if cfg.Foreground != nil && !explicit["foreground"] {
		*foreground = *cfg.Foreground
	}
	if cfg.TLS != nil && !explicit["tls"] {
		*tlsFlag = *cfg.TLS
	}
	if cfg.Cert != nil && !explicit["cert"] {
		*cert = strings.TrimSpace(*cfg.Cert)
	}
	if cfg.Key != nil && !explicit["key"] {
		*key = strings.TrimSpace(*cfg.Key)
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

func startBackgroundProcess(logPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := rotateLogIfNeeded(logPath, maxLogSizeBytes, maxLogBackups); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(cmd.Environ(), daemonEnvKey+"=1")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	configureBackgroundCommand(cmd)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return err
	}
	return logFile.Close()
}
