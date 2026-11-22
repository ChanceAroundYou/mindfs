package app

import (
	"strings"

	"mindfs/server/internal/api/usecase"
)

// 会话卫生的默认策略。选这些数字的依据：
//
//   - 2 小时活跃：一次专注的深聊通常在 1–3 小时；低于 2 小时的会话压缩收益
//     抵不过一次摘要请求。
//   - 140k / 180k 上下文：两家 CLI 的自动压缩都在真实上下文 ~167k（100k rail
//     折叠之后）附近触发，留出余量让我们先提醒、再由提示转自动。
//   - 3 轮：用户看到提示后还有三次说话机会自己决定；「静默自动」不等于「立刻
//     动手」。
//   - 30 分钟空闲：挂机回来的会话不算「持续久」。
const (
	defaultHygieneActiveSeconds  = 2 * 3600
	defaultHygieneContextTokens  = 140000
	defaultHygieneCodexTokens    = 180000
	defaultHygieneHintTurns      = 3
	defaultHygieneIdleGapSeconds = 1800
)

// sessionHygieneConfigFromOptions 是 StartOptions -> usecase.HygieneConfig 的
// 唯一转换点：默认值只在这里补，tracker 与前端拿到的都是补全后的值。
func sessionHygieneConfigFromOptions(opts SessionHygieneOptions) usecase.HygieneConfig {
	cfg := usecase.HygieneConfig{
		Enabled:        true,
		ActiveSeconds:  defaultHygieneActiveSeconds,
		ContextTokens:  defaultHygieneContextTokens,
		HintTurns:      defaultHygieneHintTurns,
		IdleGapSeconds: defaultHygieneIdleGapSeconds,
	}
	if opts.Enabled != nil {
		cfg.Enabled = *opts.Enabled
	}
	if opts.ActiveSeconds > 0 {
		cfg.ActiveSeconds = opts.ActiveSeconds
	}
	if opts.ContextTokens > 0 {
		cfg.ContextTokens = opts.ContextTokens
	}
	if opts.HintTurns > 0 {
		cfg.HintTurns = opts.HintTurns
	}
	if opts.IdleGapSeconds > 0 {
		cfg.IdleGapSeconds = opts.IdleGapSeconds
	}
	// Codex 的窗口比 Claude 大：同一个全局阈值会让它更早提示，且默认值应是
	// 各自压缩点前一点。显式配置永远覆盖这一条。
	if _, ok := opts.Harnesses["codex"]; !ok {
		cfg.Harness = map[string]usecase.HygieneConfig{
			"codex": {ContextTokens: defaultHygieneCodexTokens},
		}
	}
	for name, override := range opts.Harnesses {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if cfg.Harness == nil {
			cfg.Harness = map[string]usecase.HygieneConfig{}
		}
		// 在既有覆盖（例如 codex 的内置 180k）上逐字段覆盖，而不是整体替换：
		// 只写 `harness.codex.hint_turns` 的人不该顺手把 codex 的上下文阈值
		// 悄悄改回全局值。
		merged := cfg.Harness[key]
		if override.ActiveSeconds > 0 {
			merged.ActiveSeconds = override.ActiveSeconds
		}
		if override.ContextTokens > 0 {
			merged.ContextTokens = override.ContextTokens
		}
		if override.HintTurns > 0 {
			merged.HintTurns = override.HintTurns
		}
		if override.IdleGapSeconds > 0 {
			merged.IdleGapSeconds = override.IdleGapSeconds
		}
		cfg.Harness[key] = merged
	}
	return cfg
}
