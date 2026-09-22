package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"mindfs/server/internal/apperr"

	agenttypes "mindfs/server/internal/agent/types"
)

type ImporterOptions struct {
	AgentName string
}

type Importer struct {
	agentName string
	baseDir   string
	mu        sync.RWMutex
	index     map[string]claudeSessionFile
	// subagentRelations 缓存「子代理 → 其 Task 调用」的映射。该映射一旦产生即不再变化，
	// 故可跨同步复用：配合增量扫描父转录，避免每次同步都全量重读数十 MB 父转录
	// （实测 72MB 父转录的关系扫描 1.33s/次）。
	subagentRelations map[string]claudeSubagentRelation
	// subagentFileCursors 记录各子代理转录的 (size, mtime)，未变则跳过重读
	// （实测该会话 87 个子代理文件全量重读 0.77s/次）。
	subagentFileCursors map[string]subagentFileStamp
}

type subagentFileStamp struct {
	Size      int64
	ModTimeNs int64
}

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

type importedExchangeLocator struct {
	agenttypes.ImportedExchange
	ClaudeLastMessageUUID string
	// StartOffset 是这条 item 第一条相关条目在转录文件里的字节偏移。增量提交以它为界：
	// 它之前的都已落库，之后的才算新内容（取代了原先「拿会被 tool_result 持续改写的
	// Timestamp 当判据」的做法）。
	StartOffset int64
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
	home, _ := os.UserHomeDir()
	return &Importer{
		agentName:           strings.TrimSpace(opts.AgentName),
		baseDir:             filepath.Join(strings.TrimSpace(home), ".claude", "projects"),
		index:               make(map[string]claudeSessionFile),
		subagentRelations:   make(map[string]claudeSubagentRelation),
		subagentFileCursors: make(map[string]subagentFileStamp),
	}
}

func (i *Importer) AgentName() string {
	return i.agentName
}

