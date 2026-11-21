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
	"time"

	"mindfs/server/app"
)

type startupConfig struct {
	Addr        *string `json:"addr"`
	WebPush     *bool   `json:"webPush"`
	WebPushFlag *bool   `json:"web-push"`
	// WebPushRelayURL 是「把本节点通知交给控制节点代发」的地址，只有 worker 需要。
	// 只在 config.json 里配：它是每台机器的部署事实，不适合跟着命令行到处走。
	WebPushRelayURL *string `json:"web_push_relay_url"`
	Foreground      *bool   `json:"foreground"`
	TLS             *bool   `json:"tls"`
	Cert            *string `json:"cert"`
	Key             *string `json:"key"`
	AgentConfig     *string `json:"agent-config"`
	NotifyScript    *string `json:"notify-script"`
	// Role 决定这台机器提不提供控制面与前端；worker 只提供数据面。
	// 不配置 = control = 改造前的行为。
	Role   *string              `json:"role"`
	Router *startupRouterConfig `json:"router"`
	// Task 是新任务派发默认 Agent/模型的节点本地配置。只走 config.json：
	// 它是每台机器的部署事实（装了什么 Agent），不做命令行 flag。
	Task     *startupTaskConfig     `json:"task"`
	Workflow *startupWorkflowConfig `json:"workflow"`
	// LiveCompact 是节点本地的上下文活压缩配置。不配置 = off。
	LiveCompact *startupLiveCompactConfig `json:"live_compact"`
	// Channels 是外部通道（飞书等）的本机配置。不配置 = 全关。
	Channels *startupChannelsConfig `json:"channels"`
	// ControlSync 是控制面同步（离线优先本地控制面）。不配置 = 关闭。
	ControlSync *startupControlSyncConfig `json:"control_sync"`
}

// startupControlSyncConfig 是 control_sync 段。
//
// 同步通道搬运的整份控制面里有口令 hash，所以 token 只认 token_file：不提供内联
// token 字段，也不做 flag —— 密钥不该出现在 config.json 或进程参数里。
type startupControlSyncConfig struct {
	Enabled    *bool   `json:"enabled"`
	Role       *string `json:"role"` // primary（默认）| edge
	InstanceID *string `json:"instance_id"`
	PrimaryURL *string `json:"primary_url"`
	CAFile     *string `json:"ca_file"`
	TokenFile  *string `json:"token_file"`
}

// startupLiveCompactConfig 只影响本机，不走控制面共享。mode 默认 off；
// shadow 只测量不生效，on 在 Phase B1 之后才会改写请求。
type startupLiveCompactConfig struct {
	Mode             *string `json:"mode"`
	KeepRounds       *int    `json:"keep_rounds"`
	KeepTailTokens   *int    `json:"keep_tail_tokens"`
	MinBlockTokens   *int    `json:"min_block_tokens"`
	MinBodyBytes     *int    `json:"min_body_bytes"`
	MaxDriftMessages *int    `json:"max_drift_messages"`
	// Rail gate and fold budget (design §12.6). No CLI flags: these are node
	// tuning knobs, not something to carry on every command line.
	RailTokens       *int `json:"rail_tokens"`
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
	Harness map[string]startupLiveCompactHarnessConfig `json:"harness"`
}

// startupLiveCompactHarnessConfig 只写与节点值不同的字段；缺省字段沿用全局值。
type startupLiveCompactHarnessConfig struct {
	Enabled          *bool `json:"enabled"`
	KeepRounds       *int  `json:"keep_rounds"`
	KeepTailTokens   *int  `json:"keep_tail_tokens"`
	MinBlockTokens   *int  `json:"min_block_tokens"`
	MinBodyBytes     *int  `json:"min_body_bytes"`
	RailTokens       *int  `json:"rail_tokens"`
	TargetTokens     *int  `json:"target_tokens"`
	MinFoldBytes     *int  `json:"min_fold_bytes"`
	MaxFoldBytes     *int  `json:"max_fold_bytes"`
	LargeBlockTokens *int  `json:"large_block_tokens"`
	ColdFold         *bool `json:"cold_fold"`
	ColdRailTokens   *int  `json:"cold_rail_tokens"`
	CacheTTLSeconds  *int  `json:"cache_ttl_seconds"`
}

