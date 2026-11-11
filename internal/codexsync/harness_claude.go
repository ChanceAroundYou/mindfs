package codexsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// Claude 的共享配置由 .agents/claude 管理，运行时文件是
// ${CLAUDE_CONFIG_DIR:-$HOME/.claude}/settings.json、.mcp.json，以及设备本地的
// ~/.claude.json。渲染语义必须与 .agents/claude/scripts/render.sh 保持一致：
//   - settings.json = settings.shared.json * settings.local.json（递归合并，数组替换）
//   - permissions.deny 取各层并集（去重排序，单调收紧）
//   - .mcp.json = mcp.shared.json * {mcpServers: settings.local.json.mcpServers}
//     值为 null 的服务器表示「本机停用」，展开 ${HOME}
//   - ~/.claude.json 只合并 mcpServers，其余设备本地键原样保留
//
// 之所以在 Go 里重写而不是调用 render.sh：pc-win 没有 jq，bash 还是 WSL 的
// bash；同步必须能在四个节点上跑同一套逻辑。语义等价性由测试钉住。
const maxClaudeRuntimeJSONBytes = 8 << 20

var claudeSecretKeyPattern = regexp.MustCompile(`(?i)(^|_)(api_?key|access_?token|auth_?token|refresh_?token|client_?secret|password|secret|authorization|credentials?)$`)

var claudeLiteralCredentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`),
	regexp.MustCompile(`\bghp_[A-Za-z0-9]{20,}\b`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`),
}

// validateClaudeShared 阻止把凭据推进共享仓库。共享文件里允许出现环境变量引用
// （${VAR}、$VAR）、空值与 <placeholder>；一旦是字面量密钥就拒绝。
func validateClaudeShared(paths Paths) error {
	for _, path := range []string{paths.SharedClaudeSettings, paths.SharedClaudeMCP} {
		payload, err := readOptional(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(path), err)
		}
		if payload == nil {
			continue
		}
		var value any
		if err := json.Unmarshal(payload, &value); err != nil {
			return fmt.Errorf("%s must be one JSON document: %w", filepath.Base(path), err)
		}
		if err := rejectClaudeSecrets(filepath.Base(path), value, ""); err != nil {
			return err
		}
		if err := rejectClaudeLiteralTokens(filepath.Base(path), value); err != nil {
			return err
		}
	}
	return nil
}

