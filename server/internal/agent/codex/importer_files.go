package codex

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mindfs/server/internal/apperr"

	agenttypes "mindfs/server/internal/agent/types"
)

type codexSessionFile struct {
	Path                 string
	AgentSessionID       string
	ParentAgentSessionID string
	Cwd                  string
	Title                string
	AgentNickname        string
	AgentRole            string
	FirstUserText        string
	UpdatedAt            time.Time
}

type sessionFileCandidate struct {
	Path      string
	UpdatedAt time.Time
}

func (i *Importer) scanSessionFiles(ctx context.Context, before, after time.Time, limit int, visit agenttypes.ExternalSessionVisitFunc) ([]codexSessionFile, error) {
	if strings.TrimSpace(i.baseDir) == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	titles := readCodexSessionTitles(i.titlePath)
	items := make([]codexSessionFile, 0)
	paths, err := sortedSessionJSONLFiles(i.baseDir)
	if err != nil {
		return nil, err
	}
	for _, candidate := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !before.IsZero() && !candidate.UpdatedAt.Before(before) {
			continue
		}
		if !after.IsZero() && !candidate.UpdatedAt.After(after) {
			break
		}
		item, ok, err := inspectCodexSessionFile(candidate.Path)
		if err != nil {
			if apperr.IsPermission(err) {
				return nil, err
			}
			log.Printf("[agent/codex/importer] inspect session file failed path=%s err=%v", candidate.Path, err)
			continue
		}
		if !ok {
			continue
		}
		if item.ParentAgentSessionID != "" {
			continue
		}
		item.Title = titles[item.AgentSessionID]
		if visit != nil {
			shouldContinue, err := visit(agenttypes.ExternalSessionSummary{
				Agent:          i.agentName,
				AgentSessionID: item.AgentSessionID,
				Cwd:            item.Cwd,
				Title:          item.Title,
				FirstUserText:  item.FirstUserText,
				UpdatedAt:      item.UpdatedAt,
			})
			if err != nil {
				return nil, err
			}
			items = append(items, item)
			if !shouldContinue {
				return items, nil
			}
			continue
		}
		items = appendSortedCodexSession(items, item)
		if len(items) > limit {
			items = items[:limit]
		}
	}
	i.storeSessionFiles(items)
	return items, nil
}

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

func (i *Importer) storeSessionFiles(items []codexSessionFile) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for _, item := range items {
		if strings.TrimSpace(item.AgentSessionID) == "" {
			continue
		}
		i.index[item.AgentSessionID] = item
	}
}

func (i *Importer) lookupSessionFile(sessionID, rootPath string) (codexSessionFile, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	item, ok := i.index[strings.TrimSpace(sessionID)]
	if !ok {
		return codexSessionFile{}, false
	}
	if !cwdMatchesRoot(item.Cwd, rootPath) {
		return codexSessionFile{}, false
	}
	return item, true
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

func inspectCodexSessionFile(path string) (codexSessionFile, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return codexSessionFile{}, false, apperr.Wrap("open", path, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return codexSessionFile{}, false, err
	}
	var sessionID, parentSessionID, cwd, firstUserText, agentNickname, agentRole string
	err = forEachJSONLLine(file, func(line string) error {
		line = strings.TrimSpace(line)
		if line == "" {
			return nil
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil
		}
		if sessionID == "" && raw["type"] == "session_meta" {
			if payload, _ := raw["payload"].(map[string]any); payload != nil {
				sessionID = strings.TrimSpace(asString(payload["id"]))
				cwd = normalizeComparablePath(asString(payload["cwd"]))
				agentNickname = strings.TrimSpace(asString(payload["agent_nickname"]))
				agentRole = strings.TrimSpace(asString(payload["agent_role"]))
				if source, _ := payload["source"].(map[string]any); source != nil {
					if subagent, _ := source["subagent"].(map[string]any); subagent != nil {
						if spawn, _ := subagent["thread_spawn"].(map[string]any); spawn != nil {
							parentSessionID = strings.TrimSpace(asString(spawn["parent_thread_id"]))
							if agentNickname == "" {
								agentNickname = strings.TrimSpace(asString(spawn["agent_nickname"]))
							}
							if agentRole == "" {
								agentRole = strings.TrimSpace(asString(spawn["agent_role"]))
							}
						}
					}
				}
			}
			return nil
		}
		if cwd == "" && raw["type"] == "turn_context" {
			if payload, _ := raw["payload"].(map[string]any); payload != nil {
				cwd = normalizeComparablePath(asString(payload["cwd"]))
			}
			return nil
		}
		if firstUserText == "" && raw["type"] == "response_item" {
			if payload, _ := raw["payload"].(map[string]any); payload != nil {
				if payload["type"] == "message" && strings.EqualFold(asString(payload["role"]), "user") {
					if text := extractCodexUserPreview(payload["content"]); text != "" {
						firstUserText = text
					}
				}
			}
		}
		if sessionID != "" && cwd != "" && firstUserText != "" {
			return errStopJSONL
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopJSONL) {
		return codexSessionFile{}, false, err
	}
	if sessionID == "" || cwd == "" {
		return codexSessionFile{}, false, nil
	}
	return codexSessionFile{
		Path:                 path,
		AgentSessionID:       sessionID,
		ParentAgentSessionID: parentSessionID,
		Cwd:                  cwd,
		AgentNickname:        agentNickname,
		AgentRole:            agentRole,
		FirstUserText:        firstUserText,
		UpdatedAt:            info.ModTime().UTC(),
	}, true, nil
}

func appendSortedCodexSession(items []codexSessionFile, item codexSessionFile) []codexSessionFile {
	idx := sort.Search(len(items), func(i int) bool {
		return compareCodexSessionFile(item, items[i]) < 0
	})
	items = append(items, codexSessionFile{})
	copy(items[idx+1:], items[idx:])
	items[idx] = item
	return items
}

func compareCodexSessionFile(left, right codexSessionFile) int {
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