// startupWorkflowConfig 是工作流执行的节点本地开关。不配置 = 默认值（并行开启）。
type startupWorkflowConfig struct {
	// ParallelMainTree 控制没有独立 worktree 的任务是否共用主树并行写。
	ParallelMainTree *bool `json:"parallel_main_tree"`
}

// startupTaskConfig 是新任务派发的默认 Agent/模型。不配置时由 app 归一化为
// claude + sonnet。
type startupTaskConfig struct {
	Agent *string `json:"agent"`
	Model *string `json:"model"`
}

// startupRouterConfig 是 Task Router 的节点本地配置。它与 agent-config 同类：
// 只影响本机，不走控制面共享。生产不配置 = mode off。
type startupRouterConfig struct {
	Mode        *string           `json:"mode"`
	Agent       *string           `json:"agent"`
	Model       *string           `json:"model"`
	Effort      *string           `json:"effort"`
	TurnTimeout *string           `json:"turn_timeout"`
	IdleClose   *string           `json:"idle_close"`
	Roots       map[string]string `json:"roots"`
	// Supervision 是主动监管（S0 观测 / S1 影子 / S2 有限执行）。只走 config.json：
	// 预算是节点本地的部署事实，不做命令行 flag。
	Supervision *startupRouterSupervisionConfig `json:"supervision"`
}

// startupRouterSupervisionConfig 的零值 = off + 默认预算（由 kanban 归一化兜底）。
type startupRouterSupervisionConfig struct {
	Mode              *string `json:"mode"`
	MaxRetriesPerNode *int    `json:"max_retries_per_node"`
	MaxActionsPerRun  *int    `json:"max_actions_per_run"`
	MaxActionsPerHour *int    `json:"max_actions_per_hour"`
	QuotaHoldSeconds  *int    `json:"quota_hold_seconds"`
}

