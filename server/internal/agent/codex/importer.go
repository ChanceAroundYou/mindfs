package codex

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/apperr"

	agenttypes "mindfs/server/internal/agent/types"
)

type ImporterOptions struct {
	AgentName string
}

type Importer struct {
	agentName string
	baseDir   string
	titlePath string
	mu        sync.RWMutex
	index     map[string]codexSessionFile
}

type importedToolLocation struct {
	ExchangeIndex int
	AuxIndex      int
}

func NewImporter(opts ImporterOptions) *Importer {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		home, _ := os.UserHomeDir()
		codexHome = filepath.Join(strings.TrimSpace(home), ".codex")
	}
	return &Importer{
		agentName: strings.TrimSpace(opts.AgentName),
		baseDir:   filepath.Join(codexHome, "sessions"),
		titlePath: filepath.Join(codexHome, "session_index.jsonl"),
		index:     make(map[string]codexSessionFile),
	}
}

func (i *Importer) AgentName() string {
	return i.agentName
}

// HasTranscriptTimestamps 恒为 true：codex 转录行带 `timestamp`（parseTimeStringPtr 解析），所以时间地板对它成立。
func (i *Importer) HasTranscriptTimestamps() bool {
	return true
}

func (i *Importer) ListExternalSessions(ctx context.Context, in agenttypes.ListExternalSessionsInput) (agenttypes.ListExternalSessionsResult, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	items := make([]agenttypes.ExternalSessionSummary, 0, limit)
	err := i.ScanExternalSessions(ctx, in, func(item agenttypes.ExternalSessionSummary) (bool, error) {
		items = append(items, item)
		return len(items) < limit, nil
	})
	if err != nil {
		return agenttypes.ListExternalSessionsResult{}, err
	}
	return agenttypes.ListExternalSessionsResult{Items: items}, nil
}

func (i *Importer) ScanExternalSessions(ctx context.Context, in agenttypes.ListExternalSessionsInput, visit agenttypes.ExternalSessionVisitFunc) error {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	files, err := i.scanSessionFiles(ctx, in.BeforeTime, in.AfterTime, limit, visit)
	if err != nil {
		return err
	}
	i.storeSessionFiles(files)
	return nil
}

func (i *Importer) ImportExternalSession(_ context.Context, in agenttypes.ImportExternalSessionInput) (agenttypes.ImportedExternalSession, error) {
	rootPath := normalizeComparablePath(in.RootPath)
	if rootPath == "" {
		return agenttypes.ImportedExternalSession{}, errors.New("root path required")
	}
	targetID := strings.TrimSpace(in.AgentSessionID)
	if targetID == "" {
		return agenttypes.ImportedExternalSession{}, errors.New("agent session id required")
	}
	if file, ok := i.lookupSessionFile(targetID, rootPath); ok {
		return i.importSessionFile(file, in.AfterTimestamp, in.Cursor, in.ForceRead)
	}
	files, err := i.scanSessionFiles(context.Background(), time.Time{}, time.Time{}, int(^uint(0)>>1), nil)
	if err != nil {
		return agenttypes.ImportedExternalSession{}, err
	}
	for _, file := range files {
		if file.AgentSessionID != targetID {
			continue
		}
		return i.importSessionFile(file, in.AfterTimestamp, in.Cursor, in.ForceRead)
	}
	return agenttypes.ImportedExternalSession{}, errors.New("external session not found")
}

func (i *Importer) importSessionFile(file codexSessionFile, after time.Time, previous agenttypes.ExternalSessionCursor, forceRead bool) (agenttypes.ImportedExternalSession, error) {
	cursor, unchanged, err := externalSessionFileCursor(file.Path, previous)
	if err != nil {
		return agenttypes.ImportedExternalSession{}, err
	}
	if unchanged && !forceRead {
		return agenttypes.ImportedExternalSession{Agent: i.agentName, AgentSessionID: file.AgentSessionID, Cwd: file.Cwd, Title: file.Title, Cursor: cursor}, nil
	}
	exchanges, err := readCodexImportedExchanges(file.Path, after)
	if err != nil {
		log.Printf("[agent/codex/importer] import session read failed session_id=%s path=%s err=%v", file.AgentSessionID, file.Path, err)
		return agenttypes.ImportedExternalSession{}, err
	}
	subagents, err := i.readImportedSubagents(file)
	if err != nil {
		return agenttypes.ImportedExternalSession{}, err
	}
	return agenttypes.ImportedExternalSession{
		Agent:          i.agentName,
		AgentSessionID: file.AgentSessionID,
		Cwd:            file.Cwd,
		Title:          file.Title,
		Exchanges:      exchanges,
		Subagents:      subagents,
		Cursor:         cursor,
	}, nil
}

