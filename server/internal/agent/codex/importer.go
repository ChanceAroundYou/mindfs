package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
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

type importedExchangeLocator struct {
	agenttypes.ImportedExchange
	CodexUserCountAfter int
}

type importedTurn struct {
	Users []importedExchangeLocator
	Agent importedExchangeLocator
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

type codexSpawnRelation struct {
	ParentToolCallID string
	Title            string
}

func (i *Importer) readImportedSubagents(root codexSessionFile) ([]agenttypes.ImportedSubagentSession, error) {
	candidates, err := sortedSessionJSONLFiles(i.baseDir)
	if err != nil {
		return nil, err
	}
	files := make(map[string]codexSessionFile)
	for _, candidate := range candidates {
		item, ok, err := inspectCodexSessionFile(candidate.Path)
		if err != nil {
			return nil, err
		}
		if ok && normalizeComparablePath(item.Cwd) == normalizeComparablePath(root.Cwd) {
			files[item.AgentSessionID] = item
		}
	}
	relationsByParent := make(map[string]map[string]codexSpawnRelation)
	for id, file := range files {
		relations, err := readCodexSpawnRelations(file.Path)
		if err != nil {
			return nil, err
		}
		relationsByParent[id] = relations
	}
	items := make([]agenttypes.ImportedSubagentSession, 0)
	added := map[string]bool{root.AgentSessionID: true}
	for {
		progressed := false
		for id, file := range files {
			if id == root.AgentSessionID || added[id] || !added[file.ParentAgentSessionID] {
				continue
			}
			exchanges, err := readCodexImportedExchanges(file.Path, time.Time{})
			if err != nil {
				return nil, err
			}
			relation := relationsByParent[file.ParentAgentSessionID][id]
			parentID := file.ParentAgentSessionID
			if parentID == root.AgentSessionID {
				parentID = ""
			}
			title := firstNonEmpty(file.AgentNickname, relation.Title, file.AgentRole, file.FirstUserText, "Subagent")
			items = append(items, agenttypes.ImportedSubagentSession{
				AgentSessionID:       id,
				ParentAgentSessionID: parentID,
				ParentToolCallID:     relation.ParentToolCallID,
				Title:                title,
				Exchanges:            exchanges,
			})
			added[id] = true
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return items, nil
}

func readCodexSpawnRelations(path string) (map[string]codexSpawnRelation, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, apperr.Wrap("open", path, err)
	}
	defer file.Close()
	callDetails := make(map[string]codexSpawnRelation)
	relations := make(map[string]codexSpawnRelation)
	err = forEachJSONLLine(file, func(line string) error {
		var raw map[string]any
		if json.Unmarshal([]byte(line), &raw) != nil || raw["type"] != "response_item" {
			return nil
		}
		payload, _ := raw["payload"].(map[string]any)
		switch asString(payload["type"]) {
		case "function_call":
			if asString(payload["name"]) != "spawn_agent" {
				return nil
			}
			callID := strings.TrimSpace(asString(payload["call_id"]))
			var args map[string]any
			_ = json.Unmarshal([]byte(asString(payload["arguments"])), &args)
			callDetails[callID] = codexSpawnRelation{
				ParentToolCallID: callID,
				Title:            firstNonEmpty(asString(args["agent_type"]), asString(args["message"]), "Subagent"),
			}
		case "function_call_output":
			callID := strings.TrimSpace(asString(payload["call_id"]))
			relation, ok := callDetails[callID]
			if !ok {
				return nil
			}
			var result map[string]any
			if json.Unmarshal([]byte(asString(payload["output"])), &result) != nil {
				return nil
			}
			agentID := strings.TrimSpace(asString(result["agent_id"]))
			if agentID != "" {
				if nickname := strings.TrimSpace(asString(result["nickname"])); nickname != "" {
					relation.Title = nickname
				}
				relations[agentID] = relation
			}
		}
		return nil
	})
	return relations, err
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

func readCodexImportedExchangeLocators(path string, after time.Time) ([]importedExchangeLocator, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, apperr.Wrap("open", path, err)
	}
	defer file.Close()

	items := make([]agenttypes.ImportedExchange, 0)
	toolLocations := make(map[string]importedToolLocation)
	toolOrdinal := 0
	sessionShell := ""
	err = forEachJSONLLine(file, func(line string) error {
		line = strings.TrimSpace(line)
		if line == "" {
			return nil
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil
		}
		timestamp := parseTimeRFC3339(asString(raw["timestamp"]))
		switch raw["type"] {
		case "response_item":
			payload, _ := raw["payload"].(map[string]any)
			if payload == nil {
				return nil
			}
			switch strings.TrimSpace(asString(payload["type"])) {
			case "message":
				role := strings.ToLower(strings.TrimSpace(asString(payload["role"])))
				switch role {
				case "user":
					text := extractCodexImportedUserText(payload["content"])
					if text == "" {
						return nil
					}
					items, _, _ = appendMergedCodexExchange(items, "user", text, timestamp, nil)
				case "assistant":
					text := strings.TrimSpace(extractCodexMessageText(payload["content"]))
					text, planAux := extractImportedCodexProposedPlan(text)
					if text == "" && len(planAux) == 0 {
						return nil
					}
					items, _, _ = appendMergedCodexExchange(items, "agent", text, timestamp, planAux)
				}
			case "function_call", "custom_tool_call":
				toolOrdinal++
				toolCall, ok := parseImportedCodexToolCall(payload, toolOrdinal, sessionShell)
				if !ok {
					return nil
				}
				aux := []agenttypes.ImportedExchangeAux{{
					Line:     0,
					ToolCall: &toolCall,
				}}
				var exchangeIndex, auxStart int
				items, exchangeIndex, auxStart = appendMergedCodexExchange(
					items,
					"agent",
					"",
					timestamp,
					aux,
				)
				if exchangeIndex < 0 {
					return nil
				}
				toolLocations[toolCall.CallID] = importedToolLocation{
					ExchangeIndex: exchangeIndex,
					AuxIndex:      auxStart,
				}
			case "function_call_output", "custom_tool_call_output":
				applyImportedCodexToolOutput(items, toolLocations, payload, timestamp)
			}
		case "event_msg":
			if numTurns := codexRollbackTurns(raw); numTurns > 0 {
				items = dropLastCodexUserTurns(items, numTurns)
				toolLocations = rebuildImportedCodexToolLocations(items)
			}
		case "world_state":
			if shell := importedCodexWorldStateShell(raw); shell != "" {
				sessionShell = shell
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return codexExchangeLocatorsAfter(items, after), nil
}

func extractImportedCodexProposedPlan(text string) (string, []agenttypes.ImportedExchangeAux) {
	const openTag = "<proposed_plan>"
	const closeTag = "</proposed_plan>"
	start := strings.Index(text, openTag)
	if start < 0 {
		return text, nil
	}
	contentStart := start + len(openTag)
	closeOffset := strings.Index(text[contentStart:], closeTag)
	if closeOffset < 0 {
		return text, nil
	}
	end := contentStart + closeOffset
	planContent := strings.TrimSpace(text[contentStart:end])
	if planContent == "" {
		return text, nil
	}
	before := strings.TrimSpace(text[:start])
	after := strings.TrimSpace(text[end+len(closeTag):])
	remaining := strings.TrimSpace(strings.Join(nonEmptyStrings(before, after), "\n\n"))
	return remaining, []agenttypes.ImportedExchangeAux{{
		Line: importedCodexAssistantLine(before),
		Plan: &agenttypes.PlanUpdate{Content: planContent},
	}}
}

func nonEmptyStrings(values ...string) []string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			items = append(items, value)
		}
	}
	return items
}

var errStopJSONL = errors.New("stop jsonl")

func forEachJSONLLine(file *os.File, fn func(string) error) error {
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if callErr := fn(string(line)); callErr != nil {
				return callErr
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

func appendMergedCodexExchange(
	items []agenttypes.ImportedExchange,
	role, content string,
	ts time.Time,
	aux []agenttypes.ImportedExchangeAux,
) ([]agenttypes.ImportedExchange, int, int) {
	content = strings.TrimSpace(content)
	if content == "" && len(aux) == 0 {
		return items, -1, 0
	}
	if len(items) > 0 && items[len(items)-1].Role == role {
		last := &items[len(items)-1]
		lineOffset := importedCodexAssistantLine(last.Content)
		if content != "" {
			if last.Content == "" {
				last.Content = content
			} else {
				last.Content = strings.TrimSpace(last.Content + "\n\n" + content)
			}
		}
		if !ts.IsZero() {
			last.Timestamp = ts
		}
		auxStart := len(last.Aux)
		for _, item := range aux {
			item.Line += lineOffset
			last.Aux = append(last.Aux, item)
		}
		return items, len(items) - 1, auxStart
	}
	items = append(items, agenttypes.ImportedExchange{
		Role:      role,
		Content:   content,
		Timestamp: ts,
		Aux:       aux,
	})
	return items, len(items) - 1, 0
}

func appendMergedExchangeLocator(
	items []importedExchangeLocator,
	role, content string,
	ts time.Time,
	userCount int,
	aux []agenttypes.ImportedExchangeAux,
) []importedExchangeLocator {
	content = strings.TrimSpace(content)
	if content == "" && len(aux) == 0 {
		return items
	}
	if len(items) > 0 && items[len(items)-1].Role == role {
		last := &items[len(items)-1]
		lineOffset := importedCodexAssistantLine(last.Content)
		if content != "" {
			if last.Content == "" {
				last.Content = content
			} else {
				last.Content = strings.TrimSpace(last.Content + "\n\n" + content)
			}
		}
		if !ts.IsZero() {
			last.Timestamp = ts
		}
		for _, item := range aux {
			item.Line += lineOffset
			last.Aux = append(last.Aux, item)
		}
		last.CodexUserCountAfter = userCount
		return items
	}
	items = append(items, importedExchangeLocator{
		ImportedExchange: agenttypes.ImportedExchange{
			Role:      role,
			Content:   content,
			Timestamp: ts,
			Aux:       aux,
		},
		CodexUserCountAfter: userCount,
	})
	return items
}

func codexRollbackTurns(raw map[string]any) int {
	payload, _ := raw["payload"].(map[string]any)
	if payload == nil || strings.TrimSpace(asString(payload["type"])) != "thread_rolled_back" {
		return 0
	}
	switch value := payload["num_turns"].(type) {
	case float64:
		if value > 0 {
			return int(value)
		}
	case int:
		if value > 0 {
			return value
		}
	}
	return 0
}

func dropLastCodexUserTurns(items []agenttypes.ImportedExchange, numTurns int) []agenttypes.ImportedExchange {
	if numTurns <= 0 || len(items) == 0 {
		return items
	}
	out := items
	for ; numTurns > 0 && len(out) > 0; numTurns-- {
		userIndex := -1
		for i := len(out) - 1; i >= 0; i-- {
			if out[i].Role == "user" {
				userIndex = i
				break
			}
		}
		if userIndex < 0 {
			return nil
		}
		out = out[:userIndex]
	}
	return out
}

func codexExchangeLocatorsAfter(items []agenttypes.ImportedExchange, after time.Time) []importedExchangeLocator {
	out := make([]importedExchangeLocator, 0, len(items))
	userCount := 0
	for _, item := range items {
		if item.Role == "user" {
			userCount++
		}
		if !after.IsZero() && (item.Timestamp.IsZero() || !item.Timestamp.After(after)) {
			continue
		}
		out = appendMergedExchangeLocator(
			out,
			item.Role,
			item.Content,
			item.Timestamp,
			userCount,
			item.Aux,
		)
	}
	return out
}

func buildImportedTurns(items []importedExchangeLocator) []importedTurn {
	turns := make([]importedTurn, 0)
	users := make([]importedExchangeLocator, 0)
	for _, item := range items {
		switch item.Role {
		case "user":
			users = append(users, item)
		case "agent":
			turns = append(turns, importedTurn{
				Users: append([]importedExchangeLocator(nil), users...),
				Agent: item,
			})
			users = nil
		}
	}
	return turns
}

func extractCodexMessageText(raw any) string {
	parts, _ := raw.([]any)
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		item, _ := part.(map[string]any)
		if item == nil {
			continue
		}
		switch strings.TrimSpace(asString(item["type"])) {
		case "input_text", "output_text", "text":
			if text := strings.TrimSpace(asString(item["text"])); text != "" {
				lines = append(lines, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n\n"))
}

func extractCodexUserPreview(raw any) string {
	parts, _ := raw.([]any)
	for index := len(parts) - 1; index >= 0; index-- {
		item, _ := parts[index].(map[string]any)
		if item == nil {
			continue
		}
		switch strings.TrimSpace(asString(item["type"])) {
		case "input_text", "text":
			text := strings.TrimSpace(asString(item["text"]))
			if isMeaningfulCodexUserText(text) {
				return text
			}
		}
	}
	return ""
}

func extractCodexImportedUserText(raw any) string {
	if text := strings.TrimSpace(asString(raw)); text != "" {
		if isMeaningfulCodexUserText(text) {
			return text
		}
		return ""
	}
	parts, _ := raw.([]any)
	texts := make([]string, 0, len(parts))
	for _, value := range parts {
		item, _ := value.(map[string]any)
		if item == nil {
			continue
		}
		switch strings.TrimSpace(asString(item["type"])) {
		case "input_text", "text":
			text := strings.TrimSpace(asString(item["text"]))
			if isMeaningfulCodexUserText(text) {
				texts = append(texts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n\n"))
}

func isMeaningfulCodexUserText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, tags := range [][2]string{
		{"<recommended_plugins>", "</recommended_plugins>"},
		{"<environment_context>", "</environment_context>"},
		{"<permissions instructions>", "</permissions instructions>"},
		{"<skills_instructions>", "</skills_instructions>"},
		{"<apps_instructions>", "</apps_instructions>"},
		{"<plugins_instructions>", "</plugins_instructions>"},
	} {
		if strings.HasPrefix(text, tags[0]) && strings.HasSuffix(text, tags[1]) {
			return false
		}
	}
	if strings.HasPrefix(text, "# AGENTS.md instructions") && strings.HasSuffix(text, "</INSTRUCTIONS>") {
		return false
	}
	return true
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

func parseTimeRFC3339(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
