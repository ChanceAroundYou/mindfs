package codexsync

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
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
	var mergedOverlay []byte
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
		mergedOverlay = profile
		prefix = mergeSharedProfiles(shared, profile)
	}
	overlay, err := extractLocalConfigOverlay(shared, local, active)
	if err != nil {
		return nil, 0, err
	}
	// 共享配置的根级赋值必须在任何表头之前，本地 overlay 同理。直接拼在
	// shared 末尾时，若 shared 以表结尾（例如 [features]），overlay 的根级键
	// （Windows Codex 会写 service_tier / notify）会被吞进那张表，Codex 报
	// `invalid type: string "default", expected a boolean`。因此把 shared 拆成
	// 根级/表两段：overlay（含根级键与表）整段留在 sentinel 内并置于 shared
	// 的表之前，shared 的表移到 sentinel 之后。布局只取决于两段内容，不随
	// overlay 是否含根级键切换，避免同一个文件在两种形态间来回改写。
	sharedRoot, sharedTables := splitTOMLRoot(strings.TrimRight(prefix, "\n"))
	overlayRoot, overlayTables := splitTOMLRoot(strings.TrimRight(overlay, "\n"))
	payload := []byte(composeRuntimeConfig(sharedRoot, overlayRoot, overlayTables, sharedTables))
	if err := ValidateRenderedConfig(payload); err != nil {
		return nil, 0, err
	}
	if err := validateLocalOverlayRootKeys(payload, []byte(overlay)); err != nil {
		return nil, 0, err
	}
	if mergedOverlay != nil {
		if err := validateMergedRootKeys(payload, mergedOverlay); err != nil {
			return nil, 0, err
		}
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(paths.RuntimeConfig); err == nil {
		mode = info.Mode().Perm()
	}
	return payload, mode, nil
}

// mergeSharedProfiles 把 llmux overlay 合并进 shared default。
//
// 关键约束：TOML 里表头之后的所有赋值都属于该表，所以 overlay 的根级键
// （model / model_provider / model_reasoning_effort）必须放在第一个表头之前，
// 不能直接拼在 default 末尾 —— 否则会被吞进最后一个表（[features]），
// Codex 解析时报 `invalid type: string "gpt-6-luna", expected a boolean`。
func mergeSharedProfiles(base, overlay []byte) string {
	root, _ := decodeTOML(overlay)
	dropKeys := make(map[string]bool, len(root))
	for key := range root {
		dropKeys[key] = true
	}
	baseRoot, baseTables := splitTOMLRoot(stripTOMLKeys(string(base), dropKeys))
	overlayRoot, overlayTables := splitTOMLRoot(string(overlay))
	var output strings.Builder
	appendPart := func(part string) {
		part = strings.TrimRight(part, "\n")
		if strings.TrimSpace(part) == "" {
			return
		}
		if output.Len() > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(part)
	}
	appendPart(baseRoot)
	appendPart(overlayRoot)
	appendPart(baseTables)
	appendPart(overlayTables)
	output.WriteString("\n")
	return output.String()
}

// splitTOMLRoot 按第一个表头切分 TOML 文本：前半是根级赋值，后半是表内容。
func splitTOMLRoot(raw string) (string, string) {
	lines := strings.SplitAfter(raw, "\n")
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			return strings.Join(lines[:index], ""), strings.Join(lines[index:], "")
		}
	}
	return raw, ""
}

// composeRuntimeConfig 按 TOML 作用域拼接运行时配置：
//
//	<shared 根级>
//	# >>> sync-codex local (not shared) >>>
//	<overlay 根级>
//	<overlay 表>
//	# <<< sync-codex local (not shared) <<<
//	<shared 表>
//
// overlay 整段必须在 sentinel 内，否则下次渲染会被当成共享内容丢弃；overlay
// 的根级键必须出现在任何表头之前，否则会被共享表（[features]）吸收。
func composeRuntimeConfig(sharedRoot, overlayRoot, overlayTables, sharedTables string) string {
	var out strings.Builder
	writePart := func(part string) {
		part = strings.TrimRight(part, "\n")
		if part == "" {
			return
		}
		out.WriteString(part)
		out.WriteString("\n")
	}
	writePart(sharedRoot)
	out.WriteString(localConfigBegin + "\n")
	writePart(overlayRoot)
	writePart(overlayTables)
	out.WriteString(localConfigEnd + "\n")
	writePart(sharedTables)
	return out.String()
}

// validateLocalOverlayRootKeys 确认本地 overlay 的每个根级键在渲染结果里仍位于
// 根级。共享配置以表结尾时，直接拼接会把 overlay 根级键塞进最后一张表；这里
// 让渲染直接失败，而不是写出 Codex 拒绝加载的配置。
func validateLocalOverlayRootKeys(payload, overlay []byte) error {
	renderedRoot, err := decodeTOML(payload)
	if err != nil {
		return err
	}
	overlayRoot, err := decodeTOML(overlay)
	if err != nil {
		return err
	}
	for key, want := range overlayRoot {
		if !mergedRootValuePresent(renderedRoot[key], want) {
			return fmt.Errorf("rendered Codex config lost local root key %q", key)
		}
	}
	return nil
}

// validateMergedRootKeys 确认 overlay 的每个根级键在最终配置里仍位于根级且值一致。
// 这是 mergeSharedProfiles 的回归护栏：键被表吞掉时渲染直接失败，而不是写出坏配置。
func validateMergedRootKeys(payload, overlay []byte) error {
	renderedRoot, err := decodeTOML(payload)
	if err != nil {
		return err
	}
	overlayRoot, err := decodeTOML(overlay)
	if err != nil {
		return err
	}
	for key, want := range overlayRoot {
		if !mergedRootValuePresent(renderedRoot[key], want) {
			return fmt.Errorf("rendered Codex config lost shared root key %q", key)
		}
	}
	return nil
}

// mergedRootValuePresent 比较 shared profile 的根级值是否仍落在根级。
// 表值只要求 shared 定义的子键存在（设备本地 provider 等兄弟键允许并存）。
func mergedRootValuePresent(got, want any) bool {
	wantTable := tomlTable(want)
	if wantTable == nil {
		return reflect.DeepEqual(got, want)
	}
	gotTable := tomlTable(got)
	if gotTable == nil {
		return false
	}
	for key, value := range wantTable {
		if !mergedRootValuePresent(gotTable[key], value) {
			return false
		}
	}
	return true
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
