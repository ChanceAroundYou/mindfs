package codexsync

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	localConfigBegin = "# >>> sync-codex local (not shared) >>>"
	localConfigEnd   = "# <<< sync-codex local (not shared) <<<"
)

func renderRuntime(paths Paths) error {
	return renderRuntimeWithPolicy(paths, false)
}

func renderRuntimeRequired(paths Paths) error {
	return renderRuntimeWithPolicy(paths, true)
}

func renderRuntimeWithPolicy(paths Paths, requireSharedDefault bool) error {
	payload, mode, err := buildRuntimeConfig(paths, true, requireSharedDefault)
	if err != nil || payload == nil {
		return err
	}
	if filesEqual(paths.RuntimeConfig, payload) {
		return nil
	}
	if err := saveRollbackCopy(paths.StateDir, paths.RuntimeConfig); err != nil {
		return err
	}
	if err := atomicWriteFile(paths.RuntimeConfig, payload, mode); err != nil {
		return fmt.Errorf("atomically replace local Codex config: %w", err)
	}
	return nil
}

func buildRuntimeConfig(paths Paths, persistActive, requireSharedDefault bool) ([]byte, os.FileMode, error) {
	shared, err := readOptional(paths.SharedDefault)
	if err != nil {
		return nil, 0, err
	}
	if shared == nil && requireSharedDefault {
		return nil, 0, errors.New("shared default Codex profile is missing")
	}
	if shared != nil {
		if err := ValidateSharedProfile("default", shared); err != nil {
			return nil, 0, err
		}
	}
	active, err := activeProfileForRender(paths, persistActive)
	if err != nil {
		return nil, 0, err
	}
	if shared == nil {
		if active == "llmux" {
			return nil, 0, errors.New("shared default Codex profile is missing")
		}
		return nil, 0, nil
	}
	if info, err := os.Lstat(paths.RuntimeConfig); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, 0, errors.New("runtime Codex config is a symlink; refusing to replace an unowned target")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, 0, err
	}
	local, err := readOptional(paths.RuntimeConfig)
	if err != nil {
		return nil, 0, fmt.Errorf("read local Codex config: %w", err)
	}
	prefix := strings.TrimRight(string(shared), "\n")
	if active == "llmux" {
		profile, err := readOptional(paths.SharedLLMux)
		if err != nil {
			return nil, 0, err
		}
		if profile == nil {
			return nil, 0, errors.New("shared llmux Codex profile is missing")
		}
		if err := ValidateSharedProfile("llmux", profile); err != nil {
			return nil, 0, err
		}
		prefix = mergeSharedProfiles(shared, profile)
	}
	overlay, err := extractLocalConfigOverlay(shared, local, active)
	if err != nil {
		return nil, 0, err
	}
	// Match claude/scripts/sync-codex.sh byte shape so its read-only drift check
	// continues to recognize the generated config on every device.
	payload := []byte(prefix + "\n" + localConfigBegin + "\n" + overlay + localConfigEnd + "\n")
	if err := ValidateRenderedConfig(payload); err != nil {
		return nil, 0, err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(paths.RuntimeConfig); err == nil {
		mode = info.Mode().Perm()
	}
	return payload, mode, nil
}

func mergeSharedProfiles(base, overlay []byte) string {
	root, _ := decodeTOML(overlay)
	dropKeys := make(map[string]bool, len(root))
	for key := range root {
		dropKeys[key] = true
	}
	return strings.TrimRight(stripTOMLKeys(string(base), dropKeys), "\n") + "\n" + strings.TrimRight(string(overlay), "\n")
}

func stripTOMLKeys(raw string, drop map[string]bool) string {
	var output strings.Builder
	section := ""
	dropSection := false
	for _, line := range strings.SplitAfter(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			section = tomlSectionName(trimmed)
			dropSection = false
			for key := range drop {
				if section == key || strings.HasPrefix(section, key+".") {
					dropSection = true
					break
				}
			}
			if !dropSection {
				output.WriteString(line)
			}
			continue
		}
		if dropSection {
			continue
		}
		if section == "" {
			if drop[tomlAssignmentKey(trimmed)] {
				continue
			}
		}
		output.WriteString(line)
	}
	return output.String()
}

func RenderRuntime() error {
	paths, err := ResolvePaths()
	if err != nil {
		return err
	}
	return RenderRuntimeAt(paths)
}

func RenderRuntimeAt(paths Paths) error {
	return WithSyncLock(paths, func() error { return renderRuntimeRequired(paths) })
}