func rejectClaudeSecrets(file string, value any, path string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			childPath := path + "/" + key
			if claudeSecretKeyPattern.MatchString(key) {
				if text, ok := child.(string); ok && !isClaudePlaceholder(text) {
					return fmt.Errorf("%s%s looks like a credential; keep secrets in settings.local.json", file, childPath)
				}
			}
			if err := rejectClaudeSecrets(file, child, childPath); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range typed {
			if err := rejectClaudeSecrets(file, child, fmt.Sprintf("%s/%d", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func rejectClaudeLiteralTokens(file string, value any) error {
	switch typed := value.(type) {
	case string:
		for _, pattern := range claudeLiteralCredentialPatterns {
			if pattern.MatchString(typed) {
				return fmt.Errorf("%s contains a literal credential token; keep secrets in settings.local.json", file)
			}
		}
	case map[string]any:
		for _, child := range typed {
			if err := rejectClaudeLiteralTokens(file, child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := rejectClaudeLiteralTokens(file, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func isClaudePlaceholder(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return true
	}
	if strings.HasPrefix(trimmed, "Bearer ") || strings.HasPrefix(trimmed, "bearer ") {
		return isClaudePlaceholder(strings.TrimSpace(trimmed[len("Bearer "):]))
	}
	if strings.HasPrefix(trimmed, "${") && strings.HasSuffix(trimmed, "}") && len(trimmed) > 3 {
		return true
	}
	if strings.HasPrefix(trimmed, "$") && len(trimmed) > 1 && isClaudeEnvName(trimmed[1:]) {
		return true
	}
	return strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">") && len(trimmed) > 2
}

func isClaudeEnvName(value string) bool {
	for _, char := range value {
		if (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return value != ""
}

// renderClaudeRuntime 把共享配置渲染到本机运行时文件；内容语义等价时不落盘，
// 现有文件不是合法 JSON 时拒绝覆盖（宁可不改，也不吞掉用户配置）。
func renderClaudeRuntime(paths Paths) error {
	shared, err := readJSONObject(paths.SharedClaudeSettings)
	if err != nil {
		return fmt.Errorf("read shared settings: %w", err)
	}
	if shared == nil {
		return nil
	}
	local, err := readJSONObjectFollow(paths.ClaudeLocalSettings)
	if err != nil {
		return fmt.Errorf("read settings.local.json: %w", err)
	}

	layers := []map[string]any{shared}
	if local != nil {
		layers = append(layers, local)
	}
	settings := map[string]any{}
	for _, layer := range layers {
		settings = deepMergeJSON(settings, layer)
	}
	if err := applyClaudeDenyUnion(settings, layers); err != nil {
		return err
	}
	if err := writeClaudeJSONIfChanged(paths.ClaudeRuntimeSettings, settings); err != nil {
		return fmt.Errorf("render settings.json: %w", err)
	}

	renderedMCP, disabled, err := renderClaudeMCP(local, paths)
	if err != nil {
		return err
	}
	if renderedMCP == nil {
		return nil
	}
	if err := writeClaudeJSONIfChanged(paths.ClaudeRuntimeMCP, renderedMCP); err != nil {
		return fmt.Errorf("render .mcp.json: %w", err)
	}
	if err := mergeClaudeJSONMCPServers(paths.ClaudeJSON, renderedMCP, disabled); err != nil {
		return fmt.Errorf("merge ~/.claude.json mcpServers: %w", err)
	}
	return nil
}

func renderClaudeMCP(local map[string]any, paths Paths) (map[string]any, []string, error) {
	sharedMCP, err := readJSONObject(paths.SharedClaudeMCP)
	if err != nil {
		return nil, nil, fmt.Errorf("read shared mcp config: %w", err)
	}
	layers := make([]map[string]any, 0, 2)
	if sharedMCP != nil {
		layers = append(layers, sharedMCP)
	}
	if local != nil {
		if servers, ok := local["mcpServers"].(map[string]any); ok && len(servers) > 0 {
			layers = append(layers, map[string]any{"mcpServers": servers})
		}
	}
	if len(layers) == 0 {
		return nil, nil, nil
	}
	merged := map[string]any{}
	for _, layer := range layers {
		merged = deepMergeJSON(merged, layer)
	}
	disabled := []string{}
	if servers, ok := merged["mcpServers"].(map[string]any); ok {
		for name, value := range servers {
			if value == nil {
				delete(servers, name)
				disabled = append(disabled, name)
			}
		}
		if len(servers) > 0 {
			merged["mcpServers"] = servers
		} else {
			delete(merged, "mcpServers")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	expanded, _ := expandClaudeHome(merged, home).(map[string]any)
	sort.Strings(disabled)
	return expanded, disabled, nil
}

func mergeClaudeJSONMCPServers(path string, rendered map[string]any, disabled []string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	existing, err := readJSONObjectFollow(path)
	if err != nil {
		// ~/.claude.json 是设备本地文件；解析失败时保持原样，不阻断同步。
		return nil
	}
	if existing == nil {
		return nil
	}
	servers := map[string]any{}
	if current, ok := existing["mcpServers"].(map[string]any); ok {
		servers = deepMergeJSON(map[string]any{}, current)
	}
	for _, name := range disabled {
		delete(servers, name)
	}
	if renderedServers, ok := rendered["mcpServers"].(map[string]any); ok {
		servers = deepMergeJSON(servers, renderedServers)
	}
	if len(servers) == 0 {
		delete(existing, "mcpServers")
	} else {
		existing["mcpServers"] = servers
	}
	return writeClaudeJSONIfChanged(path, existing)
}

func applyClaudeDenyUnion(settings map[string]any, layers []map[string]any) error {
	seen := map[string]struct{}{}
	for _, layer := range layers {
		permissions, ok := layer["permissions"].(map[string]any)
		if !ok {
			continue
		}
		entries, ok := permissions["deny"].([]any)
		if !ok {
			continue
		}
		for _, entry := range entries {
			if text, ok := entry.(string); ok {
				seen[text] = struct{}{}
			}
		}
	}
	deny := make([]string, 0, len(seen))
	for entry := range seen {
		deny = append(deny, entry)
	}
	sort.Strings(deny)

	permissions, ok := settings["permissions"].(map[string]any)
	if !ok && settings["permissions"] != nil {
		return errors.New("permissions in shared Claude settings must be an object")
	}
	if permissions == nil {
		permissions = map[string]any{}
	}
	values := make([]any, len(deny))
	for index, entry := range deny {
		values[index] = entry
	}
	permissions["deny"] = values
	settings["permissions"] = permissions
	return nil
}

func readJSONObject(path string) (map[string]any, error) {
	payload, err := readOptional(path)
	if err != nil {
		return nil, err
	}
	return decodeJSONObject(path, payload)
}

func readJSONObjectFollow(path string) (map[string]any, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	if info.Size() > maxClaudeRuntimeJSONBytes {
		return nil, fmt.Errorf("%s exceeds 8 MiB", filepath.Base(path))
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decodeJSONObject(path, payload)
}

func decodeJSONObject(path string, payload []byte) (map[string]any, error) {
	if payload == nil {
		return nil, nil
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, fmt.Errorf("%s must be one JSON object: %w", filepath.Base(path), err)
	}
	if value == nil {
		value = map[string]any{}
	}
	return value, nil
}

func writeClaudeJSONIfChanged(path string, value map[string]any) error {
	target, err := claudeWriteTarget(path)
	if err != nil {
		return err
	}
	existing, readErr := os.ReadFile(target)
	if readErr == nil {
		var current any
		if err := json.Unmarshal(existing, &current); err != nil {
			return fmt.Errorf("%s is not valid JSON; refusing to overwrite it", filepath.Base(path))
		}
		if reflect.DeepEqual(current, value) {
			return nil
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return atomicWriteFile(target, payload, 0o600)
}

// claudeWriteTarget 保留 symlink 布局：运行时文件若是指向 dotfiles 的链接，
// 原子替换会把它变成普通文件，所以写到链接目标上。
func claudeWriteTarget(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		return "", err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("%s is a broken symlink: %w", filepath.Base(path), err)
	}
	return resolved, nil
}

func deepMergeJSON(base, overlay map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overlay))
	for key, value := range base {
		out[key] = cloneJSONValue(value)
	}
	for key, value := range overlay {
		if existing, ok := out[key]; ok {
			existingObject, existingOK := existing.(map[string]any)
			overlayObject, overlayOK := value.(map[string]any)
			if existingOK && overlayOK {
				out[key] = deepMergeJSON(existingObject, overlayObject)
				continue
			}
		}
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, child := range typed {
			cloned[key] = cloneJSONValue(child)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, child := range typed {
			cloned[index] = cloneJSONValue(child)
		}
		return cloned
	default:
		return value
	}
}

func expandClaudeHome(value any, home string) any {
	switch typed := value.(type) {
	case string:
		return strings.ReplaceAll(typed, "${HOME}", home)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			out[key] = expandClaudeHome(child, home)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = expandClaudeHome(child, home)
		}
		return out
	default:
		return value
	}
}
