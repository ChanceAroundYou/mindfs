package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	types "mindfs/server/internal/agent/types"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

func isClaudeAliasModel(model string) bool {
	base := strip1MSuffix(model)
	lower := strings.ToLower(strings.TrimSpace(base))
	switch lower {
	case "fable", "opus", "sonnet", "haiku", "of", "op", "os", "ok", "default":
		return true
	default:
		return false
	}
}

func resolveClaudeBaseAlias(base string) string {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	switch lower {
	case "fable":
		return "of"
	case "opus":
		return "op"
	case "sonnet":
		return "os"
	case "haiku":
		return "ok"
	case "of", "op", "os", "ok":
		return lower
	default:
		return trimmed
	}
}

func canonicalClaudeModel(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}
	if strings.EqualFold(trimmed, "default") {
		return trimmed
	}
	if !isClaudeAliasModel(trimmed) {
		return trimmed
	}
	has := has1MSuffix(trimmed)
	base := strip1MSuffix(trimmed)
	alias := resolveClaudeBaseAlias(base)
	if alias == "" {
		return ""
	}
	if has {
		return alias + "[1m]"
	}
	return alias
}

func strip1MSuffix(model string) string {
	trimmed := strings.TrimSpace(model)
	if len(trimmed) >= 4 && strings.EqualFold(trimmed[len(trimmed)-4:], "[1m]") {
		return strings.TrimSpace(trimmed[:len(trimmed)-4])
	}
	return trimmed
}

func has1MSuffix(model string) bool {
	trimmed := strings.TrimSpace(model)
	return len(trimmed) >= 4 && strings.EqualFold(trimmed[len(trimmed)-4:], "[1m]")
}

// Strip1MSuffix 剥除模型名的 [1m]/[1M] 后缀（导出供 agent 包复用）。
func Strip1MSuffix(model string) string { return strip1MSuffix(model) }

// Has1MSuffix 判断模型名是否带 [1m]/[1M] 后缀（导出供 agent 包复用）。
func Has1MSuffix(model string) bool { return has1MSuffix(model) }

// TierKey 将模型名归一并忽略 [1m]/[1M] 后缀，映射到 Anthropic 家族短 key
// （fable/of→of、opus/op→op、sonnet/os→os、haiku/ok→ok，其余小写原样）。
// 用于探测目录去重与默认模型归属比对，避免 os[1m] 与 os 被误判为不同模型。
func TierKey(id string) string {
	base := strip1MSuffix(id)
	lower := strings.ToLower(strings.TrimSpace(base))
	switch lower {
	case "fable":
		return "of"
	case "opus":
		return "op"
	case "sonnet":
		return "os"
	case "haiku":
		return "ok"
	case "of", "op", "os", "ok":
		return lower
	default:
		return strings.ToLower(strings.TrimSpace(base))
	}
}

// ClaudeSettingsPath 返回 Claude Code 的 settings.json 路径（cc-switch 的 provider
// 切换写入点）。优先 $CLAUDE_CONFIG_DIR/settings.json，否则 $HOME/.claude/settings.json。
func ClaudeSettingsPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func with1MSuffix(model string, enabled bool) string {
	base := strip1MSuffix(model)
	if base == "" {
		return ""
	}
	if isClaudeAliasModel(base) {
		alias := resolveClaudeBaseAlias(base)
		if alias == "" {
			return ""
		}
		if enabled {
			return alias + "[1m]"
		}
		return alias
	}
	if enabled {
		return base + "[1m]"
	}
	return base
}

// readSettingsEnv 读取单个 settings 的 {"env":{...}}，不存在/失败返回 nil。
func readSettingsEnv(path string) map[string]string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(payload, &settings); err != nil {
		return nil
	}
	return settings.Env
}

// claudeSettingsPathOrEmpty 容错版 ClaudeSettingsPath，错误时返回空串（调用方跳过）。
func claudeSettingsPathOrEmpty() string {
	path, err := ClaudeSettingsPath()
	if err != nil {
		return ""
	}
	return path
}

// ClaudeEffectiveEnv 按 Claude Code 的 env 分层叠加：
// os.Environ() → baseEnv → 用户 settings → 项目 <rootDir>/.claude/settings.json
// （若 rootDir 非空且文件存在）→ managed（本实现中与用户 settings 同层，无独立文件）。
// 后者覆盖前者。导出供 usecase 展示链路复用同一分层。
func ClaudeEffectiveEnv(baseEnv map[string]string, rootDir string) map[string]string {
	return claudeEffectiveEnv(baseEnv, rootDir)
}

func claudeEffectiveEnv(baseEnv map[string]string, rootDir string) map[string]string {
	merged := map[string]string{}
	for _, kv := range os.Environ() {
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			merged[kv[:idx]] = kv[idx+1:]
		}
	}
	for k, v := range baseEnv {
		merged[k] = v
	}
	// 用户 settings（cc-switch 的 provider/模型切换写入点）覆盖上层。
	if userEnv := readSettingsEnv(claudeSettingsPathOrEmpty()); userEnv != nil {
		for k, v := range userEnv {
			merged[k] = v
		}
	}
	// 项目 settings 覆盖用户 settings。
	if root := strings.TrimSpace(rootDir); root != "" {
		projectPath := filepath.Join(root, ".claude", "settings.json")
		if projectEnv := readSettingsEnv(projectPath); projectEnv != nil {
			for k, v := range projectEnv {
				merged[k] = v
			}
		}
	}
	// managed settings 与用户 settings 同层（无独立文件），已随用户 settings 应用。
	return merged
}