func CheckRuntime() error {
	paths, err := ResolvePaths()
	if err != nil {
		return err
	}
	return CheckRuntimeAt(paths)
}

func CheckRuntimeAt(paths Paths) error {
	payload, _, err := buildRuntimeConfig(paths, false, true)
	if err != nil {
		return err
	}
	if payload == nil {
		return errors.New("shared default Codex profile is missing")
	}
	if !filesEqual(paths.RuntimeConfig, payload) {
		return errors.New("local Codex runtime config differs from the active shared profile")
	}
	return nil
}

func extractLocalConfigOverlay(shared, current []byte, active string) (string, error) {
	sharedRoot, err := decodeTOML(shared)
	if err != nil {
		return "", err
	}
	var currentRoot map[string]any
	if len(bytes.TrimSpace(current)) > 0 {
		currentRoot, err = decodeTOML(current)
		if err != nil {
			return "", fmt.Errorf("local Codex config is invalid: %w", err)
		}
	}
	lines := strings.SplitAfter(string(current), "\n")
	begin, end := -1, -1
	beginCount, endCount := 0, 0
	for index, line := range lines {
		switch strings.TrimSpace(line) {
		case localConfigBegin:
			begin, beginCount = index, beginCount+1
		case localConfigEnd:
			end, endCount = index, endCount+1
		}
	}
	if beginCount != endCount || beginCount > 1 || (beginCount == 1 && begin >= end) {
		return "", errors.New("local Codex config has an incomplete sync-codex sentinel block")
	}
	var raw string
	if beginCount == 1 {
		raw = strings.Join(lines[begin+1:end], "")
	} else {
		raw = string(current)
	}
	removeLLMux := active == "llmux" || (currentRoot != nil && currentRoot["model_provider"] == "llmux")
	removeLLMuxParent := active == "llmux"
	if !removeLLMuxParent && removeLLMux {
		providers := tomlTable(currentRoot["model_providers"])
		_, hasLLMux := providers["llmux"]
		removeLLMuxParent = len(providers) == 0 || (len(providers) == 1 && hasLLMux)
	}
	filtered := filterLocalConfigText(raw, sharedRoot, currentRoot, removeLLMux, removeLLMuxParent)
	return strings.TrimLeft(filtered, "\n"), nil
}

func filterLocalConfigText(raw string, sharedRoot, currentRoot map[string]any, removeLLMux, removeLLMuxParent bool) string {
	sharedRootKeys := map[string]bool{}
	sharedTables := map[string]bool{}
	for key, value := range sharedRoot {
		sharedRootKeys[key] = true
		if tomlTable(value) != nil {
			sharedTables[key] = true
		}
	}
	var output strings.Builder
	section := ""
	dropSection := false
	for _, line := range strings.SplitAfter(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			section = tomlSectionName(trimmed)
			dropSection = sharedTables[section] || (removeLLMux && section == "model_providers.llmux") || (removeLLMuxParent && section == "model_providers")
			if !dropSection {
				output.WriteString(line)
			}
			continue
		}
		if dropSection {
			continue
		}
		if section == "" {
			if key := tomlAssignmentKey(trimmed); key != "" {
				if sharedRootKeys[key] || (removeLLMux && key == "model_provider") {
					continue
				}
			}
		}
		output.WriteString(line)
	}
	return output.String()
}

func tomlSectionName(line string) string {
	if strings.HasPrefix(line, "[[") && strings.HasSuffix(line, "]]") {
		return strings.TrimSpace(line[2 : len(line)-2])
	}
	if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
		return strings.TrimSpace(line[1 : len(line)-1])
	}
	return ""
}

func tomlAssignmentKey(line string) string {
	if strings.HasPrefix(line, "#") || line == "" {
		return ""
	}
	index := strings.IndexByte(line, '=')
	if index <= 0 {
		return ""
	}
	key := strings.TrimSpace(line[:index])
	if strings.ContainsAny(key, " \t") {
		return ""
	}
	return strings.Trim(key, `"'`)
}

func ValidateRenderedConfig(payload []byte) error {
	root, err := decodeTOML(payload)
	if err != nil {
		return err
	}
	providers := tomlTable(root["model_providers"])
	if providers == nil {
		return nil
	}
	llmux := tomlTable(providers["llmux"])
	if llmux == nil {
		return nil
	}
	if _, exists := llmux["experimental_bearer_token"]; exists {
		return errors.New("rendered Codex base config still contains an inline llmux bearer token")
	}
	return nil
}