// startupFlags 收拢 applyStartupConfig 的操作对象。之前是 11 个位置参数，
// 每加一个配置就要改签名和所有调用点；结构体只增字段，调用点不动。
type startupFlags struct {
	Addr         *string
	WebPush      *bool
	Foreground   *bool
	TLS          *bool
	Cert         *string
	Key          *string
	AgentConfig  *string
	NotifyScript *string
	// WebPushRelayURL 没有对应 flag：只在 config.json 里配（见 startupConfig）。
	WebPushRelayURL *string
	Role            *string

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
	LiveCompactTargetTokens       *int
	LiveCompactMinFoldBytes       *int
	LiveCompactMaxFoldBytes       *int
	LiveCompactLargeBlockTokens   *int
	LiveCompactPrefixGuard        *bool
	LiveCompactPrefixUnsafeModels *[]string
	LiveCompactColdFold           *bool
	LiveCompactColdRailTokens     *int
	LiveCompactCacheTTLSeconds    *int

	// 飞书通道没有 CLI flag：只由 config.json 的 channels.feishu 段填充。
	FeishuEnabled      *bool
	FeishuOwnerOpenID  *string
	FeishuLarkCLI      *string
	FeishuCardTTLHours *int
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

// applyStartupConfig 把文件配置落到 flag 变量上，显式 flag 永远胜出。
// 只有「文件值即将生效但解析不了」才返回错误；被 flag 覆盖的坏值不报错，
// 因为它不会影响运行结果。
func applyStartupConfig(cfg startupConfig, explicit map[string]bool, flags startupFlags) error {
	if flags.Addr != nil && cfg.Addr != nil && !explicit["addr"] {
		*flags.Addr = strings.TrimSpace(*cfg.Addr)
	}
	if flags.WebPush != nil {
		if value := firstBool(cfg.WebPush, cfg.WebPushFlag); value != nil && !explicit["web-push"] {
			*flags.WebPush = *value
		}
	}
	if flags.Foreground != nil && cfg.Foreground != nil && !explicit["foreground"] {
		*flags.Foreground = *cfg.Foreground
	}
	if flags.TLS != nil && cfg.TLS != nil && !explicit["tls"] {
		*flags.TLS = *cfg.TLS
	}
	if flags.Cert != nil && cfg.Cert != nil && !explicit["cert"] {
		*flags.Cert = strings.TrimSpace(*cfg.Cert)
	}
	if flags.Key != nil && cfg.Key != nil && !explicit["key"] {
		*flags.Key = strings.TrimSpace(*cfg.Key)
	}
	if flags.AgentConfig != nil && cfg.AgentConfig != nil && !explicit["agent-config"] {
		*flags.AgentConfig = strings.TrimSpace(*cfg.AgentConfig)
	}
	if flags.NotifyScript != nil && cfg.NotifyScript != nil && !explicit["notify-script"] {
		*flags.NotifyScript = strings.TrimSpace(*cfg.NotifyScript)
	}
	if flags.WebPushRelayURL != nil && cfg.WebPushRelayURL != nil {
		*flags.WebPushRelayURL = strings.TrimSpace(*cfg.WebPushRelayURL)
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
		// These two have no CLI flag: they are tuning knobs for a node's
		// config.json, not something to carry on every command line.
		if flags.LiveCompactMinBodyBytes != nil && live.MinBodyBytes != nil {
			*flags.LiveCompactMinBodyBytes = *live.MinBodyBytes
		}
		if flags.LiveCompactMaxDriftMessages != nil && live.MaxDriftMessages != nil {
			*flags.LiveCompactMaxDriftMessages = *live.MaxDriftMessages
		}
		if flags.LiveCompactRailTokens != nil && live.RailTokens != nil {
			*flags.LiveCompactRailTokens = *live.RailTokens
		}
		if flags.LiveCompactTargetTokens != nil && live.TargetTokens != nil {
			*flags.LiveCompactTargetTokens = *live.TargetTokens
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
	// Channels 与 router 无关，必须在那条提前 return 之前落地。
	applyFeishuConfig(cfg, explicit, flags)
	router := cfg.Router
	if task := cfg.Task; task != nil {
		if flags.TaskAgent != nil && task.Agent != nil {
			*flags.TaskAgent = strings.TrimSpace(*task.Agent)
		}
		if flags.TaskModel != nil && task.Model != nil {
			*flags.TaskModel = strings.TrimSpace(*task.Model)
		}
	}
	if router == nil {
		return nil
	}
	setRouterString(flags.RouterMode, router.Mode, explicit["router-mode"], "router.mode")
	setRouterString(flags.RouterAgent, router.Agent, explicit["router-agent"], "router.agent")
	setRouterString(flags.RouterModel, router.Model, explicit["router-model"], "router.model")
	setRouterString(flags.RouterEffort, router.Effort, explicit["router-effort"], "router.effort")
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

func setRouterString(dst *string, value *string, explicit bool, field string) {
	if dst == nil || value == nil || explicit {
		return
	}
	*dst = strings.TrimSpace(*value)
}

// parseStartupDuration 接受 Go duration 字符串（"20s"、"30m"）。
// 空值 = 未配置，返回 nil 让下游用默认值。
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

// routerSupervision 解出 config.json 的 router.supervision 段。缺省返回空值，
// 由 app/kanban 归一化成 off + 默认预算（2 次重试/节点、6 个动作/小时）。
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

// routerRoots 把 config.json 的 roots 覆盖表原样交给 app 归一化。
// flags 没有 roots（按项目开关不适合命令行长期携带）；空表返回 nil。
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

// liveCompactHarnessOverrides 把 `live_compact.harness.<name>` 转成 fold 内核的
// 覆盖表。`make build` 装的是本命令（不是 server/cmd/mindfs-server），所以这条
// 转换必须落在这里：少了它，harness 段会被解析后静默丢掉，Codex 只会按节点值折叠
// （实测症状是 codex 请求一路 gate=below-rail，胶囊永远 0 条）。
func liveCompactHarnessOverrides(cfg startupConfig) map[string]app.LiveCompactHarnessOverride {
	if cfg.LiveCompact == nil || len(cfg.LiveCompact.Harness) == 0 {
		return nil
	}
	out := make(map[string]app.LiveCompactHarnessOverride, len(cfg.LiveCompact.Harness))
	for name, h := range cfg.LiveCompact.Harness {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		out[key] = app.LiveCompactHarnessOverride{
			Enabled:          h.Enabled,
			KeepRounds:       intValue(h.KeepRounds),
			KeepTailTokens:   intValue(h.KeepTailTokens),
			MinBlockTokens:   intValue(h.MinBlockTokens),
			MinBodyBytes:     intValue(h.MinBodyBytes),
			RailTokens:       intValue(h.RailTokens),
			TargetTokens:     intValue(h.TargetTokens),
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