// displayToTier 将 display 短名映射回 CLI 内建 tier 名：
// of→fable、op→opus、os→sonnet、ok→haiku，其余原样（小写）。
func displayToTier(display string) string {
	lower := strings.ToLower(strings.TrimSpace(display))
	switch lower {
	case "of":
		return "fable"
	case "op":
		return "opus"
	case "os":
		return "sonnet"
	case "ok":
		return "haiku"
	default:
		return lower
	}
}

// claudeModelAliasForCLI converts MindFS's internal family IDs to Claude Code's
// accepted family aliases. Those short IDs can appear in model-pin env vars when
// MindFS IDs were copied into Claude settings (for example of[1M]); they are not
// model IDs that Claude Code can validate with the provider.
func claudeModelAliasForCLI(model string) string {
	model = strings.TrimSpace(model)
	has := has1MSuffix(model)
	base := strip1MSuffix(model)
	if !isClaudeAliasModel(base) || strings.EqualFold(base, "default") {
		return model
	}
	alias := displayToTier(base)
	if has {
		return alias + "[1m]"
	}
	return alias
}

// applyToggle 按 has 标志返回模型名：开则保留/追加 [1m]，关则剥后缀。
func applyToggle(value string, has bool) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	stripped := strip1MSuffix(value)
	if has {
		if has1MSuffix(value) {
			return value
		}
		return stripped + "[1m]"
	}
	return stripped
}

// ResolveClaudeModelArg 是发送边界的单一模型名解析器：把存储用的 display 短名
// （os[1m]/d4p 等）在 spawn CLI 时解析成 env 里的真实模型名。
//   - model=="" → ""（OpenSession 不附加 --model）
//   - 剥 [1m]（EqualFold 识别 [1M]），记录 toggle
//   - default：查 ANTHROPIC_MODEL；空→带 toggle 的 default；alias→递归一次并透传 toggle；
//     否则（如 deepseek-v4-pro）按 toggle 返回原样/剥后缀。防止 default→default 死循环。
//   - alias（of/fable/...）：displayToTier→查 ANTHROPIC_DEFAULT_<TIER>_MODEL；
//     env 有值按 toggle 返回（含其后缀），无值回退 tier 名本身带 toggle。
//   - 非 alias（第三方如 d4p/deepseek-v4-pro）：按 toggle 透传。
//   - env 查找 key 大写，比较用归一小写，返回 env 值保持原样（不 lowercase）。
func ResolveClaudeModelArg(model string, env map[string]string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	has := has1MSuffix(model)
	base := strip1MSuffix(model)
	return resolveClaudeModelArg(base, env, has)
}

// resolveClaudeModelArg 是 ResolveClaudeModelArg 的核心，has 表示调用方传入的
// [1m] toggle 标志，递归（default→ANTHROPIC_MODEL）时透传以保持 env 值的原样后缀。
func resolveClaudeModelArg(base string, env map[string]string, has bool) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}

	if strings.EqualFold(base, "default") {
		envVal := strings.TrimSpace(envLookup(env, "ANTHROPIC_MODEL"))
		if envVal == "" || strings.EqualFold(strip1MSuffix(envVal), "default") {
			if has {
				return "default[1m]"
			}
			return "default"
		}
		// 递归一次，透传 toggle（防 default→default 死循环由上面的 envVal 检查拦截）。
		return resolveClaudeModelArg(envVal, env, has)
	}

	if isClaudeAliasModel(base) {
		tier := displayToTier(base) // fable/opus/sonnet/haiku
		envKey := "ANTHROPIC_DEFAULT_" + strings.ToUpper(tier) + "_MODEL"
		envVal := strings.TrimSpace(envLookup(env, envKey))
		if envVal != "" {
			return applyToggle(claudeModelAliasForCLI(envVal), has)
		}
		return applyToggle(tier, has)
	}

	// 第三方透传
	return applyToggle(base, has)
}

// envLookup 从 env map 读大写 key（env 已归一为大写 key）。
func envLookup(env map[string]string, key string) string {
	if env == nil {
		return ""
	}
	return env[key]
}

// cloneEnv 拷贝 env map，避免复用外部调用方的底层 map。
func cloneEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}

// resolveClaudeModelArg 用本会话的 rootPath/baseEnv 构造有效 env 后解析模型名。

func claudeModelInfo(model claudeagent.ModelInfo) types.ModelInfo {
	name := strings.TrimSpace(model.DisplayName)
	if name == "" {
		name = strings.TrimSpace(model.Value)
	}
	return types.ModelInfo{
		ID:            model.Value,
		Name:          name,
		Description:   model.Description,
		SupportEffort: true,
		Efforts:       claudeEffortLevels(),
	}
}

func claudeEffortLevels() []string {
	return []string{"low", "medium", "high", "xhigh", "max"}
}
