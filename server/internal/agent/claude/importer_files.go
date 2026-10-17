package claude

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"mindfs/server/internal/apperr"

	agenttypes "mindfs/server/internal/agent/types"
)

type claudeSessionFile struct {
	Path           string
	AgentSessionID string
	Cwd            string
	FirstUserText  string
	UpdatedAt      time.Time
}

type sessionFileCandidate struct {
	Path      string
	UpdatedAt time.Time
}

func (i *Importer) scanSessionFiles(ctx context.Context, rootPath string, before, after time.Time, limit int, visit agenttypes.ExternalSessionVisitFunc) ([]claudeSessionFile, error) {
	if strings.TrimSpace(i.baseDir) == "" {
		return nil, nil
	}
	dir := i.projectDir(rootPath)
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, apperr.Wrap("stat", dir, err)
	}
	if !info.IsDir() {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	items := make([]claudeSessionFile, 0)
	paths, err := sortedSessionJSONLFiles(dir)
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
		item, ok, err := inspectClaudeSessionFile(candidate.Path)
		if err != nil {
			if apperr.IsPermission(err) {
				return nil, err
			}
			log.Printf("[agent/claude/importer] inspect session file failed path=%s err=%v", candidate.Path, err)
			continue
		}
		if !ok {
			continue
		}
		if visit != nil {
			shouldContinue, err := visit(agenttypes.ExternalSessionSummary{
				Agent:          i.agentName,
				AgentSessionID: item.AgentSessionID,
				Cwd:            item.Cwd,
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
		items = appendSortedClaudeSession(items, item)
		if len(items) > limit {
			items = items[:limit]
		}
	}
	i.storeSessionFiles(items)
	return items, nil
}
