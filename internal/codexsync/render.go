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
	shared, err := readOptional(paths.SharedDefault)
	if err != nil {
		return err
	}
	if shared == nil {
		return nil
	}
	if err := ValidateSharedProfile("default", shared); err != nil {
		return err
	}
	if info, err := os.Lstat(paths.RuntimeConfig); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("runtime Codex config is a symlink; refusing to replace an unowned target")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	local, err := readOptional(paths.RuntimeConfig)
	if err != nil {
		return fmt.Errorf("read local Codex config: %w", err)
	}
	overlay, err := extractLocalConfigOverlay(shared, local)
	if err != nil {
		return err
	}
	// Match claude/scripts/sync-codex.sh byte shape so its read-only drift check
	// continues to recognize the generated config on every device.
	prefix := strings.TrimRight(string(shared), "\n") + "\n"
	payload := []byte(prefix + localConfigBegin + "\n" + overlay + localConfigEnd + "\n")
	if err := ValidateRenderedConfig(payload); err != nil {
		return err
	}
	if filesEqual(paths.RuntimeConfig, payload) {
		return nil
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(paths.RuntimeConfig); err == nil {
		mode = info.Mode().Perm()
	}
	if err := saveRollbackCopy(paths.StateDir, paths.RuntimeConfig); err != nil {
		return err
	}
	if err := atomicWriteFile(paths.RuntimeConfig, payload, mode); err != nil {
		return fmt.Errorf("atomically replace local Codex config: %w", err)
	}
	return nil
}

func extractLocalConfigOverlay(shared, current []byte) (string, error) {
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
	filtered := filterLocalConfigText(raw, sharedRoot, currentRoot)
	return strings.TrimLeft(filtered, "\n"), nil
}

func filterLocalConfigText(raw string, sharedRoot, currentRoot map[string]any) string {
	sharedRootKeys := map[string]bool{}
	sharedTables := map[string]bool{}
	for key, value := range sharedRoot {
		sharedRootKeys[key] = true
		if tomlTable(value) != nil {
			sharedTables[key] = true
		}
	}
	removeLLMux := currentRoot != nil && currentRoot["model_provider"] == "llmux"
	var output strings.Builder
	section := ""
	dropSection := false
	for _, line := range strings.SplitAfter(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			section = tomlSectionName(trimmed)
			dropSection = sharedTables[section] || (removeLLMux && section == "model_providers.llmux")
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
