package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"mindfs/server/app"
	"mindfs/server/internal/agent/fold"
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
	liveCompactKeepTailTokens := 0
	liveCompactMinBlockTokensFlag := flag.Int("live-compact-min-block-tokens", 0, "minimum estimated tokens for a tool result to be folded (default 2000)")
	liveCompactMinBodyBytes := 0
	liveCompactMaxDriftMessages := 0
	// Rail gate and fold budget (design §12.6). 只由 config.json 填充，没有 CLI flag。
	liveCompactRailTokens := 0
	liveCompactMinFoldBytes := 0
	liveCompactMaxFoldBytes := 0
	liveCompactLargeBlockTokens := 0
	// 前缀闸门与冷缓存择时没有 CLI flag：只由 config.json 的 live_compact 段填充。
	// 初值就是默认值（都开），配置文件存在时覆盖它。
	liveCompactPrefixGuard := true
	var liveCompactPrefixUnsafeModels []string
	liveCompactColdFold := true
	liveCompactColdRailTokens := 0
	liveCompactCacheTTLSeconds := 0
	// 任务派发默认 Agent/模型，只走 config.json（task 段），没有 CLI flag。
	taskAgent, taskModel := "", ""
	routerSupervisionMode, routerSupervisionRetries, routerSupervisionRunActions, routerSupervisionActions, routerSupervisionQuotaHold := "", 0, 0, 0, 0
	flag.Parse()
	explicitFlags := visitedFlags(flag.CommandLine)
	startupCfg, err := loadStartupConfig(*configFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	if err := applyStartupConfig(startupCfg, explicitFlags, startupFlags{
		Addr:                          addr,
		WebPush:                       webPushFlag,
		AgentConfig:                   agentConfigFlag,
		NotifyScript:                  notifyScriptFlag,
		Role:                          roleFlag,
		RouterMode:                    routerModeFlag,
		RouterAgent:                   routerAgentFlag,
		RouterModel:                   routerModelFlag,
		RouterEffort:                  routerEffortFlag,
		RouterTurnTimeout:             routerTurnTimeoutFlag,
		RouterIdleClose:               routerIdleCloseFlag,
		WorkflowParallelMainTree:      workflowParallelMainTreeFlag,
		LiveCompactMode:               liveCompactModeFlag,
		LiveCompactKeepRounds:         liveCompactKeepRoundsFlag,
		LiveCompactKeepTailTokens:     &liveCompactKeepTailTokens,
		LiveCompactMinBlockTokens:     liveCompactMinBlockTokensFlag,
		LiveCompactMinBodyBytes:       &liveCompactMinBodyBytes,
		LiveCompactMaxDriftMessages:   &liveCompactMaxDriftMessages,
		LiveCompactRailTokens:         &liveCompactRailTokens,
		LiveCompactMinFoldBytes:       &liveCompactMinFoldBytes,
		LiveCompactMaxFoldBytes:       &liveCompactMaxFoldBytes,
		LiveCompactLargeBlockTokens:   &liveCompactLargeBlockTokens,
		LiveCompactPrefixGuard:        &liveCompactPrefixGuard,
		LiveCompactPrefixUnsafeModels: &liveCompactPrefixUnsafeModels,
		LiveCompactColdFold:           &liveCompactColdFold,
		LiveCompactColdRailTokens:     &liveCompactColdRailTokens,
		LiveCompactCacheTTLSeconds:    &liveCompactCacheTTLSeconds,
		TaskAgent:                     &taskAgent,
		TaskModel:                     &taskModel,
	}); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	routerSupervisionMode, routerSupervisionRetries, routerSupervisionRunActions, routerSupervisionActions, routerSupervisionQuotaHold = routerSupervision(startupCfg)
	nodeID, nodeName := startupNodeIdentity(startupCfg)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := app.Start(ctx, *addr, app.StartOptions{
		Version:                            version,
		Args:                               os.Args[1:],
		AgentConfigPath:                    *agentConfigFlag,
		WebPushEnabled:                     *webPushFlag,
		NotifyScript:                       *notifyScriptFlag,
		Role:                               *roleFlag,
		NodeID:                             nodeID,
		NodeName:                           nodeName,
		RouterMode:                         *routerModeFlag,
		RouterAgent:                        *routerAgentFlag,
		RouterModel:                        *routerModelFlag,
		RouterEffort:                       *routerEffortFlag,
		RouterTurnTimeout:                  *routerTurnTimeoutFlag,
		RouterIdleClose:                    *routerIdleCloseFlag,
		RouterRoots:                        routerRoots(startupCfg),
		RouterSupervisionMode:              routerSupervisionMode,
		RouterSupervisionMaxRetriesPerNode: routerSupervisionRetries,
		RouterSupervisionMaxActionsPerRun:  routerSupervisionRunActions,
		RouterSupervisionMaxActionsPerHour: routerSupervisionActions,
		RouterSupervisionQuotaHoldSeconds:  routerSupervisionQuotaHold,
		TaskAgent:                          taskAgent,
		TaskModel:                          taskModel,
		WorkflowParallelMainTree:           *workflowParallelMainTreeFlag,
		LiveCompactMode:                    *liveCompactModeFlag,
		LiveCompactKeepRounds:              *liveCompactKeepRoundsFlag,
		LiveCompactKeepTailTokens:          liveCompactKeepTailTokens,
		LiveCompactMinBlockTokens:          *liveCompactMinBlockTokensFlag,
		LiveCompactMinBodyBytes:            liveCompactMinBodyBytes,
		LiveCompactMaxDriftMessages:        liveCompactMaxDriftMessages,
		LiveCompactRailTokens:              liveCompactRailTokens,
		LiveCompactMinFoldBytes:            liveCompactMinFoldBytes,
		LiveCompactMaxFoldBytes:            liveCompactMaxFoldBytes,
		LiveCompactLargeBlockTokens:        liveCompactLargeBlockTokens,
		LiveCompactPrefixGuard:             &liveCompactPrefixGuard,
		LiveCompactPrefixUnsafeModels:      liveCompactPrefixUnsafeModels,
		LiveCompactColdFold:                &liveCompactColdFold,
		LiveCompactColdRailTokens:          liveCompactColdRailTokens,
		LiveCompactCacheTTLSeconds:         liveCompactCacheTTLSeconds,
		LiveCompactHarnesses:               liveCompactHarnessOverrides(&startupCfg),
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
	Role   *string              `json:"role"`
	Router *startupRouterConfig `json:"router"`
	// Node 是本机身份。不配置 = 主控 home/主控；缺省规则与 CLI 入口共用
	// app.resolveNodeIdentity，两个二进制读同一份 config.json。
	Node *startupNodeConfig `json:"node"`
	// Task 是新任务派发默认 Agent/模型的节点本地配置。只走 config.json。
	Task     *startupTaskConfig     `json:"task"`
	Workflow *startupWorkflowConfig `json:"workflow"`
	// LiveCompact 是节点本地的上下文活压缩配置。不配置 = off。
	LiveCompact *startupLiveCompactConfig `json:"live_compact"`
}

type startupWorkflowConfig struct {
	ParallelMainTree *bool `json:"parallel_main_tree"`
}

// startupNodeConfig 是本机在节点表与前端里的身份（node.id/node.name）。
// 缺省不写文件，由 app 侧补：主控 home/主控，边缘取 control_sync.instance_id。
type startupNodeConfig struct {
	ID   *string `json:"id"`
	Name *string `json:"name"`
}

func startupNodeIdentity(cfg startupConfig) (string, string) {
	if cfg.Node == nil {
		return "", ""
	}
	var id, name string
	if cfg.Node.ID != nil {
		id = strings.TrimSpace(*cfg.Node.ID)
	}
	if cfg.Node.Name != nil {
		name = strings.TrimSpace(*cfg.Node.Name)
	}
	return id, name
}

type startupLiveCompactConfig struct {
	Mode             *string `json:"mode"`
	KeepRounds       *int    `json:"keep_rounds"`
	KeepTailTokens   *int    `json:"keep_tail_tokens"`
	MinBlockTokens   *int    `json:"min_block_tokens"`
	MinBodyBytes     *int    `json:"min_body_bytes"`
	MaxDriftMessages *int    `json:"max_drift_messages"`
	// Rail gate and fold budget (design §12.6). No CLI flags: node tuning
	// knobs, not something to carry on every command line.
	RailTokens *int `json:"rail_tokens"`
	// Deprecated: merged into keep_tail_tokens. Still parsed so an old config
	// keeps meaning what it meant, but only as a fallback when the new key is
	// absent; a deprecation line is logged on every start that uses it.
	TargetTokens     *int `json:"target_tokens"`
	MinFoldBytes     *int `json:"min_fold_bytes"`
	MaxFoldBytes     *int `json:"max_fold_bytes"`
	LargeBlockTokens *int `json:"large_block_tokens"`
	// Prefix guard and cold-cache timing (design 2026-11-20 §4/§5). Pointers so
	// "absent" keeps the default (both on) instead of reading as false.
	PrefixGuard        *bool    `json:"prefix_guard"`
	PrefixUnsafeModels []string `json:"prefix_unsafe_models"`
	ColdFold           *bool    `json:"cold_fold"`
	ColdRailTokens     *int     `json:"cold_rail_tokens"`
	CacheTTLSeconds    *int     `json:"cache_ttl_seconds"`
	// Harness 按 harness 覆盖上面的策略（`live_compact.harness.codex.*`）。
	// 只走 config.json：这是节点调参，不是每条命令都要带的东西。
	Harness map[string]startupLiveCompactHarnessConfig `json:"harness"`
}

// startupLiveCompactHarnessConfig 只写与节点值不同的字段；缺省字段沿用全局值。
type startupLiveCompactHarnessConfig struct {
	Enabled        *bool `json:"enabled"`
	KeepRounds     *int  `json:"keep_rounds"`
	KeepTailTokens *int  `json:"keep_tail_tokens"`
	MinBlockTokens *int  `json:"min_block_tokens"`
	MinBodyBytes   *int  `json:"min_body_bytes"`
	RailTokens     *int  `json:"rail_tokens"`
	// Deprecated: see startupLiveCompactConfig.TargetTokens.
	TargetTokens     *int  `json:"target_tokens"`
	MinFoldBytes     *int  `json:"min_fold_bytes"`
	MaxFoldBytes     *int  `json:"max_fold_bytes"`
	LargeBlockTokens *int  `json:"large_block_tokens"`
	ColdFold         *bool `json:"cold_fold"`
	ColdRailTokens   *int  `json:"cold_rail_tokens"`
	CacheTTLSeconds  *int  `json:"cache_ttl_seconds"`
}

type startupTaskConfig struct {
	Agent *string `json:"agent"`
	Model *string `json:"model"`
}

type startupRouterConfig struct {
	Mode        *string           `json:"mode"`
	Agent       *string           `json:"agent"`
	Model       *string           `json:"model"`
	Effort      *string           `json:"effort"`
	TurnTimeout *string           `json:"turn_timeout"`
	IdleClose   *string           `json:"idle_close"`
	Roots       map[string]string `json:"roots"`
	// Supervision 是主动监管（S0 观测 / S1 影子 / S2 有限执行）。只走 config.json。
	Supervision *startupRouterSupervisionConfig `json:"supervision"`
}

type startupRouterSupervisionConfig struct {
	Mode              *string `json:"mode"`
	MaxRetriesPerNode *int    `json:"max_retries_per_node"`
	MaxActionsPerRun  *int    `json:"max_actions_per_run"`
	MaxActionsPerHour *int    `json:"max_actions_per_hour"`
	QuotaHoldSeconds  *int    `json:"quota_hold_seconds"`
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

	// 任务派发默认值没有 CLI flag：只由 config.json 的 task 段填充。
	TaskAgent *string
	TaskModel *string

	WorkflowParallelMainTree *bool

	LiveCompactMode               *string
	LiveCompactKeepRounds         *int
	LiveCompactKeepTailTokens     *int
	LiveCompactMinBlockTokens     *int
	LiveCompactMinBodyBytes       *int
	LiveCompactMaxDriftMessages   *int
	LiveCompactRailTokens         *int
	LiveCompactMinFoldBytes       *int
	LiveCompactMaxFoldBytes       *int
	LiveCompactLargeBlockTokens   *int
	LiveCompactPrefixGuard        *bool
	LiveCompactPrefixUnsafeModels *[]string
	LiveCompactColdFold           *bool
	LiveCompactColdRailTokens     *int
	LiveCompactCacheTTLSeconds    *int
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
		// No CLI flag: the tail budget is node tuning, like the fold budget.
		if flags.LiveCompactKeepTailTokens != nil && live.KeepTailTokens != nil {
			*flags.LiveCompactKeepTailTokens = *live.KeepTailTokens
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
		if flags.LiveCompactRailTokens != nil && live.RailTokens != nil {
			*flags.LiveCompactRailTokens = *live.RailTokens
		}
		// target_tokens 已并入 keep_tail_tokens；旧键只在没写新键时按别名生效。
		if live.TargetTokens != nil {
			switch {
			case live.KeepTailTokens == nil:
				if flags.LiveCompactKeepTailTokens != nil {
					*flags.LiveCompactKeepTailTokens = *live.TargetTokens
				}
				log.Printf("[live-compact] target_tokens is deprecated; using %d as keep_tail_tokens", *live.TargetTokens)
			case *live.KeepTailTokens != *live.TargetTokens:
				log.Printf("[live-compact] target_tokens is deprecated and ignored: keep_tail_tokens=%d wins over target_tokens=%d", *live.KeepTailTokens, *live.TargetTokens)
			}
		}
		if flags.LiveCompactMinFoldBytes != nil && live.MinFoldBytes != nil {
			*flags.LiveCompactMinFoldBytes = *live.MinFoldBytes
		}
		if flags.LiveCompactMaxFoldBytes != nil && live.MaxFoldBytes != nil {
			*flags.LiveCompactMaxFoldBytes = *live.MaxFoldBytes
		}
		if flags.LiveCompactLargeBlockTokens != nil && live.LargeBlockTokens != nil {
			*flags.LiveCompactLargeBlockTokens = *live.LargeBlockTokens
		}
		if flags.LiveCompactPrefixGuard != nil && live.PrefixGuard != nil {
			*flags.LiveCompactPrefixGuard = *live.PrefixGuard
		}
		if flags.LiveCompactPrefixUnsafeModels != nil && live.PrefixUnsafeModels != nil {
			*flags.LiveCompactPrefixUnsafeModels = live.PrefixUnsafeModels
		}
		if flags.LiveCompactColdFold != nil && live.ColdFold != nil {
			*flags.LiveCompactColdFold = *live.ColdFold
		}
		if flags.LiveCompactColdRailTokens != nil && live.ColdRailTokens != nil {
			*flags.LiveCompactColdRailTokens = *live.ColdRailTokens
		}
		if flags.LiveCompactCacheTTLSeconds != nil && live.CacheTTLSeconds != nil {
			*flags.LiveCompactCacheTTLSeconds = *live.CacheTTLSeconds
		}
	}
	if task := cfg.Task; task != nil {
		if flags.TaskAgent != nil && task.Agent != nil {
			*flags.TaskAgent = strings.TrimSpace(*task.Agent)
		}
		if flags.TaskModel != nil && task.Model != nil {
			*flags.TaskModel = strings.TrimSpace(*task.Model)
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

// routerSupervision 解出 config.json 的 router.supervision 段；缺省空值 = off。
func routerSupervision(cfg startupConfig) (mode string, maxRetriesPerNode, maxActionsPerRun, maxActionsPerHour, quotaHoldSeconds int) {
	if cfg.Router == nil || cfg.Router.Supervision == nil {
		return "", 0, 0, 0, 0
	}
	sup := cfg.Router.Supervision
	if sup.Mode != nil {
		mode = strings.TrimSpace(*sup.Mode)
	}
	if sup.MaxRetriesPerNode != nil {
		maxRetriesPerNode = *sup.MaxRetriesPerNode
	}
	if sup.MaxActionsPerRun != nil {
		maxActionsPerRun = *sup.MaxActionsPerRun
	}
	if sup.MaxActionsPerHour != nil {
		maxActionsPerHour = *sup.MaxActionsPerHour
	}
	if sup.QuotaHoldSeconds != nil {
		quotaHoldSeconds = *sup.QuotaHoldSeconds
	}
	return mode, maxRetriesPerNode, maxActionsPerRun, maxActionsPerHour, quotaHoldSeconds
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

// liveCompactHarnessOverrides reads live_compact.harness.<name> from config.json.
// Absent section means every harness runs the node configuration unchanged.
func liveCompactHarnessOverrides(cfg *startupConfig) map[string]fold.HarnessOverride {
	if cfg == nil || cfg.LiveCompact == nil || len(cfg.LiveCompact.Harness) == 0 {
		return nil
	}
	out := make(map[string]fold.HarnessOverride, len(cfg.LiveCompact.Harness))
	for name, h := range cfg.LiveCompact.Harness {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		out[key] = fold.HarnessOverride{
			Enabled:          h.Enabled,
			KeepRounds:       intValue(h.KeepRounds),
			KeepTailTokens:   intValue(h.KeepTailTokens),
			MinBlockTokens:   intValue(h.MinBlockTokens),
			MinBodyBytes:     intValue(h.MinBodyBytes),
			RailTokens:       intValue(h.RailTokens),
			MinFoldBytes:     intValue(h.MinFoldBytes),
			MaxFoldBytes:     intValue(h.MaxFoldBytes),
			LargeBlockTokens: intValue(h.LargeBlockTokens),
			ColdFold:         h.ColdFold,
			ColdRailTokens:   intValue(h.ColdRailTokens),
			CacheTTLSeconds:  intValue(h.CacheTTLSeconds),
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func intValue(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
