package claude

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"mindfs/server/internal/apperr"
)


func sortedSessionJSONLFiles(baseDir string) ([]sessionFileCandidate, error) {
	items := make([]sessionFileCandidate, 0)
	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if apperr.IsPermission(walkErr) {
				return apperr.Wrap("walk", path, walkErr)
			}
			return nil
		}
		if d == nil || d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if isClaudeSubagentSessionFile(baseDir, path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if apperr.IsPermission(err) {
				return apperr.Wrap("stat", path, err)
			}
			return nil
		}
		items = append(items, sessionFileCandidate{
			Path:      path,
			UpdatedAt: info.ModTime().UTC(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].Path > items[j].Path
	})
	return items, nil
}

func isClaudeSubagentSessionFile(baseDir, path string) bool {
	rel, err := filepath.Rel(baseDir, path)
	if err != nil {
		return false
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "subagents" {
			return true
		}
	}
	return false
}

func (i *Importer) projectDir(rootPath string) string {
	dirName := claudeProjectDirName(rootPath)
	if dirName == "" {
		return ""
	}
	return filepath.Join(i.baseDir, dirName)
}

func claudeProjectDirName(rootPath string) string {
	rootPath = normalizeComparablePath(rootPath)
	if rootPath == "" {
		return ""
	}
	return sanitizeClaudeProjectPath(rootPath)
}

// sanitizeClaudeProjectPath mirrors Claude Code's JavaScript project-directory
// encoding. Replacement and truncation operate on UTF-16 code units, not
// Unicode code points; long names include a base-36 hash of the original path.
func sanitizeClaudeProjectPath(path string) string {
	units := utf16.Encode([]rune(path))
	encodedLength := min(len(units), claudeProjectDirMaxLength)

	var b strings.Builder
	b.Grow(encodedLength)
	for _, unit := range units[:encodedLength] {
		if (unit >= 'a' && unit <= 'z') || (unit >= 'A' && unit <= 'Z') || (unit >= '0' && unit <= '9') {
			b.WriteByte(byte(unit))
		} else {
			b.WriteByte('-')
		}
	}
	if len(units) > claudeProjectDirMaxLength {
		b.WriteByte('-')
		b.WriteString(claudeProjectPathHash(units))
	}
	return b.String()
}

func claudeProjectPathHash(units []uint16) string {
	var hash uint32
	for _, unit := range units {
		// JavaScript: hash = ((hash << 5) - hash + charCodeAt(i)) | 0
		hash = hash*31 + uint32(unit)
	}
	signed := int64(int32(hash))
	if signed < 0 {
		signed = -signed
	}
	return strconv.FormatInt(signed, 36)
}

func (i *Importer) storeSessionFiles(items []claudeSessionFile) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, item := range items {
		if strings.TrimSpace(item.AgentSessionID) == "" {
			continue
		}
		i.index[item.AgentSessionID] = item
	}
}

func (i *Importer) lookupSessionFile(sessionID, rootPath string) (claudeSessionFile, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	item, ok := i.index[strings.TrimSpace(sessionID)]
	if !ok {
		return claudeSessionFile{}, false
	}
	if !cwdMatchesRoot(item.Cwd, rootPath) {
		return claudeSessionFile{}, false
	}
	return item, true
}

// worktreeCandidateRoots 列出 rootPath 本身及其下 .worktree/* 托管工作树目录。
// mindfs 工作树固定创建在 <root>/.worktree/<name>（appcontext.CreateTaskWorktree）。
func worktreeCandidateRoots(rootPath string) []string {
	roots := []string{rootPath}
	parent := filepath.Join(rootPath, ".worktree")
	entries, err := os.ReadDir(parent)
	if err != nil {
		return roots
	}
	for _, entry := range entries {
		if entry.IsDir() {
			roots = append(roots, filepath.Join(parent, entry.Name()))
		}
	}
	return roots
}

// cwdMatchesRoot 报告转录文件记录的 cwd 是否归属该托管目录：根目录本身，
// 或其 .worktree/* 下的工作树（转录按 spawn cwd 归档，worktree 会话的 cwd 是工作树路径）。
func cwdMatchesRoot(cwd, rootPath string) bool {
	cwd = normalizeComparablePath(cwd)
	rootPath = normalizeComparablePath(rootPath)
	if cwd == rootPath {
		return true
	}
	return strings.HasPrefix(cwd, rootPath+"/.worktree/")
}

func inspectClaudeSessionFile(path string) (claudeSessionFile, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return claudeSessionFile{}, false, apperr.Wrap("open", path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return claudeSessionFile{}, false, err
	}
	var sessionID, cwd, firstUserText string
	err = forEachJSONLLine(file, func(_ int64, line string) error {
		line = strings.TrimSpace(line)
		if line == "" {
			return nil
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil
		}
		if sessionID == "" {
			sessionID = strings.TrimSpace(asString(raw["sessionId"]))
		}
		if cwd == "" {
			candidate := normalizeComparablePath(asString(raw["cwd"]))
			if candidate != "" {
				cwd = candidate
			}
		}
		if firstUserText == "" && strings.EqualFold(asString(raw["type"]), "user") {
			if message, _ := raw["message"].(map[string]any); message != nil {
				if text := extractClaudeUserPreview(message["content"]); text != "" {
					firstUserText = text
				}
			}
		}
		if sessionID != "" && cwd != "" && firstUserText != "" {
			return errStopJSONL
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopJSONL) {
		return claudeSessionFile{}, false, err
	}
	if sessionID == "" || cwd == "" {
		return claudeSessionFile{}, false, nil
	}
	return claudeSessionFile{
		Path:           path,
		AgentSessionID: sessionID,
		Cwd:            cwd,
		FirstUserText:  firstUserText,
		UpdatedAt:      info.ModTime().UTC(),
	}, true, nil
}

func appendSortedClaudeSession(items []claudeSessionFile, item claudeSessionFile) []claudeSessionFile {
	idx := sort.Search(len(items), func(i int) bool {
		return compareClaudeSessionFile(item, items[i]) < 0
	})
	items = append(items, claudeSessionFile{})
	copy(items[idx+1:], items[idx:])
	items[idx] = item
	return items
}

func compareClaudeSessionFile(left, right claudeSessionFile) int {
	if left.UpdatedAt.After(right.UpdatedAt) {
		return -1
	}
	if left.UpdatedAt.Before(right.UpdatedAt) {
		return 1
	}
	switch {
	case left.AgentSessionID > right.AgentSessionID:
		return -1
	case left.AgentSessionID < right.AgentSessionID:
		return 1
	default:
		return 0
	}
}

func normalizeComparablePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil && strings.TrimSpace(resolved) != "" {
		clean = resolved
	}
	if abs, err := filepath.Abs(clean); err == nil {
		clean = abs
	}
	return filepath.Clean(clean)
}
