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
	"time"

	"mindfs/server/app"
	"mindfs/server/internal/config"
)

var version = "dev"

func main() {
	addr := flag.String("addr", "127.0.0.1:7331", "listen address")
	webPushFlag := flag.Bool("web-push", true, "enable PWA Web Push notifications")
	configFlag := flag.String("config", "", "mindfs startup config file; defaults to <config-dir>/config.json when present. Command-line flags override file values")
	agentConfigFlag := flag.String("agent-config", "", "extra agents.json file")
	notifyScriptFlag := flag.String("notify-script", "", "executable script for notification events; receives JSON payload on stdin")
	roleFlag := flag.String("role", "", "node role: control (default, serves UI + control plane) or worker (data plane only)")
	routerModeFlag := flag.String("router-mode", "", "task router mode: off (default), shadow, route, coordinate")
	routerAgentFlag := flag.String("router-agent", "", "task router agent name (default claude)")
	routerModelFlag := flag.String("router-model", "", "task router model; empty = agent default")
	routerEffortFlag := flag.String("router-effort", "", "task router effort level (default low)")
	routerTurnTimeoutFlag := flag.Duration("router-turn-timeout", 0, "task router turn timeout, e.g. 90s (default 90s)")
	routerIdleCloseFlag := flag.Duration("router-idle-close", 0, "close idle router sessions after this duration, e.g. 30m (default 30m)")
	workflowParallelMainTreeFlag := flag.Bool("workflow-parallel-main-tree", true, "run tasks without independent worktrees in parallel on the shared main tree")
	liveCompactModeFlag := flag.String("live-compact-mode", "", "context live-compact mode: off (default), shadow (measure only), on")
	liveCompactKeepRoundsFlag := flag.Int("live-compact-keep-rounds", 0, "newest completed tool rounds kept verbatim (default 3)")
	liveCompactMinBlockTokensFlag := flag.Int("live-compact-min-block-tokens", 0, "minimum estimated tokens for a tool result to be folded (default 2000)")
	liveCompactMinBodyBytes := 0
	liveCompactMaxDriftMessages := 0
	flag.Parse()
	explicitFlags := visitedFlags(flag.CommandLine)
	startupCfg, err := loadStartupConfig(*configFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	if err := applyStartupConfig(startupCfg, explicitFlags, startupFlags{
		Addr:                        addr,
		WebPush:                     webPushFlag,
		AgentConfig:                 agentConfigFlag,
		NotifyScript:                notifyScriptFlag,
		Role:                        roleFlag,
		RouterMode:                  routerModeFlag,
		RouterAgent:                 routerAgentFlag,
		RouterModel:                 routerModelFlag,
		RouterEffort:                routerEffortFlag,
		RouterTurnTimeout:           routerTurnTimeoutFlag,
		RouterIdleClose:             routerIdleCloseFlag,
		WorkflowParallelMainTree:    workflowParallelMainTreeFlag,
		LiveCompactMode:             liveCompactModeFlag,
		LiveCompactKeepRounds:       liveCompactKeepRoundsFlag,
		LiveCompactMinBlockTokens:   liveCompactMinBlockTokensFlag,
		LiveCompactMinBodyBytes:     &liveCompactMinBodyBytes,
		LiveCompactMaxDriftMessages: &liveCompactMaxDriftMessages,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := app.Start(ctx, *addr, app.StartOptions{
		Version:                     version,
		Args:                        os.Args[1:],
		AgentConfigPath:             *agentConfigFlag,
		WebPushEnabled:              *webPushFlag,
		NotifyScript:                *notifyScriptFlag,
		Role:                        *roleFlag,
		RouterMode:                  *routerModeFlag,
		RouterAgent:                 *routerAgentFlag,
		RouterModel:                 *routerModelFlag,
		RouterEffort:                *routerEffortFlag,
		RouterTurnTimeout:           *routerTurnTimeoutFlag,
		RouterIdleClose:             *routerIdleCloseFlag,
		RouterRoots:                 routerRoots(startupCfg),
		WorkflowParallelMainTree:    *workflowParallelMainTreeFlag,
		LiveCompactMode:             *liveCompactModeFlag,
		LiveCompactKeepRounds:       *liveCompactKeepRoundsFlag,
		LiveCompactMinBlockTokens:   *liveCompactMinBlockTokensFlag,
		LiveCompactMinBodyBytes:     liveCompactMinBodyBytes,
		LiveCompactMaxDriftMessages: liveCompactMaxDriftMessages,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

type startupConfig struct {
	Addr         *string `json:"addr"`
	WebPush      *bool   `json:"webPush"`
	WebPushFlag  *bool   `json:"web-push"`
	AgentConfig  *string `json:"agent-config"`
	NotifyScript *string `json:"notify-script"`
	// Role 决定这台机器提不提供控制面与前端；worker 只提供数据面。
	// 不配置 = control = 改造前的行为。
	Role     *string                `json:"role"`
	Router   *startupRouterConfig   `json:"router"`
	Workflow *startupWorkflowConfig `json:"workflow"`
	// LiveCompact 是节点本地的上下文活压缩配置。不配置 = off。
	LiveCompact *startupLiveCompactConfig `json:"live_compact"`
}

type startupWorkflowConfig struct {
	ParallelMainTree *bool `json:"parallel_main_tree"`
}

type startupLiveCompactConfig struct {
	Mode             *string `json:"mode"`
	KeepRounds       *int    `json:"keep_rounds"`
	MinBlockTokens   *int    `json:"min_block_tokens"`
	MinBodyBytes     *int    `json:"min_body_bytes"`
	MaxDriftMessages *int    `json:"max_drift_messages"`
}

type startupRouterConfig struct {
	Mode        *string           `json:"mode"`
	Agent       *string           `json:"agent"`
	Model       *string           `json:"model"`
	Effort      *string           `json:"effort"`
	TurnTimeout *string           `json:"turn_timeout"`
	IdleClose   *string           `json:"idle_close"`
	Roots       map[string]string `json:"roots"`
}

type startupFlags struct {
	Addr         *string
	WebPush      *bool
	AgentConfig  *string
	NotifyScript *string
	Role         *string

	RouterMode        *string
	RouterAgent       *string
	RouterModel       *string
	RouterEffort      *string
	RouterTurnTimeout *time.Duration
	RouterIdleClose   *time.Duration

	WorkflowParallelMainTree *bool

	LiveCompactMode             *string
	LiveCompactKeepRounds       *int
	LiveCompactMinBlockTokens   *int
	LiveCompactMinBodyBytes     *int
	LiveCompactMaxDriftMessages *int
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

func applyStartupConfig(cfg startupConfig, explicit map[string]bool, flags startupFlags) error {
	if flags.Addr != nil && cfg.Addr != nil && !explicit["addr"] {
		*flags.Addr = strings.TrimSpace(*cfg.Addr)
	}
	if flags.WebPush != nil {
		if value := firstBool(cfg.WebPush, cfg.WebPushFlag); value != nil && !explicit["web-push"] {
			*flags.WebPush = *value
		}
	}
	if flags.AgentConfig != nil && cfg.AgentConfig != nil && !explicit["agent-config"] {
		*flags.AgentConfig = strings.TrimSpace(*cfg.AgentConfig)
	}
	if flags.NotifyScript != nil && cfg.NotifyScript != nil && !explicit["notify-script"] {
		*flags.NotifyScript = strings.TrimSpace(*cfg.NotifyScript)
	}
	if flags.Role != nil && cfg.Role != nil && !explicit["role"] {
		*flags.Role = strings.TrimSpace(*cfg.Role)
	}
	if flags.WorkflowParallelMainTree != nil && !explicit["workflow-parallel-main-tree"] &&
		cfg.Workflow != nil && cfg.Workflow.ParallelMainTree != nil {
		*flags.WorkflowParallelMainTree = *cfg.Workflow.ParallelMainTree
	}
	if live := cfg.LiveCompact; live != nil {
		if flags.LiveCompactMode != nil && live.Mode != nil && !explicit["live-compact-mode"] {
			*flags.LiveCompactMode = strings.TrimSpace(*live.Mode)
		}
		if flags.LiveCompactKeepRounds != nil && live.KeepRounds != nil && !explicit["live-compact-keep-rounds"] {
			*flags.LiveCompactKeepRounds = *live.KeepRounds
		}
		if flags.LiveCompactMinBlockTokens != nil && live.MinBlockTokens != nil && !explicit["live-compact-min-block-tokens"] {
			*flags.LiveCompactMinBlockTokens = *live.MinBlockTokens
		}
		if flags.LiveCompactMinBodyBytes != nil && live.MinBodyBytes != nil {
			*flags.LiveCompactMinBodyBytes = *live.MinBodyBytes
		}
		if flags.LiveCompactMaxDriftMessages != nil && live.MaxDriftMessages != nil {
			*flags.LiveCompactMaxDriftMessages = *live.MaxDriftMessages
		}
	}
	router := cfg.Router
	if router == nil {
		return nil
	}
	setRouterString(flags.RouterMode, router.Mode, explicit["router-mode"])
	setRouterString(flags.RouterAgent, router.Agent, explicit["router-agent"])
	setRouterString(flags.RouterModel, router.Model, explicit["router-model"])
	setRouterString(flags.RouterEffort, router.Effort, explicit["router-effort"])
	if flags.RouterTurnTimeout != nil && !explicit["router-turn-timeout"] {
		value, err := parseStartupDuration("router.turn_timeout", router.TurnTimeout)
		if err != nil {
			return err
		}
		if value != nil {
			*flags.RouterTurnTimeout = *value
		}
	}
	if flags.RouterIdleClose != nil && !explicit["router-idle-close"] {
		value, err := parseStartupDuration("router.idle_close", router.IdleClose)
		if err != nil {
			return err
		}
		if value != nil {
			*flags.RouterIdleClose = *value
		}
	}
	return nil
}

func setRouterString(dst *string, value *string, explicit bool) {
	if dst == nil || value == nil || explicit {
		return
	}
	*dst = strings.TrimSpace(*value)
}

func parseStartupDuration(field string, value *string) (*time.Duration, error) {
	if value == nil {
		return nil, nil
	}
	text := strings.TrimSpace(*value)
	if text == "" {
		return nil, nil
	}
	parsed, err := time.ParseDuration(text)
	if err != nil {
		return nil, fmt.Errorf("config %s=%q: %w", field, text, err)
	}
	return &parsed, nil
}

func routerRoots(cfg startupConfig) map[string]string {
	if cfg.Router == nil || len(cfg.Router.Roots) == 0 {
		return nil
	}
	return cfg.Router.Roots
}

func firstBool(values ...*bool) *bool {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
