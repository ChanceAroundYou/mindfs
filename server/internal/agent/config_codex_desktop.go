package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const stalePluginCodexMarker = "/.codex/plugins/.plugin-appserver/"

// isStalePluginCodexCommand 认出旧的插件版 codex（Windows 上实测是 0.166），
// 路径分隔符和大小写都归一后再比，避免 C:\Users\... 与 C:/Users/... 漏判。
func isStalePluginCodexCommand(command string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(command), `\`, `/`))
	return strings.Contains(normalized, stalePluginCodexMarker)
}

// windowsDesktopCodexPath 找 WindowsApps 里 OpenAI.Codex 桌面端随包带的 codex.exe。
// MSIX 目录名带版本号，取字典序最大的那个；找不到就返回空串让调用方回退 PATH。
func windowsDesktopCodexPath(programFiles string) string {
	if strings.TrimSpace(programFiles) == "" {
		return ""
	}
	pattern := filepath.Join(programFiles, "WindowsApps", "OpenAI.Codex_*", "app", "resources", "codex.exe")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	for i := len(matches) - 1; i >= 0; i-- {
		info, err := os.Stat(matches[i])
		if err == nil && !info.IsDir() {
			return matches[i]
		}
	}
	return ""
}

// preferWindowsDesktopCodex 在 Windows 上把 codex 指向桌面端内置的 CLI。
// 用户显式指定的其它 codex 不动；只有默认值、PATH 写法和旧插件路径会被替换。
// 桌面端不存在时，旧插件路径也降级成 PATH 写法，确保 0.166 不再被用。
func preferWindowsDesktopCodex(cfg *Config, goos, programFiles string) {
	if cfg == nil || goos != "windows" {
		return
	}
	desktop := windowsDesktopCodexPath(programFiles)
	for i := range cfg.Agents {
		agent := &cfg.Agents[i]
		if agent.Name != "codex" || agent.Protocol != ProtocolCodexSDK {
			continue
		}
		command := strings.TrimSpace(agent.Command)
		if desktop != "" {
			if command == "" || command == "codex" || isStalePluginCodexCommand(command) {
				agent.Command = desktop
			}
			continue
		}
		if isStalePluginCodexCommand(command) {
			agent.Command = "codex"
		}
	}
}

func programFilesDir() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	if value := strings.TrimSpace(os.Getenv("ProgramFiles")); value != "" {
		return value
	}
	return `C:\Program Files`
}

// finalizeAgentConfig 在配置合并完成后统一应用平台相关的默认值。
func finalizeAgentConfig(cfg Config) Config {
	preferWindowsDesktopCodex(&cfg, runtime.GOOS, programFilesDir())
	return cfg
}
