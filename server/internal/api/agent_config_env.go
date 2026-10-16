package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/apperr"
)

func normalizeAgentConfigRequest(agentName, backupName string) (string, string, string, error) {
	agentName = strings.TrimSpace(agentName)
	backupName = strings.TrimSpace(backupName)
	if agentName == "" {
		return "", "", "", apperrInvalid("请指定 agent")
	}
	if backupName == "" {
		return "", "", "", apperrInvalid("请提供备份名称")
	}
	if !agentConfigNamePattern.MatchString(backupName) || strings.Contains(backupName, "..") {
		return "", "", "", apperrInvalid("备份名称只能包含字母、数字、点、下划线和连字符")
	}
	return agentName, backupName, agentName + "-" + backupName, nil
}

func normalizeFileSources(input []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, item := range input {
		path := strings.TrimSpace(item)
		if path == "" {
			continue
		}
		path, err := expandUserPath(path)
		if err != nil {
			return nil, err
		}
		if seen[path] {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, apperr.Wrap("stat", path, err)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("config source is a directory: %s", path)
		}
		seen[path] = true
		out = append(out, path)
	}
	return out, nil
}

func existingDefaultFileSources(input []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, item := range input {
		path := strings.TrimSpace(item)
		if path == "" {
			continue
		}
		path, err := expandUserPath(path)
		if err != nil || path == "" || seen[path] {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

func normalizeEnvLines(input []string) ([]string, []string, error) {
	var lines []string
	var keys []string
	seen := map[string]bool{}
	for _, raw := range input {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		rawKey, rawValue, ok := strings.Cut(line, "=")
		key := strings.TrimSpace(rawKey)
		if !ok || key == "" {
			return nil, nil, fmt.Errorf("invalid env line: %s", line)
		}
		value := strings.TrimSpace(rawValue)
		if value == "" {
			continue
		}
		lines = append(lines, key+"="+value)
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return lines, keys, nil
}

func envLinesToMap(lines []string) (map[string]string, []string, error) {
	env := make(map[string]string, len(lines))
	var keys []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, nil, fmt.Errorf("invalid env line: %s", line)
		}
		env[key] = value
		keys = append(keys, key)
	}
	return env, keys, nil
}

func expandUserPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, strings.TrimLeft(path[1:], `/\`)), nil
}

// mutateUserAgent 读用户层、就地改指定 agent、写回用户层。
//
// 三个写入点（env / configBackup / provider switch）都走它。以前它们读的是
// LoadConfig("")（安装自带 ⊕ 用户层），改完把**整份合并结果**写回用户层，
// 于是上游定义被冻进用户层，之后改 agents.json 永远被那层旧快照盖住 ——
// dsh 的 installCommands 就这样一直停在坏版本上。用户层只该留用户自己配的东西，
// 上游那份由加载时的合并供给。
func mutateUserAgent(agentName string, mutate func(*agent.Definition)) error {
	cfg, err := agent.LoadUserConfig()
	if err != nil {
		return err
	}
	if _, ok := cfg.GetAgent(agentName); !ok {
		// 用户层里没有就先确认这名字在上游存在：否则会在用户层里造出一个
		// command 为空的幽灵 agent（补最小条目靠的就是上游供给 command）。
		effective, err := agent.LoadConfig("")
		if err != nil {
			return err
		}
		if _, ok := effective.GetAgent(agentName); !ok {
			return fmt.Errorf("agent not configured: %s", agentName)
		}
	}
	mutate(cfg.EnsureAgent(agentName))
	return agent.SaveUserConfig(cfg)
}

func updateAgentConfigDefaults(agentName string, fileSources []string, envKeys []string) error {
	return mutateUserAgent(agentName, func(def *agent.Definition) {
		def.ConfigBackup.FileSources = append([]string(nil), fileSources...)
		def.ConfigBackup.EnvKeys = append([]string(nil), envKeys...)
	})
}

func updateAgentEnvConfig(agentName string, env map[string]string) error {
	return mutateUserAgent(agentName, func(def *agent.Definition) {
		def.Env = cloneStringMap(env)
	})
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
