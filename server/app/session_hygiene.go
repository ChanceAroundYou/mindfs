package app

import (
	"strings"

	"mindfs/server/internal/api/usecase"
)

// 会话卫生的默认策略。选这些数字的依据：
//
//   - 1 小时活跃（距上次压缩）：live compact 已经把「大」这一侧压住了，剩下的
//     风险是长时间对话里反复累积的碎片；1 小时是用户观察到的、还愿意为一次摘要
//     请求买单的间隔。
//   - 上下文窗口的 70%：阈值必须相对窗口算。绝对 token 数在 200k 与 272k 窗口上
//     含义完全不同 —— 同一个 140k 在 Claude 上已过半、在 Codex 上还没发力。
//     窗口未知时按 200k 基准（见 usecase.hygieneDefaultContextWindow）。
//   - 3 个连续过线回合：用户看到提示后还有机会自己决定；「静默自动」不等于
//     「立刻动手」。中断过（某一轮不再过线）就重新攒。
//   - 30 分钟空闲：挂机回来的会话不算「持续久」。
const (
	defaultHygieneActiveSeconds  = 3600
	defaultHygieneContextPercent = 70
	defaultHygieneOverLineTurns  = 3
	defaultHygieneIdleGapSeconds = 1800
)

// sessionHygieneConfigFromOptions 是 StartOptions -> usecase.HygieneConfig 的
// 唯一转换点：默认值只在这里补，tracker 与前端拿到的都是补全后的值。
func sessionHygieneConfigFromOptions(opts SessionHygieneOptions) usecase.HygieneConfig {
	cfg := usecase.HygieneConfig{
		Enabled:        true,
		ActiveSeconds:  defaultHygieneActiveSeconds,
		ContextPercent: defaultHygieneContextPercent,
		OverLineTurns:  defaultHygieneOverLineTurns,
		IdleGapSeconds: defaultHygieneIdleGapSeconds,
	}
	if opts.Enabled != nil {
		cfg.Enabled = *opts.Enabled
	}
	if opts.ActiveSeconds > 0 {
		cfg.ActiveSeconds = opts.ActiveSeconds
	}
	if opts.ContextPercent > 0 {
		cfg.ContextPercent = opts.ContextPercent
	}
	if opts.OverLineTurns > 0 {
		cfg.OverLineTurns = opts.OverLineTurns
	}
	if opts.IdleGapSeconds > 0 {
		cfg.IdleGapSeconds = opts.IdleGapSeconds
	}
	// 百分比阈值本身跨 harness 成立（各自按自己的窗口算），所以不再给 Codex
	// 单独的内置绝对阈值 —— 那正是「同一策略在两种窗口上含义不同」的来源。
	// 这里只保留用户显式写的覆盖，逐字段合并，不整体替换：只写
	// `harness.codex.over_line_turns` 的人不该顺手把 codex 的窗口比例改回全局值。
	for name, override := range opts.Harnesses {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if cfg.Harness == nil {
			cfg.Harness = map[string]usecase.HygieneConfig{}
		}
		merged := cfg.Harness[key]
		if override.ActiveSeconds > 0 {
			merged.ActiveSeconds = override.ActiveSeconds
		}
		if override.ContextPercent > 0 {
			merged.ContextPercent = override.ContextPercent
		}
		if override.OverLineTurns > 0 {
			merged.OverLineTurns = override.OverLineTurns
		}
		if override.IdleGapSeconds > 0 {
			merged.IdleGapSeconds = override.IdleGapSeconds
		}
		cfg.Harness[key] = merged
	}
	return cfg
}
