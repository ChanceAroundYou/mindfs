package claude

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

// HasTranscriptTimestamps 恒为 true：claude 转录条目自带 `timestamp` 字段（parseTimeRFC3339 解析），所以 TimestampFloor 对它成立 —— 那是 2026-09-16「重导 09-14 内容」那次事故之后加的护栏。
func (i *Importer) HasTranscriptTimestamps() bool {
	return true
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

// collectClaudeSubagentRelations 扫描转录，收集「子代理 → 其 Task 调用」的映射。
// startOffset>0 时只读该字节位置之后的内容（父转录按轮次追加，新关系只出现在新行），
// subagentRelationSeekBack 是从父转录已读位置往前多读的字节数。子代理与其父 Task 调用的
// 归属关系可能跨越上次同步的读取边界，多读一小段保证关系仍能被识别出来。
// 关系一旦产生即进缓存，代价极小（64KB vs 数十 MB）。
const subagentRelationSeekBack = 64 << 10

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

const claudeProjectDirMaxLength = 200

var errStopJSONL = errors.New("stop jsonl")
