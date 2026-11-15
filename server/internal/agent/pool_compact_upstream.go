package agent

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// resolveClaudeUpstreamBaseURL 返回「不插代理时 CLI 实际会连的上游 base URL」。
//
// 为什么不能只看 def.Env：本机 claude 的 base URL 写在 `~/.claude/settings.json`
// 的 `env` 里，而 Claude Code 在启动后用 settings env **覆盖进程环境**（实测：
// 把进程环境指到不可达端口，CLI 仍走 settings 里的地址并成功回包）。所以
//   - 代理商必须转发到 settings 里那个地址（不是 def.Env / 进程环境）；
//   - 真正让 CLI 改道的也只有更高优先级的 `--settings`，见 compactProxyEnv。
//
// configDir 非空时（Router 会话）只看该目录：它刻意隔离用户 settings，
// 不允许回落到 `~/.claude`。
func resolveClaudeUpstreamBaseURL(defEnv map[string]string, configDir string) string {
	for _, path := range claudeSettingsPaths(configDir) {
		if base := claudeSettingsEnvBaseURL(path); base != "" {
			return base
		}
	}
	if base := strings.TrimSpace(defEnv["ANTHROPIC_BASE_URL"]); base != "" {
		return base
	}
	return strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL"))
}

func claudeSettingsPaths(configDir string) []string {
	if dir := strings.TrimSpace(configDir); dir != "" {
		return []string{filepath.Join(dir, "settings.json")}
	}
	var paths []string
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		paths = append(paths, filepath.Join(dir, "settings.json"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".claude", "settings.json"))
	}
	return paths
}

// claudeSettingsEnvBaseURL 只读 env.ANTHROPIC_BASE_URL 一个键，其余字段一律不碰。
func claudeSettingsEnvBaseURL(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return ""
	}
	return strings.TrimSpace(settings.Env["ANTHROPIC_BASE_URL"])
}

// clampUpstreamForLog 只保留 scheme+host：base URL 可能带查询串或路径里的租户段。
func clampUpstreamForLog(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "<unparsable>"
	}
	return parsed.Scheme + "://" + parsed.Host
}