func externalSessionFileCursor(path string, previous agenttypes.ExternalSessionCursor) (agenttypes.ExternalSessionCursor, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return agenttypes.ExternalSessionCursor{}, false, err
	}
	cursor := agenttypes.ExternalSessionCursor{SourcePath: filepath.Clean(path), Offset: info.Size(), ModTimeUnixNano: info.ModTime().UnixNano()}
	unchanged := previous.Offset > 0 && filepath.Clean(previous.SourcePath) == cursor.SourcePath && previous.Offset == cursor.Offset && previous.ModTimeUnixNano == cursor.ModTimeUnixNano
	return cursor, unchanged, nil
}

func (i *Importer) ResolveForkPointByAgentTurnIndex(ctx context.Context, in agenttypes.ResolveForkPointInput) (agenttypes.ResolveForkPointOutput, error) {
	rootPath := normalizeComparablePath(in.RootPath)
	if rootPath == "" {
		return agenttypes.ResolveForkPointOutput{}, errors.New("root path required")
	}
	targetID := strings.TrimSpace(in.AgentSessionID)
	if targetID == "" {
		return agenttypes.ResolveForkPointOutput{}, errors.New("agent session id required")
	}
	if in.AgentTurnIndex <= 0 {
		return agenttypes.ResolveForkPointOutput{}, errors.New("agent turn index required")
	}
	file, ok := i.lookupSessionFile(targetID, rootPath)
	if !ok {
		files, err := i.scanSessionFiles(ctx, time.Time{}, time.Time{}, int(^uint(0)>>1), nil)
		if err != nil {
			return agenttypes.ResolveForkPointOutput{}, err
		}
		for _, candidate := range files {
			if candidate.AgentSessionID == targetID && cwdMatchesRoot(candidate.Cwd, rootPath) {
				file = candidate
				ok = true
				break
			}
		}
	}
	if !ok {
		return agenttypes.ResolveForkPointOutput{}, errors.New("external session not found")
	}
	items, err := readCodexImportedExchangeLocators(file.Path, time.Time{})
	if err != nil {
		return agenttypes.ResolveForkPointOutput{}, err
	}
	turns := buildImportedTurns(items)
	if in.AgentTurnIndex > len(turns) {
		return agenttypes.ResolveForkPointOutput{}, errors.New("agent turn index out of range")
	}
	agent := turns[in.AgentTurnIndex-1].Agent
	return agenttypes.ResolveForkPointOutput{
		Kind:             agenttypes.ForkPointCodexUserOrdinal,
		AgentSessionID:   targetID,
		CodexUserOrdinal: agent.CodexUserCountAfter,
	}, nil
}

func readCodexSessionTitles(path string) map[string]string {
	titles := make(map[string]string)
	file, err := os.Open(path)
	if err != nil {
		if apperr.IsPermission(err) {
			log.Printf("[agent/codex/importer] read title index failed path=%s err=%v", path, err)
		}
		return titles
	}
	defer file.Close()

	_ = forEachJSONLLine(file, func(line string) error {
		line = strings.TrimSpace(line)
		if line == "" {
			return nil
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil
		}
		id := strings.TrimSpace(asString(raw["id"]))
		title := strings.TrimSpace(asString(raw["thread_name"]))
		if id != "" && title != "" {
			titles[id] = title
		}
		return nil
	})
	return titles
}

func readCodexImportedExchanges(path string, after time.Time) ([]agenttypes.ImportedExchange, error) {
	locators, err := readCodexImportedExchangeLocators(path, after)
	if err != nil {
		return nil, err
	}
	items := make([]agenttypes.ImportedExchange, 0, len(locators))
	for _, item := range locators {
		items = append(items, item.ImportedExchange)
	}
	return items, nil
}