func (i *Importer) ListExternalSessions(ctx context.Context, in agenttypes.ListExternalSessionsInput) (agenttypes.ListExternalSessionsResult, error) {
	rootPath := normalizeComparablePath(in.RootPath)
	if rootPath == "" {
		return agenttypes.ListExternalSessionsResult{}, errors.New("root path required")
	}
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
	rootPath := normalizeComparablePath(in.RootPath)
	if rootPath == "" {
		return errors.New("root path required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	files, err := i.scanSessionFiles(ctx, rootPath, in.BeforeTime, in.AfterTime, limit, visit)
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
		return i.importSessionFile(file, in.AfterTimestamp, in.TimestampFloor, in.Cursor, in.ForceRead)
	}
	// 主目录未命中时继续扫描根目录下 .worktree/* 的转录目录：Agent 转录按 spawn cwd
	// 归档（Claude Code: ~/.claude/projects/<slug(cwd)>），worktree 会话落在各自的
	// slug 目录，仅扫主仓库目录会漏掉全部 worktree 会话（同步报 external session not found）。
	for _, candidateRoot := range worktreeCandidateRoots(rootPath) {
		files, err := i.scanSessionFiles(context.Background(), candidateRoot, time.Time{}, time.Time{}, int(^uint(0)>>1), nil)
		if err != nil {
			return agenttypes.ImportedExternalSession{}, err
		}
		for _, file := range files {
			if file.AgentSessionID != targetID {
				continue
			}
			return i.importSessionFile(file, in.AfterTimestamp, in.TimestampFloor, in.Cursor, in.ForceRead)
		}
	}
	return agenttypes.ImportedExternalSession{}, errors.New("external session not found")
}

func (i *Importer) importSessionFile(file claudeSessionFile, after, floor time.Time, previous agenttypes.ExternalSessionCursor, forceRead bool) (agenttypes.ImportedExternalSession, error) {
	cursor, unchanged, err := externalSessionFileCursor(file.Path, previous)
	if err != nil {
		return agenttypes.ImportedExternalSession{}, err
	}
	// 已提交位置决定从哪儿读。旧游标只记了 Offset（=「已读到」），首次升级时沿用之，
	// 否则会被当成「什么都没提交过」而把整份转录重放一遍。
	committed := previous.CommittedOffset
	if committed <= 0 {
		committed = previous.Offset
	}
	if unchanged && !forceRead {
		cursor.CommittedOffset = committed
		return agenttypes.ImportedExternalSession{Agent: i.agentName, AgentSessionID: file.AgentSessionID, Cwd: file.Cwd, Cursor: cursor}, nil
	}
	exchanges, nextCommitted, err := readClaudeImportedExchanges(file.Path, committed, after, floor)
	if err != nil {
		log.Printf("[agent/claude/importer] import session read failed session_id=%s path=%s err=%v", file.AgentSessionID, file.Path, err)
		return agenttypes.ImportedExternalSession{}, err
	}
	cursor.CommittedOffset = nextCommitted
	subagents, err := i.readClaudeImportedSubagents(file.Path, previous.Offset)
	if err != nil {
		log.Printf("[agent/claude/importer] import subagents failed session_id=%s path=%s err=%v", file.AgentSessionID, file.Path, err)
		return agenttypes.ImportedExternalSession{}, err
	}
	return agenttypes.ImportedExternalSession{
		Agent:          i.agentName,
		AgentSessionID: file.AgentSessionID,
		Cwd:            file.Cwd,
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

type claudeSubagentRelation struct {
	AgentID          string
	ParentAgentID    string
	ParentToolCallID string
	Title            string
	Model            string
}

// readClaudeImportedSubagents 发现子代理会话及其与父会话 Task 调用的关系。
// parentStartOffset>0 时父转录只读游标之后的新行：关系映射一旦产生即不变，配合
// i.subagentRelations 缓存即可复用，避免每次同步都全量重读父转录（实测 72MB 父转录
// 的关系扫描 1.33s/次，占本函数总耗时的 67%）。
func (i *Importer) readClaudeImportedSubagents(parentPath string, parentStartOffset int64) ([]agenttypes.ImportedSubagentSession, error) {
	dir := filepath.Join(strings.TrimSuffix(parentPath, filepath.Ext(parentPath)), "subagents")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, apperr.Wrap("read_dir", dir, err)
	}
	pathsByAgentID := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		agentID, err := inspectClaudeSubagentID(path)
		if err != nil {
			return nil, err
		}
		if agentID != "" {
			pathsByAgentID[agentID] = path
		}
	}
	if len(pathsByAgentID) == 0 {
		return nil, nil
	}
	i.mu.Lock()
	if i.subagentRelations == nil {
		i.subagentRelations = make(map[string]claudeSubagentRelation)
	}
	relations := i.subagentRelations
	i.mu.Unlock()
	if err := collectClaudeSubagentRelations(parentPath, "", relations, parentStartOffset); err != nil {
		return nil, err
	}
	for agentID, path := range pathsByAgentID {
		// 子代理转录未变则跳过重读：其关系已在上次扫描时并入 relations 缓存。
		stamp := subagentFileStamp{}
		if info, statErr := os.Stat(path); statErr == nil {
			stamp = subagentFileStamp{Size: info.Size(), ModTimeNs: info.ModTime().UnixNano()}
			i.mu.RLock()
			prev, seen := i.subagentFileCursors[path]
			i.mu.RUnlock()
			if seen && prev == stamp {
				continue
			}
		}
		if err := collectClaudeSubagentRelations(path, agentID, relations, 0); err != nil {
			return nil, err
		}
		i.mu.Lock()
		i.subagentFileCursors[path] = stamp
		i.mu.Unlock()
	}
	items := make([]agenttypes.ImportedSubagentSession, 0, len(relations))
	remaining := make(map[string]claudeSubagentRelation, len(relations))
	for agentID, relation := range relations {
		if _, ok := pathsByAgentID[agentID]; ok {
			remaining[agentID] = relation
		}
	}
	added := make(map[string]bool)
	for len(remaining) > 0 {
		progressed := false
		for agentID, relation := range remaining {
			if relation.ParentAgentID != "" && !added[relation.ParentAgentID] {
				continue
			}
			exchanges, _, err := readClaudeImportedExchanges(pathsByAgentID[agentID], 0, time.Time{}, time.Time{})
			if err != nil {
				return nil, err
			}
			parentID := ""
			if relation.ParentAgentID != "" {
				parentID = "claude-subagent:" + relation.ParentAgentID
			}
			items = append(items, agenttypes.ImportedSubagentSession{
				AgentSessionID:       "claude-subagent:" + agentID,
				ParentAgentSessionID: parentID,
				ParentToolCallID:     relation.ParentToolCallID,
				Title:                relation.Title,
				Model:                relation.Model,
				Exchanges:            exchanges,
			})
			added[agentID] = true
			delete(remaining, agentID)
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return items, nil
}

func inspectClaudeSubagentID(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", apperr.Wrap("open", path, err)
	}
	defer file.Close()
	var agentID string
	err = forEachJSONLLine(file, func(_ int64, line string) error {
		var raw map[string]any
		if json.Unmarshal([]byte(line), &raw) == nil {
			agentID = strings.TrimSpace(asString(raw["agentId"]))
		}
		if agentID != "" {
			return errStopJSONL
		}
		return nil
	})
	if errors.Is(err, errStopJSONL) {
		err = nil
	}
	return agentID, err
}

// collectClaudeSubagentRelations 扫描转录，收集「子代理 → 其 Task 调用」的映射。
// startOffset>0 时只读该字节位置之后的内容（父转录按轮次追加，新关系只出现在新行），
// subagentRelationSeekBack 是从父转录已读位置往前多读的字节数。子代理与其父 Task 调用的
// 归属关系可能跨越上次同步的读取边界，多读一小段保证关系仍能被识别出来。
// 关系一旦产生即进缓存，代价极小（64KB vs 数十 MB）。
const subagentRelationSeekBack = 64 << 10

// 避免每次同步都全量重读父转录。
func collectClaudeSubagentRelations(path, parentAgentID string, relations map[string]claudeSubagentRelation, startOffset int64) error {
	file, err := os.Open(path)
	if err != nil {
		return apperr.Wrap("open", path, err)
	}
	defer file.Close()
	if startOffset > 0 {
		pos := startOffset - subagentRelationSeekBack
		if pos < 0 {
			pos = 0
		}
		if _, err := file.Seek(pos, io.SeekStart); err != nil {
			return apperr.Wrap("seek", path, err)
		}
		if pos > 0 {
			if _, err := bufio.NewReader(file).ReadBytes('\n'); err != nil && !errors.Is(err, io.EOF) {
				return apperr.Wrap("seek_align", path, err)
			}
		}
	}
	callDetails := make(map[string]claudeSubagentRelation)
	return forEachJSONLLine(file, func(_ int64, line string) error {
		var raw map[string]any
		if json.Unmarshal([]byte(line), &raw) != nil {
			return nil
		}
		message, _ := raw["message"].(map[string]any)
		blocks, _ := message["content"].([]any)
		for _, value := range blocks {
			block, _ := value.(map[string]any)
			if block == nil {
				continue
			}
			toolName := strings.ToLower(strings.TrimSpace(asString(block["name"])))
			if strings.EqualFold(asString(block["type"]), "tool_use") && (toolName == "agent" || toolName == "task") {
				input, _ := block["input"].(map[string]any)
				callID := strings.TrimSpace(asString(block["id"]))
				callDetails[callID] = claudeSubagentRelation{
					ParentAgentID:    parentAgentID,
					ParentToolCallID: callID,
					Title:            firstNonEmpty(asString(input["description"]), asString(input["subagent_type"]), "Subagent"),
					Model:            strings.TrimSpace(asString(input["model"])),
				}
			}
		}
		result, _ := raw["toolUseResult"].(map[string]any)
		agentID := strings.TrimSpace(asString(result["agentId"]))
		if agentID == "" {
			return nil
		}
		callID := ""
		for _, value := range blocks {
			block, _ := value.(map[string]any)
			if block != nil && strings.EqualFold(asString(block["type"]), "tool_result") {
				callID = strings.TrimSpace(asString(block["tool_use_id"]))
				break
			}
		}
		relation := callDetails[callID]
		relation.AgentID = agentID
		relation.ParentAgentID = parentAgentID
		relation.ParentToolCallID = callID
		if relation.Title == "" {
			relation.Title = firstNonEmpty(asString(result["agentType"]), "Subagent")
		}
		if relation.Model == "" {
			relation.Model = strings.TrimSpace(asString(result["resolvedModel"]))
		}
		relations[agentID] = relation
		return nil
	})
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
		// 同 ImportExternalSession：worktree 会话转录目录按 spawn cwd 归档，需一并扫描
		for _, candidateRoot := range worktreeCandidateRoots(rootPath) {
			files, err := i.scanSessionFiles(ctx, candidateRoot, time.Time{}, time.Time{}, int(^uint(0)>>1), nil)
			if err != nil {
				return agenttypes.ResolveForkPointOutput{}, err
			}
			for _, candidate := range files {
				if candidate.AgentSessionID == targetID {
					file = candidate
					ok = true
					break
				}
			}
			if ok {
				break
			}
		}
	}
	if !ok {
		return agenttypes.ResolveForkPointOutput{}, errors.New("external session not found")
	}
	items, _, err := readClaudeImportedExchangeLocators(file.Path, 0, time.Time{}, time.Time{})
	if err != nil {
		return agenttypes.ResolveForkPointOutput{}, err
	}
	turns := buildImportedTurns(items)
	if in.AgentTurnIndex > len(turns) {
		return agenttypes.ResolveForkPointOutput{}, errors.New("agent turn index out of range")
	}
	agent := turns[in.AgentTurnIndex-1].Agent
	if strings.TrimSpace(agent.ClaudeLastMessageUUID) == "" {
		return agenttypes.ResolveForkPointOutput{}, errors.New("claude message uuid not found")
	}
	return agenttypes.ResolveForkPointOutput{
		Kind:              agenttypes.ForkPointClaudeMessageUUID,
		AgentSessionID:    targetID,
		ClaudeMessageUUID: agent.ClaudeLastMessageUUID,
	}, nil
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

const claudeProjectDirMaxLength = 200

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

// readClaudeImportedExchanges 从 startOffset 起读取增量条目。
// startOffset<=0 表示全量读；>0 时只解析该字节位置之后的内容，避免每次同步都全量
// 解析整个转录（实测某会话转录 67MB，全量读+解析 1.5-3.3s，而每次打开会话都会触发同步）。
// committedOffset 是「已提交给 MindFS 的字节位置」，从该处往后解析。
// 返回的第二个值是**本次提交之后**的新位置：只有完整、已结束的轮次才会被提交，
// 仍在进行的尾轮留在它自己的起点上等下一轮同步。
//
// committedOffset<=0 表示该会话从未同步过（库里 229/232 个绑定都是这种，游标为空），
// 此时不知道已读到哪，退回到 bootstrapAfter 时间戳判据：只取比库内最新一条更新的回合。
// 本次同步会把游标建起来，之后一律走字节偏移。
func readClaudeImportedExchanges(path string, committedOffset int64, bootstrapAfter, floor time.Time) ([]agenttypes.ImportedExchange, int64, error) {
	locators, committed, err := readClaudeImportedExchangeLocators(path, committedOffset, bootstrapAfter, floor)
	if err != nil {
		return nil, committedOffset, err
	}
	items := make([]agenttypes.ImportedExchange, 0, len(locators))
	for _, item := range locators {
		items = append(items, item.ImportedExchange)
	}
	return items, committed, nil
}

// claudeTranscriptTailClosed 判定转录尾部这一轮是否已经走完。
// Claude Code 的回合以「不含 tool_use 的助手条目」收尾：最后一条相关条目还带 tool_use
// （在等工具结果）、或尾部停在 tool_result / 用户条目上，都说明这一轮还在进行。
// 只有「助手条目带正文且不带 tool_use」才算收尾——纯 thinking 条目不算。
func claudeTranscriptTailClosed(lastRole string, lastHadToolUse, lastHadText bool) bool {
	return lastRole == "assistant" && !lastHadToolUse && lastHadText
}

func readClaudeImportedExchangeLocators(path string, committedOffset int64, bootstrapAfter, floor time.Time) ([]importedExchangeLocator, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, committedOffset, apperr.Wrap("open", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, committedOffset, apperr.Wrap("stat", path, err)
	}
	size := info.Size()
	if committedOffset < 0 || committedOffset > size {
		// 转录被截断/轮转：游标作废，退回全量读。
		committedOffset = 0
	}
	if committedOffset > 0 {
		if _, err := file.Seek(committedOffset, io.SeekStart); err != nil {
			return nil, committedOffset, apperr.Wrap("seek", path, err)
		}
	}

	items := make([]importedExchangeLocator, 0)
	toolLocations := make(map[string]importedToolLocation)
	// 按条目 uuid 去重：同一 uuid 的条目在转录里可能整段重复出现——实测本机某会话
	// 6336 条 assistant 条目中有 1712 条 uuid 重复（重复区间相隔上万行，uuid/时间戳/
	// 内容三者完全相同）。这类远距离重复无法被「相邻同角色才合并」
	// （appendMergedClaudeExchangeLocator）吃掉，会各自成为一个条目被反复落库，
	// 表现为同一段助手文本在会话里出现两次以上。此处同一条目只处理一次。
	seenUUIDs := make(map[string]struct{})
	lastRole := ""
	lastHadToolUse := false
	lastHadText := false
	err = forEachJSONLLine(file, func(lineOffset int64, line string) error {
		// forEachJSONLLine 从当前文件位置起算，而我们已 seek 到 committedOffset，
		// 所以这里要补回基准才是转录里的绝对字节偏移。
		lineOffset += committedOffset
		line = strings.TrimSpace(line)
		if line == "" {
			return nil
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return nil
		}
		role := strings.ToLower(strings.TrimSpace(asString(raw["type"])))
		if role != "user" && role != "assistant" {
			return nil
		}
		uuid := strings.TrimSpace(asString(raw["uuid"]))
		if uuid != "" {
			if _, ok := seenUUIDs[uuid]; ok {
				return nil
			}
			seenUUIDs[uuid] = struct{}{}
		}
		message, _ := raw["message"].(map[string]any)
		if message == nil {
			return nil
		}
		ts := parseTimeRFC3339(asString(raw["timestamp"]))
		if role == "user" {
			lastRole, lastHadToolUse, lastHadText = "user", false, false
			applyClaudeToolResults(items, toolLocations, message["content"], raw["toolUseResult"], ts)
			// 转录自带 isMeta 标记，标明「这条不是用户输入」（CLI 注入的 skill 正文、自动
			// 续跑、命令回显等）。实测 139712 条 user/assistant 条目里 1420 条 isMeta=true，
			// 其中约 1030 条 isMeaningfulClaudeUserText 认不出来。漏进来就是用户没发过的
			// 气泡，还会被「相邻同角色合并」并进相邻的真人消息里。
			if isMeta, _ := raw["isMeta"].(bool); isMeta {
				return nil
			}
			text := extractClaudeImportedUserText(message["content"])
			if text != "" && isMeaningfulClaudeUserText(text) {
				before := len(items)
				items, _, _ = appendMergedClaudeExchangeLocator(items, "user", text, ts, uuid, nil)
				if len(items) > before {
					items[before].StartOffset = lineOffset
				}
			}
			return nil
		}
		lastRole = "assistant"
		lastHadToolUse, lastHadText = false, false
		if blocks, ok := message["content"].([]any); ok {
			for _, block := range blocks {
				item, _ := block.(map[string]any)
				if item == nil {
					continue
				}
				switch strings.TrimSpace(asString(item["type"])) {
				case "tool_use":
					lastHadToolUse = true
				case "text":
					if strings.TrimSpace(asString(item["text"])) != "" {
						lastHadText = true
					}
				}
			}
		}
		text := strings.TrimSpace(extractClaudeMessageText(message["content"]))
		if isAutoContinueAck(text) {
			// 自动续跑一问一答的应答侧。提问侧靠 isMeta 挡住，应答侧没有 isMeta
			// （实测 assistant 条目 isMeta 恒为 false），只能按内容判；不挡就会被
			// 「相邻同角色合并」并进紧邻的真助手文本里。
			text = ""
		}
		aux := extractClaudeToolUseAux(message["content"])
		if text == "" && len(aux) == 0 {
			return nil
		}
		before := len(items)
		var exchangeIndex, auxStart int
		items, exchangeIndex, auxStart = appendMergedClaudeExchangeLocator(
			items,
			"agent",
			text,
			ts,
			uuid,
			aux,
		)
		if len(items) > before {
			items[before].StartOffset = lineOffset
		}
		for index := auxStart; index < len(items[exchangeIndex].Aux); index++ {
			toolCall := items[exchangeIndex].Aux[index].ToolCall
			if toolCall == nil || strings.TrimSpace(toolCall.CallID) == "" {
				continue
			}
			toolLocations[strings.TrimSpace(toolCall.CallID)] = importedToolLocation{
				ExchangeIndex: exchangeIndex,
				AuxIndex:      index,
			}
		}
		return nil
	})
	if err != nil {
		return nil, committedOffset, err
	}
	// 已提交位置：只有在转录尾部已收尾时才推进过最后一条 item。
	// 尾轮还在进行（正在跑工具 / 刚拿到结果还没续写）就先不落库 —— 它的内容会继续变，
	// 落了就是半成品，而且下次同步会因为它「又变了」而被当成新内容再落一遍。
	committed := size
	if n := len(items); n > 0 {
		tail := items[n-1]
		if tail.Role == "agent" && !claudeTranscriptTailClosed(lastRole, lastHadToolUse, lastHadText) {
			if tail.StartOffset > committedOffset {
				committed = tail.StartOffset
			} else {
				committed = committedOffset
			}
			items = items[:n-1]
		}
	}
	filtered := make([]importedExchangeLocator, 0, len(items))
	// floor 是给「live-owned 会话的兜底补齐」用的：那种会话的游标冻结已久，只按偏移读会把
	// 早已落库的回合整段重导（实测 2026-09-16 BP 会话重导了 09-14 的内容，与实时路径写的
	// 行并排显示成重复）。有地板时：游标决定从哪开始读，地板决定读到的东西算不算数。
	passesFloor := func(item importedExchangeLocator) bool {
		if floor.IsZero() {
			return true
		}
		return !item.Timestamp.IsZero() && item.Timestamp.After(floor)
	}
	if committedOffset > 0 {
		for _, item := range items {
			if item.StartOffset < committedOffset {
				continue
			}
			if !passesFloor(item) {
				continue
			}
			filtered = append(filtered, item)
		}
		return filtered, committed, nil
	}
	// 引导：该会话还没有游标，用库内最新时间戳兜底（旧行为，只此一次）。
	if bootstrapAfter.IsZero() {
		return items, committed, nil
	}
	for _, item := range items {
		if item.Timestamp.IsZero() || !item.Timestamp.After(bootstrapAfter) || !passesFloor(item) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered, committed, nil
}

var errStopJSONL = errors.New("stop jsonl")

func forEachJSONLLine(file *os.File, fn func(offset int64, line string) error) error {
	reader := bufio.NewReader(file)
	var offset int64
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if callErr := fn(offset, string(line)); callErr != nil {
				return callErr
			}
			offset += int64(len(line))
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

func extractClaudeMessageText(raw any) string {
	if text := strings.TrimSpace(asString(raw)); text != "" {
		return text
	}
	parts, _ := raw.([]any)
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		item, _ := part.(map[string]any)
		if item == nil {
			continue
		}
		if strings.TrimSpace(asString(item["type"])) != "text" {
			continue
		}
		if text := strings.TrimSpace(asString(item["text"])); text != "" {
			lines = append(lines, text)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n\n"))
}

func extractClaudeUserPreview(raw any) string {
	if text := strings.TrimSpace(asString(raw)); text != "" {
		if isMeaningfulClaudeUserText(text) {
			return text
		}
		return ""
	}
	parts, _ := raw.([]any)
	for index := len(parts) - 1; index >= 0; index-- {
		item, _ := parts[index].(map[string]any)
		if item == nil || strings.TrimSpace(asString(item["type"])) != "text" {
			continue
		}
		text := strings.TrimSpace(asString(item["text"]))
		if isMeaningfulClaudeUserText(text) {
			return text
		}
	}
	return ""
}

func extractClaudeImportedUserText(raw any) string {
	if text := strings.TrimSpace(asString(raw)); text != "" {
		if isMeaningfulClaudeUserText(text) {
			return text
		}
		return ""
	}
	parts, _ := raw.([]any)
	texts := make([]string, 0, len(parts))
	for _, value := range parts {
		item, _ := value.(map[string]any)
		if item == nil || strings.TrimSpace(asString(item["type"])) != "text" {
			continue
		}
		text := strings.TrimSpace(asString(item["text"]))
		if isMeaningfulClaudeUserText(text) {
			texts = append(texts, text)
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n\n"))
}

func extractClaudeToolUseAux(raw any) []agenttypes.ImportedExchangeAux {
	parts, _ := raw.([]any)
	aux := make([]agenttypes.ImportedExchangeAux, 0)
	textParts := make([]string, 0)
	for _, part := range parts {
		item, _ := part.(map[string]any)
		if item == nil {
			continue
		}
		switch strings.TrimSpace(asString(item["type"])) {
		case "text":
			if text := strings.TrimSpace(asString(item["text"])); text != "" {
				textParts = append(textParts, text)
			}
		case "tool_use":
			callID := strings.TrimSpace(asString(item["id"]))
			name := strings.TrimSpace(asString(item["name"]))
			if callID == "" {
				continue
			}
			kind := mapToolKind(name)
			if kind != agenttypes.ToolKindExecute &&
				kind != agenttypes.ToolKindEdit &&
				kind != agenttypes.ToolKindThink &&
				kind != agenttypes.ToolKindAskUser {
				continue
			}
			input, _ := json.Marshal(item["input"])
			toolCall := newRunningToolCall(callID, name, "tool_use", input)
			aux = append(aux, agenttypes.ImportedExchangeAux{
				Line:     importedAssistantLine(strings.Join(textParts, "\n\n")),
				ToolCall: &toolCall,
			})
		}
	}
	return aux
}

func applyClaudeToolResults(
	items []importedExchangeLocator,
	locations map[string]importedToolLocation,
	raw any,
	toolUseResult any,
	timestamp time.Time,
) {
	parts, _ := raw.([]any)
	for _, part := range parts {
		item, _ := part.(map[string]any)
		if item == nil || strings.TrimSpace(asString(item["type"])) != "tool_result" {
			continue
		}
		callID := strings.TrimSpace(asString(item["tool_use_id"]))
		location, ok := locations[callID]
		if !ok || location.ExchangeIndex < 0 || location.ExchangeIndex >= len(items) {
			continue
		}
		exchange := &items[location.ExchangeIndex]
		if location.AuxIndex < 0 || location.AuxIndex >= len(exchange.Aux) {
			continue
		}
		aux := &exchange.Aux[location.AuxIndex]
		if aux.ToolCall == nil {
			continue
		}
		if !timestamp.IsZero() {
			exchange.Timestamp = timestamp
		}
		toolCall := *aux.ToolCall
		output := summarizeToolResult(toolCall.Kind, item["content"])
		if output == "" {
			output = summarizeGenericToolResult(item["content"])
		}
		isError, _ := item["is_error"].(bool)
		if isError {
			toolCall.Status = "failed"
		} else {
			toolCall.Status = "complete"
		}
		if strings.TrimSpace(output) != "" {
			toolCall.Meta = mergeToolCallMeta(toolCall.Meta, map[string]any{"output": output})
			if toolCall.Kind != agenttypes.ToolKindEdit || len(toolCall.Content) == 0 {
				toolCall.Content = []agenttypes.ToolCallContentItem{{Type: "text", Text: output}}
			}
		}
		if toolCall.Kind == agenttypes.ToolKindAskUser {
			if answers := importedClaudeAskUserAnswers(toolCall, toolUseResult); len(answers) > 0 {
				toolCall.Meta = mergeToolCallMeta(toolCall.Meta, map[string]any{"answers": answers})
			}
		}
		aux.ToolCall = &toolCall
	}
}

func importedClaudeAskUserAnswers(toolCall agenttypes.ToolCall, raw any) map[string]string {
	result, _ := raw.(map[string]any)
	rawAnswers, _ := result["answers"].(map[string]any)
	if len(rawAnswers) == 0 {
		return nil
	}
	input := importedClaudeToolInput(toolCall)
	questions, _ := input["questions"].([]any)
	answers := make(map[string]string)
	for index, value := range questions {
		question, _ := value.(map[string]any)
		if question == nil {
			continue
		}
		questionText := strings.TrimSpace(asString(question["question"]))
		if questionText == "" {
			continue
		}
		answer := strings.TrimSpace(asString(rawAnswers[questionText]))
		if answer != "" {
			answers[fmt.Sprintf("q_%d", index)] = answer
		}
	}
	return answers
}

func importedClaudeToolInput(toolCall agenttypes.ToolCall) map[string]any {
	if toolCall.Meta == nil {
		return nil
	}
	raw := strings.TrimSpace(asString(toolCall.Meta["input"]))
	if raw == "" {
		return nil
	}
	var input map[string]any
	if json.Unmarshal([]byte(raw), &input) != nil {
		return nil
	}
	return input
}

func importedAssistantLine(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}

// isAutoContinueAck 判定自动续跑一问一答的应答侧：CLI 在会话空闲时自己插
// 「Continue from where you left off.」→「No response requested.」。提问侧由 isMeta
// 挡掉，应答侧没有 isMeta（assistant 条目的 isMeta 恒为 false），只能按内容判。
func isAutoContinueAck(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n")))
	return strings.TrimSuffix(normalized, ".") == "no response requested"
}

// 中断标记等 CLI 自注入内容的名单与剥离逻辑见 agenttypes.TranscriptNoisePrefixes：
// 一侧定义、导入侧与判重侧共用，避免名单走散。
func isMeaningfulClaudeUserText(text string) bool {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return false
	}
	// 整条就是 CLI 标记 → 不是用户输入，整条不发射。标记若与真人正文粘在同一条目里，
	// 剥完不会为空，这里会保留（整条丢掉会连正文一起丢）。
	if agenttypes.IsTranscriptNoiseEntry(text) {
		return false
	}
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "<local-command-caveat>") ||
		strings.HasPrefix(lower, "<command-name>") ||
		strings.HasPrefix(lower, "<local-command-stdout>") ||
		strings.HasPrefix(lower, "<local-command-stderr>") ||
		// CLI 的子代理完成通知：整块由 CLI 注入（isMeta 为空，前面那道闸拦不住），
		// 正文是子代理的报告。子代理转录已作为子会话导入（parent_tool_call_id 指向
		// Task 调用），这里再落一条就是「用户气泡里装着助手正文」的乱格式块。
		// 同一件事还有一种带前言的外形：后台任务事件会被包上「SYSTEM NOTIFICATION - NOT
		// USER INPUT」抬头（它自己就写明不是用户输入）。
		strings.HasPrefix(lower, "<task-notification>") ||
		strings.HasPrefix(lower, "[system notification - not user input]") ||
		strings.HasPrefix(lower, "this session was migrated from elsewhere.") ||
		strings.HasPrefix(lower, "this session is being continued from a previous conversation") {
		return false
	}
	if strings.Contains(lower, "<command-message>") || strings.Contains(lower, "<command-args>") {
		return false
	}
	if strings.Contains(lower, "<local-command-stdout>") || strings.Contains(lower, "<local-command-stderr>") {
		return false
	}
	if strings.Contains(lower, "<local-command-caveat>") {
		return false
	}
	if strings.Contains(lower, "\"type\": \"tool_result\"") || strings.Contains(lower, "'type': 'tool_result'") {
		return false
	}
	return true
}

func appendMergedClaudeExchangeLocator(
	items []importedExchangeLocator,
	role, content string,
	ts time.Time,
	uuid string,
	aux []agenttypes.ImportedExchangeAux,
) ([]importedExchangeLocator, int, int) {
	content = strings.TrimSpace(content)
	if content == "" && len(aux) == 0 {
		return items, -1, 0
	}
	if len(items) > 0 && items[len(items)-1].Role == role {
		last := &items[len(items)-1]
		lineOffset := importedAssistantLine(last.Content)
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
		if strings.TrimSpace(uuid) != "" {
			last.ClaudeLastMessageUUID = strings.TrimSpace(uuid)
		}
		auxStart := len(last.Aux)
		for _, item := range aux {
			item.Line += lineOffset
			last.Aux = append(last.Aux, item)
		}
		return items, len(items) - 1, auxStart
	}
	items = append(items, importedExchangeLocator{
		ImportedExchange: agenttypes.ImportedExchange{
			Role:      role,
			Content:   content,
			Timestamp: ts,
			Aux:       aux,
		},
		ClaudeLastMessageUUID: strings.TrimSpace(uuid),
	})
	return items, len(items) - 1, 0
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
