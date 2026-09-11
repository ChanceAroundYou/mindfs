package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	agenttypes "mindfs/server/internal/agent/types"
	configpkg "mindfs/server/internal/config"
	"mindfs/server/internal/fs"

	_ "modernc.org/sqlite"
)

const (
	sessionDBPath    = "sessions/session-list.db"
	sessionDBLinkExt = ".link"
	exchangeFileTpl  = "sessions/%s.jsonl"
	auxFileTpl       = "sessions/%s.aux.jsonl"
	// maxExchangeLineBytes 单条 JSONL 上限（tool call 大 content），bufio.Scanner 兜底。
	maxExchangeLineBytes = 64 << 20
	selectSessionSQL     = `
	SELECT key, type, parent_session_key, parent_tool_call_id, source, task_id, model, shell, plan_mode, name, related_files_json, related_worktree_json, last_context_window_total_tokens, last_context_window_model_context_window, pinned_at, created_at, updated_at, closed_at
	FROM sessions`
	deleteSessionSQL = `
DELETE FROM sessions
WHERE key = ?`
	deleteBindingsBySessionSQL = `
DELETE FROM session_agent_bindings
WHERE session_key = ?`
	upsertSessionMetaSQL = `
INSERT INTO sessions (
		key, type, parent_session_key, parent_tool_call_id, source, task_id, model, shell, plan_mode, name, related_files_json, related_worktree_json, last_context_window_total_tokens, last_context_window_model_context_window, pinned_at, created_at, updated_at, closed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET
	type = excluded.type,
	parent_session_key = excluded.parent_session_key,
	parent_tool_call_id = excluded.parent_tool_call_id,
	source = excluded.source,
	task_id = excluded.task_id,
	model = excluded.model,
	shell = excluded.shell,
	plan_mode = excluded.plan_mode,
	name = excluded.name,
	related_files_json = excluded.related_files_json,
	related_worktree_json = excluded.related_worktree_json,
	last_context_window_total_tokens = excluded.last_context_window_total_tokens,
	last_context_window_model_context_window = excluded.last_context_window_model_context_window,
	pinned_at = excluded.pinned_at,
	created_at = excluded.created_at,
	updated_at = excluded.updated_at,
	closed_at = excluded.closed_at`
	sessionTableSchema = `
CREATE TABLE IF NOT EXISTS sessions (
	key TEXT PRIMARY KEY,
	type TEXT NOT NULL,
	parent_session_key TEXT NOT NULL DEFAULT '',
	parent_tool_call_id TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL DEFAULT '',
	task_id TEXT NOT NULL DEFAULT '',
	model TEXT NOT NULL DEFAULT '',
	shell TEXT NOT NULL DEFAULT '',
	plan_mode INTEGER NOT NULL DEFAULT 0,
	name TEXT NOT NULL,
	related_files_json TEXT NOT NULL,
	related_worktree_json TEXT NOT NULL DEFAULT '',
	last_context_window_total_tokens INTEGER NOT NULL DEFAULT 0,
	last_context_window_model_context_window INTEGER NOT NULL DEFAULT 0,
	pinned_at TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	closed_at TEXT
);`
	sessionNameAliasTableSchema = `
CREATE TABLE IF NOT EXISTS session_name_aliases (
	key TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	agent TEXT NOT NULL DEFAULT '',
	agent_session_id TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL
);`
	sessionExternalNameTableSchema = `
CREATE TABLE IF NOT EXISTS session_external_names (
	agent TEXT NOT NULL,
	agent_session_id TEXT NOT NULL,
	name TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	PRIMARY KEY (agent, agent_session_id)
);`
	agentBindingTableSchema = `
CREATE TABLE IF NOT EXISTS session_agent_bindings (
	session_key TEXT NOT NULL,
	agent TEXT NOT NULL,
	agent_session_id TEXT NOT NULL,
	agent_ctx_seq INTEGER NOT NULL DEFAULT 0,
	external_source_path TEXT NOT NULL DEFAULT '',
	external_source_offset INTEGER NOT NULL DEFAULT 0,
	external_source_mtime_ns INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (session_key, agent)
);`
	upsertAgentBindingSQL = `
INSERT INTO session_agent_bindings (
	session_key, agent, agent_session_id, agent_ctx_seq
) VALUES (?, ?, ?, ?)
ON CONFLICT(session_key, agent) DO UPDATE SET
	agent_session_id = excluded.agent_session_id,
	agent_ctx_seq = excluded.agent_ctx_seq`
	selectAllAgentBindingsSQL = `
SELECT session_key, agent, agent_session_id, agent_ctx_seq, external_source_path, external_source_offset, external_source_mtime_ns
FROM session_agent_bindings`
	selectAgentBindingSQL = `
SELECT session_key, agent, agent_session_id, agent_ctx_seq, external_source_path, external_source_offset, external_source_mtime_ns
FROM session_agent_bindings
WHERE session_key = ? AND agent = ?`
	selectAgentBindingsBySessionSQL = `
SELECT session_key, agent, agent_session_id, agent_ctx_seq, external_source_path, external_source_offset, external_source_mtime_ns
FROM session_agent_bindings
WHERE session_key = ?`
	selectBindingByAgentSessionSQL = `
SELECT session_key, agent, agent_session_id, agent_ctx_seq, external_source_path, external_source_offset, external_source_mtime_ns
FROM session_agent_bindings
WHERE agent = ? AND agent_session_id = ?
LIMIT 1`
	selectSessionNameAliasSQL = `
SELECT name FROM session_name_aliases WHERE key = ?`
	selectSessionNameAliasByAgentSQL = `
SELECT name FROM session_name_aliases WHERE agent = ? AND agent_session_id = ? LIMIT 1`
	upsertSessionNameAliasSQL = `
INSERT INTO session_name_aliases (key, name, agent, agent_session_id, updated_at) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(key) DO UPDATE SET name = excluded.name, agent = excluded.agent, agent_session_id = excluded.agent_session_id, updated_at = excluded.updated_at`
	upsertExternalSessionNameSQL = `
INSERT INTO session_external_names (agent, agent_session_id, name, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT(agent, agent_session_id) DO UPDATE SET
	name = excluded.name,
	updated_at = excluded.updated_at`
	selectExternalSessionNameSQL = `
SELECT name FROM session_external_names WHERE agent = ? AND agent_session_id = ?`
)

var openSQLiteDB = func(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path)
}

var mindFSConfigDir = configpkg.MindFSConfigDir

// exchangeFileCursor 记录 exchanges/aux JSONL 文件的读取游标，用于增量读取：
// 文件 (size, mtime) 未变 → 复用内存缓存；追加 → 从 Offset 续读尾部，避免每次全量读盘+反序列化。
type exchangeFileCursor struct {
	Offset    int64
	Size      int64
	ModTimeNs int64
	MaxSeq    int
}

type Manager struct {
	root             fs.RootInfo
	mu               sync.Mutex
	db               *sql.DB
	sessions         map[string]*Session
	exchangeCursors  map[string]exchangeFileCursor
	pendingToolCalls map[string]map[string]agenttypes.ToolCall
	now              func() time.Time
}

type CreateInput struct {
	Key              string
	Type             string
	ParentSessionKey string
	ParentToolCallID string
	Source           string
	TaskID           string
	Agent            string
	Model            string
	Shell            string
	PlanMode         bool
	Name             string
}

type AgentBinding struct {
	SessionKey            string `json:"session_key"`
	Agent                 string `json:"agent"`
	AgentSessionID        string `json:"agent_session_id"`
	AgentCtxSeq           int    `json:"agent_ctx_seq"`
	ExternalSourcePath    string `json:"external_source_path,omitempty"`
	ExternalSourceOffset  int64  `json:"external_source_offset,omitempty"`
	ExternalSourceMtimeNS int64  `json:"external_source_mtime_ns,omitempty"`
}

func (b AgentBinding) ExternalCursor() agenttypes.ExternalSessionCursor {
	return agenttypes.ExternalSessionCursor{SourcePath: b.ExternalSourcePath, Offset: b.ExternalSourceOffset, ModTimeUnixNano: b.ExternalSourceMtimeNS}
}

type ListOptions struct {
	BeforeTime       time.Time
	AfterTime        time.Time
	ParentSessionKey string
	TopLevelOnly     bool
	Limit            int
}

func NewManager(root fs.RootInfo, opts ...Option) *Manager {
	m := &Manager{
		root:             root,
		sessions:         make(map[string]*Session),
		exchangeCursors:  make(map[string]exchangeFileCursor),
		pendingToolCalls: make(map[string]map[string]agenttypes.ToolCall),
		now:              time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

type Option func(*Manager)

func WithClock(now func() time.Time) Option {
	return func(m *Manager) {
		m.now = now
	}
}

func (m *Manager) Create(_ context.Context, input CreateInput) (*Session, error) {
	if strings.TrimSpace(input.Type) == "" {
		return nil, errors.New("session type required")
	}
	key := input.Key
	if key == "" {
		key = generateKey()
	}
	now := m.now().UTC()
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = "New Session"
	}
	initialAgent := strings.TrimSpace(input.Agent)
	agentCtxSeq := map[string]int{}
	if initialAgent != "" {
		agentCtxSeq[initialAgent] = 0
	}
	session := &Session{
		Key:              key,
		Type:             input.Type,
		ParentSessionKey: strings.TrimSpace(input.ParentSessionKey),
		ParentToolCallID: strings.TrimSpace(input.ParentToolCallID),
		Source:           strings.TrimSpace(input.Source),
		TaskID:           strings.TrimSpace(input.TaskID),
		AgentCtxSeq:      agentCtxSeq,
		Model:            strings.TrimSpace(input.Model),
		Shell:            strings.TrimSpace(input.Shell),
		PlanMode:         input.PlanMode,
		Name:             name,
		Exchanges:        []Exchange{},
		RelatedFiles:     []RelatedFile{},
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.createSessionUnsafe(session); err != nil {
		return nil, err
	}
	m.sessions[session.Key] = session
	// Persist custom name so delete+reimport can resume it via LookupAliasForAgent.
	// Default "New Session" is omitted to avoid alias pollution.
	if trimmed := strings.TrimSpace(name); trimmed != "" && trimmed != "New Session" {
		_ = m.upsertSessionNameAliasUnsafe(session.Key, trimmed)
	}
	return session, nil
}

func (m *Manager) Get(_ context.Context, key string, afterSeq int) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getSessionUnsafe(key, afterSeq)
}

type SessionWindowMeta struct {
	Total   int  `json:"total"`
	HasMore bool `json:"hasMore"`
	MinSeq  int  `json:"minSeq"`
	MaxSeq  int  `json:"maxSeq"`
}

// perfTraceThreshold 是临时性能插桩的阈值：超过它才打一行分段耗时日志，
// 便于定位「窗口加载慢」到底花在等锁还是实际工作（定位完成后连同插桩一并移除）。
const perfTraceThreshold = 300 * time.Millisecond

func (m *Manager) GetWindow(_ context.Context, key string, beforeSeq, limit, latest int) (*Session, *SessionWindowMeta, error) {
	start := time.Now()
	m.mu.Lock()
	waited := time.Since(start)
	defer m.mu.Unlock()
	sess, meta, err := m.getSessionWindowUnsafe(key, beforeSeq, limit, latest)
	if total := time.Since(start); total > perfTraceThreshold {
		log.Printf("[perf] GetWindow key=%s wait_lock=%v work=%v total=%v", key, waited, total-waited, total)
	}
	return sess, meta, err
}

func (m *Manager) CountExchanges(key string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(key) == "" {
		return 0, errors.New("session key required")
	}
	path, err := m.exchangePath(key)
	if err != nil {
		return 0, err
	}
	if cached, ok := m.sessions[key]; ok && cached != nil && len(cached.Exchanges) > 0 {
		if cursor, has := m.exchangeCursors[path]; has && cursor.MaxSeq > 0 {
			if info, statErr := m.root.StatMetaFile(path); statErr == nil {
				if info.Size() == cursor.Size && info.ModTime().UnixNano() == cursor.ModTimeNs {
					return len(cached.Exchanges), nil
				}
			}
		}
	}
	exchanges, _, err := m.readExchangesFull(path)
	if err != nil {
		return 0, err
	}
	return len(exchanges), nil
}

func (m *Manager) GetExchangeAuxWindow(_ context.Context, key string, seqSet map[int]bool) (map[int][]ExchangeAux, error) {
	start := time.Now()
	m.mu.Lock()
	waited := time.Since(start)
	defer m.mu.Unlock()
	out, err := m.loadExchangeAuxWindow(key, seqSet)
	if total := time.Since(start); total > perfTraceThreshold {
		log.Printf("[perf] GetExchangeAuxWindow key=%s seqs=%d wait_lock=%v work=%v total=%v", key, len(seqSet), waited, total-waited, total)
	}
	return out, err
}

// GetMeta 只加载 SQLite meta（不含 exchanges 文件），用于列表/名称查询等不需要完整会话的场景。
func (m *Manager) GetMeta(_ context.Context, key string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getSessionMetaWithBindingsUnsafe(key)
}

func (m *Manager) GetExchangeAux(_ context.Context, key string, afterSeq int) (map[int][]ExchangeAux, error) {
	start := time.Now()
	m.mu.Lock()
	waited := time.Since(start)
	defer m.mu.Unlock()
	out, err := m.loadExchangeAux(key, afterSeq)
	if total := time.Since(start); total > perfTraceThreshold {
		log.Printf("[perf] GetExchangeAux key=%s afterSeq=%d entries=%d wait_lock=%v work=%v total=%v", key, afterSeq, len(out), waited, total-waited, total)
	}
	return out, err
}

func (m *Manager) GetFullToolCall(_ context.Context, key, callID string) (*agenttypes.ToolCall, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("session key required")
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return nil, errors.New("tool call id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if found, ok := m.pendingFullToolCallUnsafe(key, callID); ok {
		return found, nil
	}
	aux, err := m.loadExchangeAuxEntries(key, 0)
	if err != nil {
		return nil, err
	}
	var found *agenttypes.ToolCall
	for _, item := range aux {
		if item.ToolCall == nil || strings.TrimSpace(item.ToolCall.CallID) != callID {
			continue
		}
		next := *item.ToolCall
		if found == nil {
			found = &next
			continue
		}
		merged := mergeToolCall(*found, next)
		found = &merged
	}
	if found == nil {
		return nil, os.ErrNotExist
	}
	return found, nil
}

func (m *Manager) UpsertPendingExchangeAux(_ context.Context, sessionKey string, aux ExchangeAux) error {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return errors.New("session key required")
	}
	if aux.ToolCall == nil || strings.TrimSpace(aux.ToolCall.CallID) == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingToolCalls == nil {
		m.pendingToolCalls = make(map[string]map[string]agenttypes.ToolCall)
	}
	callID := strings.TrimSpace(aux.ToolCall.CallID)
	next := cloneToolCall(*aux.ToolCall)
	byCallID := m.pendingToolCalls[sessionKey]
	if byCallID == nil {
		byCallID = make(map[string]agenttypes.ToolCall)
		m.pendingToolCalls[sessionKey] = byCallID
	}
	if existing, ok := byCallID[callID]; ok {
		next = mergeToolCall(existing, next)
	}
	byCallID[callID] = next
	return nil
}

func (m *Manager) MarkPendingAskUserAnswered(_ context.Context, sessionKey, callID string, answers map[string]string, answeredAt time.Time) error {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return errors.New("session key required")
	}
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return errors.New("tool call id required")
	}
	cleanAnswers := make(map[string]string, len(answers))
	for key, value := range answers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			cleanAnswers[key] = value
		}
	}
	if len(cleanAnswers) == 0 {
		return errors.New("answers required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingToolCalls == nil || m.pendingToolCalls[sessionKey] == nil {
		return errors.New("pending tool call not found")
	}
	existing, ok := m.pendingToolCalls[sessionKey][callID]
	if !ok {
		return errors.New("pending tool call not found")
	}
	if existing.Kind != "" && existing.Kind != agenttypes.ToolKindAskUser {
		return errors.New("pending tool call is not ask_user")
	}
	meta := make(map[string]any, len(existing.Meta)+2)
	for key, value := range existing.Meta {
		meta[key] = value
	}
	meta["answers"] = cleanAnswers
	if !answeredAt.IsZero() {
		meta["answeredAt"] = answeredAt.UTC().Format(time.RFC3339Nano)
	}
	existing.Kind = agenttypes.ToolKindAskUser
	existing.Status = "complete"
	existing.Meta = meta
	m.pendingToolCalls[sessionKey][callID] = existing
	return nil
}

func (m *Manager) ClearPendingExchangeAux(_ context.Context, sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pendingToolCalls, sessionKey)
}

func (m *Manager) List(_ context.Context, opts ListOptions) ([]*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listSessionsUnsafe(opts)
}

func (m *Manager) ListPinned(_ context.Context, opts ListOptions) ([]*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listPinnedSessionsUnsafe(opts)
}

func (m *Manager) Count(_ context.Context, opts ListOptions) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.countSessionsUnsafe(opts)
}

func (m *Manager) ListMetas(_ context.Context) ([]*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listSessionMetasUnsafe()
}

func (m *Manager) Search(_ context.Context, opts SearchOptions) ([]SearchHit, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" || utf8.RuneCountInString(query) < 2 {
		return []SearchHit{}, nil
	}
	limit := normalizeSearchLimit(opts.Limit)
	qLower := strings.ToLower(query)

	m.mu.Lock()
	sessions, err := m.listSessionMetasUnsafe()
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}

	nameHits := make([]SearchHit, 0, limit)
	hitsByKey := make(map[string]SearchHit, limit)
	for _, item := range sessions {
		score := scoreSessionName(item.Name, qLower)
		if score <= 0 {
			continue
		}
		hit := buildSearchHit(item, "name", score, 0, item.Name)
		nameHits = append(nameHits, hit)
		hitsByKey[item.Key] = hit
	}
	sortSearchHits(nameHits)
	if len(nameHits) >= limit {
		return append([]SearchHit(nil), nameHits[:limit]...), nil
	}

	for _, item := range sessions {
		if len(hitsByKey) >= limit {
			break
		}
		if _, exists := hitsByKey[item.Key]; exists {
			continue
		}
		hit, ok, err := m.searchSessionContent(item, qLower)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		hitsByKey[item.Key] = hit
	}

	results := make([]SearchHit, 0, len(hitsByKey))
	for _, hit := range hitsByKey {
		results = append(results, hit)
	}
	sortSearchHits(results)
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

type exchangeModelDisplayNameContextKey struct{}
type exchangeTokenUsageContextKey struct{}

func WithExchangeModelDisplayName(ctx context.Context, displayName string) context.Context {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return ctx
	}
	return context.WithValue(ctx, exchangeModelDisplayNameContextKey{}, displayName)
}

func exchangeModelDisplayNameFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(exchangeModelDisplayNameContextKey{}).(string)
	return strings.TrimSpace(value)
}

func WithExchangeTokenUsage(ctx context.Context, usage *agenttypes.TokenUsage) context.Context {
	if ctx == nil || usage == nil {
		return ctx
	}
	copy := *usage
	return context.WithValue(ctx, exchangeTokenUsageContextKey{}, &copy)
}

func exchangeTokenUsageFromContext(ctx context.Context) *agenttypes.TokenUsage {
	if ctx == nil {
		return nil
	}
	usage, _ := ctx.Value(exchangeTokenUsageContextKey{}).(*agenttypes.TokenUsage)
	if usage == nil {
		return nil
	}
	copy := *usage
	return &copy
}

func (m *Manager) AddExchangeForAgent(ctx context.Context, session *Session, role, content, agent, mode, effort, fastService string) error {
	return m.addExchangeForAgentAt(session, role, content, agent, exchangeModelDisplayNameFromContext(ctx), exchangeTokenUsageFromContext(ctx), mode, effort, fastService, time.Time{})
}

func (m *Manager) AddExchangeForAgentAt(ctx context.Context, session *Session, role, content, agent, mode, effort, fastService string, timestamp time.Time) error {
	return m.addExchangeForAgentAt(session, role, content, agent, exchangeModelDisplayNameFromContext(ctx), exchangeTokenUsageFromContext(ctx), mode, effort, fastService, timestamp)
}

func (m *Manager) addExchangeForAgentAt(session *Session, role, content, agent, modelDisplayName string, tokenUsage *agenttypes.TokenUsage, mode, effort, fastService string, timestamp time.Time) error {
	if session == nil || strings.TrimSpace(session.Key) == "" {
		return errors.New("session required")
	}
	normalizedRole := strings.ToLower(strings.TrimSpace(role))
	if strings.TrimSpace(content) == "" && normalizedRole != "agent" && normalizedRole != "assistant" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.getSessionUnsafe(session.Key, 0)
	if err != nil {
		return err
	}
	session = current
	if session.ClosedAt != nil {
		session.ClosedAt = nil
	}
	resolvedAgent := strings.TrimSpace(agent)
	nextSeq := len(session.Exchanges) + 1
	ts := timestamp.UTC()
	if ts.IsZero() {
		ts = m.now().UTC()
	}
	record := Exchange{
		Seq:              nextSeq,
		Role:             role,
		Agent:            resolvedAgent,
		Model:            session.Model,
		ModelDisplayName: strings.TrimSpace(modelDisplayName),
		Mode:             strings.TrimSpace(mode),
		Effort:           strings.TrimSpace(effort),
		FastService:      fastService,
		Content:          content,
		TokenUsage:       tokenUsage,
		Timestamp:        ts,
	}
	if err := m.appendExchange(session.Key, record); err != nil {
		log.Printf("[session/store] append.error session=%s seq=%d role=%s agent=%s err=%v", session.Key, record.Seq, role, resolvedAgent, err)
		return err
	}
	session.Exchanges = append(session.Exchanges, record)
	session.UpdatedAt = record.Timestamp
	if resolvedAgent != "" {
		if session.AgentCtxSeq == nil {
			session.AgentCtxSeq = map[string]int{}
		}
		if _, ok := session.AgentCtxSeq[resolvedAgent]; !ok {
			session.AgentCtxSeq[resolvedAgent] = 0
		}
	}
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return err
	}
	return nil
}

func (m *Manager) AddExchangeAux(_ context.Context, sessionKey string, aux ExchangeAux) error {
	if strings.TrimSpace(sessionKey) == "" {
		return errors.New("session key required")
	}
	if aux.Seq <= 0 {
		return errors.New("aux seq required")
	}
	if aux.ToolCall == nil && strings.TrimSpace(aux.Thought) == "" && aux.Todo == nil && aux.Plan == nil && aux.Compact == nil {
		return errors.New("aux content required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appendExchangeAux(sessionKey, aux)
}

func (m *Manager) AddRelatedFile(_ context.Context, key string, file RelatedFile) error {
	if strings.TrimSpace(file.Path) == "" {
		return errors.New("file path required")
	}
	file.Path = strings.TrimSpace(file.Path)
	file.RootID = strings.TrimSpace(file.RootID)
	file.RepoPath = cleanRelatedRepoPath(file.RepoPath)
	file.RepoName = strings.TrimSpace(file.RepoName)
	file.RepoKind = strings.TrimSpace(file.RepoKind)
	file.Head = strings.TrimSpace(file.Head)
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return err
	}
	sessionChanged := addRelatedFileToSession(session, file)
	parentChanged := false
	var parent *Session
	parentKey := strings.TrimSpace(session.ParentSessionKey)
	if parentKey != "" && parentKey != session.Key {
		parent, err = m.getSessionUnsafe(parentKey, 0)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && parent != nil {
			parentChanged = addRelatedFileToSession(parent, file)
		}
	}
	if !sessionChanged && !parentChanged {
		return nil
	}
	if sessionChanged {
		if err := m.upsertSessionMetaUnsafe(session); err != nil {
			return err
		}
	}
	if parentChanged {
		if err := m.upsertSessionMetaUnsafe(parent); err != nil {
			return err
		}
	}
	return nil
}

func addRelatedFileToSession(session *Session, file RelatedFile) bool {
	if session == nil {
		return false
	}
	for _, existing := range session.RelatedFiles {
		if relatedFileIdentity(existing) == relatedFileIdentity(file) {
			return false
		}
	}
	session.RelatedFiles = append(session.RelatedFiles, file)
	return true
}

func (m *Manager) RemoveRelatedFile(ctx context.Context, key, path string) error {
	return m.RemoveRelatedFileAtHead(ctx, key, path, "", "", "")
}

func (m *Manager) RemoveRelatedFileAtHead(_ context.Context, key, path, head, repoPath, repoKind string) error {
	path = strings.TrimSpace(path)
	head = strings.TrimSpace(head)
	repoPath = strings.TrimSpace(repoPath)
	repoKind = strings.TrimSpace(repoKind)
	if path == "" {
		return errors.New("file path required")
	}
	target := relatedFileIdentity(RelatedFile{Path: path, Head: head, RepoPath: repoPath, RepoKind: repoKind})
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return err
	}
	next := make([]RelatedFile, 0, len(session.RelatedFiles))
	removed := false
	for _, item := range session.RelatedFiles {
		if relatedFileIdentity(item) == target {
			removed = true
			continue
		}
		next = append(next, item)
	}
	if !removed {
		return nil
	}
	session.RelatedFiles = next
	return m.upsertSessionMetaUnsafe(session)
}

func (m *Manager) RecordOutputFile(ctx context.Context, key, path string) error {
	return m.RecordOutputFileAtHead(ctx, key, path, "")
}

func (m *Manager) RecordOutputFileAtHead(ctx context.Context, key, path, head string) error {
	return m.RecordRelatedOutputFile(ctx, key, RelatedFile{Path: path, Head: head})
}

func (m *Manager) RecordOutputFileInRepo(ctx context.Context, key, rootID, repoKind, repoPath, repoName, path, head string) error {
	return m.RecordRelatedOutputFile(ctx, key, RelatedFile{
		RootID:   rootID,
		RepoKind: repoKind,
		RepoPath: repoPath,
		RepoName: repoName,
		Path:     path,
		Head:     head,
	})
}

func (m *Manager) RecordRelatedOutputFile(ctx context.Context, key string, file RelatedFile) error {
	if strings.TrimSpace(file.Path) == "" {
		return errors.New("file path required")
	}
	file.Relation = "output"
	file.CreatedBySession = true
	return m.AddRelatedFile(ctx, key, RelatedFile{
		RootID:           strings.TrimSpace(file.RootID),
		RepoPath:         strings.TrimSpace(file.RepoPath),
		RepoName:         strings.TrimSpace(file.RepoName),
		RepoKind:         strings.TrimSpace(file.RepoKind),
		Path:             strings.TrimSpace(file.Path),
		Head:             strings.TrimSpace(file.Head),
		Relation:         "output",
		CreatedBySession: true,
	})
}

func (m *Manager) RecordRelatedWorktree(_ context.Context, key, rootID, path, branch, head string) (bool, error) {
	rootID = strings.TrimSpace(rootID)
	path = strings.TrimSpace(path)
	if rootID == "" {
		return false, errors.New("root id required")
	}
	if path == "" {
		return false, errors.New("worktree path required")
	}
	if !filepath.IsAbs(path) {
		return false, errors.New("worktree path must be absolute")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return false, err
	}
	if session.RelatedWorktree != nil && strings.TrimSpace(session.RelatedWorktree.Path) != "" {
		return false, nil
	}
	session.RelatedWorktree = &RelatedWorktree{
		RootID:    rootID,
		Path:      filepath.Clean(path),
		Branch:    strings.TrimSpace(branch),
		Head:      strings.TrimSpace(head),
		UpdatedAt: m.now().UTC(),
	}
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) UpdateAgentState(_ context.Context, session *Session, agent string, lastCtxSeq int, agentSessionID string) error {
	if session == nil || strings.TrimSpace(session.Key) == "" {
		return errors.New("session required")
	}
	if strings.TrimSpace(agent) == "" {
		return errors.New("agent required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.getSessionUnsafe(session.Key, 0)
	if err != nil {
		return err
	}
	session = current
	if session.AgentCtxSeq == nil {
		session.AgentCtxSeq = map[string]int{}
	}
	if lastCtxSeq >= 0 {
		session.AgentCtxSeq[agent] = lastCtxSeq
	}
	if strings.TrimSpace(agentSessionID) == "" {
		return nil
	}
	if err := m.upsertAgentBindingUnsafe(AgentBinding{
		SessionKey:     strings.TrimSpace(session.Key),
		Agent:          strings.TrimSpace(agent),
		AgentSessionID: strings.TrimSpace(agentSessionID),
		AgentCtxSeq:    lastCtxSeq,
	}); err != nil {
		return err
	}
	return m.upsertExternalSessionNameUnsafe(agent, agentSessionID, session.Name)
}

func (m *Manager) UpsertAgentBinding(_ context.Context, binding AgentBinding) error {
	if strings.TrimSpace(binding.SessionKey) == "" {
		return errors.New("session key required")
	}
	if strings.TrimSpace(binding.Agent) == "" {
		return errors.New("agent required")
	}
	if strings.TrimSpace(binding.AgentSessionID) == "" {
		return errors.New("agent session id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.upsertAgentBindingUnsafe(binding); err != nil {
		return err
	}
	current, err := m.getSessionUnsafe(binding.SessionKey, 0)
	if err != nil {
		return err
	}
	return m.upsertExternalSessionNameUnsafe(binding.Agent, binding.AgentSessionID, current.Name)
}

func (m *Manager) UpdateExternalSessionCursor(_ context.Context, sessionKey, agent string, cursor agenttypes.ExternalSessionCursor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE session_agent_bindings SET external_source_path = ?, external_source_offset = ?, external_source_mtime_ns = ? WHERE session_key = ? AND agent = ?`,
		strings.TrimSpace(cursor.SourcePath), cursor.Offset, cursor.ModTimeUnixNano, strings.TrimSpace(sessionKey), strings.TrimSpace(agent))
	return err
}

func (m *Manager) GetAgentBinding(_ context.Context, sessionKey, agent string) (*AgentBinding, error) {
	if strings.TrimSpace(sessionKey) == "" {
		return nil, errors.New("session key required")
	}
	if strings.TrimSpace(agent) == "" {
		return nil, errors.New("agent required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return nil, err
	}
	row := db.QueryRow(selectAgentBindingSQL, strings.TrimSpace(sessionKey), strings.TrimSpace(agent))
	var binding AgentBinding
	if err := scanAgentBinding(row, &binding); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errSessionNotFound
		}
		return nil, err
	}
	return &binding, nil
}

func (m *Manager) FindAgentBinding(ctx context.Context, sessionKey, agent string) (*AgentBinding, error) {
	binding, err := m.GetAgentBinding(ctx, sessionKey, agent)
	if errors.Is(err, errSessionNotFound) {
		return nil, nil
	}
	return binding, err
}

func (m *Manager) FindAgentBindingByAgentSession(_ context.Context, agent, agentSessionID string) (*AgentBinding, error) {
	if strings.TrimSpace(agent) == "" {
		return nil, errors.New("agent required")
	}
	if strings.TrimSpace(agentSessionID) == "" {
		return nil, errors.New("agent session id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return nil, err
	}
	var binding AgentBinding
	row := db.QueryRow(
		selectBindingByAgentSessionSQL,
		strings.TrimSpace(agent),
		strings.TrimSpace(agentSessionID),
	)
	err = scanAgentBinding(row, &binding)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &binding, nil
}

func (m *Manager) HasAgentBinding(ctx context.Context, agent, agentSessionID string) (bool, error) {
	binding, err := m.FindAgentBindingByAgentSession(ctx, agent, agentSessionID)
	if err != nil {
		return false, err
	}
	return binding != nil, nil
}

func (m *Manager) listAgentBindingsUnsafe(sessionKey string) ([]AgentBinding, error) {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(selectAgentBindingsBySessionSQL, strings.TrimSpace(sessionKey))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bindings := make([]AgentBinding, 0)
	for rows.Next() {
		var binding AgentBinding
		if err := scanAgentBinding(rows, &binding); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bindings, nil
}

func scanAgentBinding(scanner rowScanner, binding *AgentBinding) error {
	return scanner.Scan(&binding.SessionKey, &binding.Agent, &binding.AgentSessionID, &binding.AgentCtxSeq, &binding.ExternalSourcePath, &binding.ExternalSourceOffset, &binding.ExternalSourceMtimeNS)
}

func (m *Manager) upsertAgentBindingUnsafe(binding AgentBinding) error {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	agentCtxSeq := 0
	if binding.AgentCtxSeq > 0 {
		agentCtxSeq = binding.AgentCtxSeq
	}
	_, err = db.Exec(
		upsertAgentBindingSQL,
		strings.TrimSpace(binding.SessionKey),
		strings.TrimSpace(binding.Agent),
		strings.TrimSpace(binding.AgentSessionID),
		agentCtxSeq,
	)
	if err != nil {
		return err
	}
	// Rename may have happened before the first agent binding existed, leaving the alias
	// stored with empty agent identity. Backfill it now so a later delete+reimport via
	// LookupAliasForAgent still finds the manual name.
	if key := strings.TrimSpace(binding.SessionKey); key != "" {
		_, _ = db.Exec(
			`UPDATE session_name_aliases SET agent = ?, agent_session_id = ?, updated_at = ? WHERE key = ?`,
			strings.TrimSpace(binding.Agent),
			strings.TrimSpace(binding.AgentSessionID),
			m.now().UTC().Format(time.RFC3339Nano),
			key,
		)
	}
	return nil
}

func (m *Manager) Close(ctx context.Context, key string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeSessionUnsafe(key)
}

func (m *Manager) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deleteSessionUnsafe(key)
}

func (m *Manager) Rename(_ context.Context, key, name string) (*Session, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, errors.New("session name required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return nil, err
	}
	if session.Name == trimmed {
		// Still persist to alias so a prior alias from an earlier rename survives.
		if err := m.upsertSessionNameAliasUnsafe(key, trimmed); err != nil {
			return nil, err
		}
		for agent, agentSessionID := range m.agentSessionIDsUnsafe(session.Key) {
			if err := m.upsertExternalSessionNameUnsafe(agent, agentSessionID, trimmed); err != nil {
				return nil, err
			}
		}
		return session, nil
	}
	session.Name = trimmed
	session.UpdatedAt = m.now().UTC()
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return nil, err
	}
	if err := m.upsertSessionNameAliasUnsafe(key, trimmed); err != nil {
		return nil, err
	}
	for agent, agentSessionID := range m.agentSessionIDsUnsafe(session.Key) {
		if err := m.upsertExternalSessionNameUnsafe(agent, agentSessionID, trimmed); err != nil {
			return nil, err
		}
	}
	return session, nil
}

func (m *Manager) SetPinned(_ context.Context, key string, pinned bool) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return nil, err
	}
	if pinned {
		if session.PinnedAt != nil {
			return session, nil
		}
		now := m.now().UTC()
		session.PinnedAt = &now
	} else {
		if session.PinnedAt == nil {
			return session, nil
		}
		session.PinnedAt = nil
	}
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return nil, err
	}
	return session, nil
}

func (m *Manager) UpdateModel(_ context.Context, session *Session, model string) error {
	if session == nil || strings.TrimSpace(session.Key) == "" {
		return errors.New("session required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.getSessionUnsafe(session.Key, 0)
	if err != nil {
		return err
	}
	model = strings.TrimSpace(model)
	if current.Model == model {
		return nil
	}
	current.Model = model
	current.UpdatedAt = m.now().UTC()
	return m.upsertSessionMetaUnsafe(current)
}

func (m *Manager) UpdateShell(_ context.Context, session *Session, shell string) error {
	if session == nil || strings.TrimSpace(session.Key) == "" {
		return errors.New("session required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.getSessionUnsafe(session.Key, 0)
	if err != nil {
		return err
	}
	shell = strings.TrimSpace(shell)
	if current.Shell == shell {
		return nil
	}
	current.Shell = shell
	current.UpdatedAt = m.now().UTC()
	session.Shell = shell
	session.UpdatedAt = current.UpdatedAt
	return m.upsertSessionMetaUnsafe(current)
}

func (m *Manager) UpdatePlanMode(_ context.Context, session *Session, enabled bool) error {
	if session == nil || strings.TrimSpace(session.Key) == "" {
		return errors.New("session required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.getSessionUnsafe(session.Key, 0)
	if err != nil {
		return err
	}
	if current.PlanMode == enabled {
		session.PlanMode = enabled
		return nil
	}
	current.PlanMode = enabled
	current.UpdatedAt = m.now().UTC()
	session.PlanMode = enabled
	session.UpdatedAt = current.UpdatedAt
	return m.upsertSessionMetaUnsafe(current)
}

func (m *Manager) UpdateLastContextWindow(_ context.Context, session *Session, contextWindow agenttypes.ContextWindow) error {
	if session == nil || strings.TrimSpace(session.Key) == "" {
		return errors.New("session required")
	}
	if !validContextWindow(contextWindow) {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.getSessionUnsafe(session.Key, 0)
	if err != nil {
		return err
	}
	if current.LastContextWindow == contextWindow {
		session.LastContextWindow = contextWindow
		return nil
	}
	current.LastContextWindow = contextWindow
	session.LastContextWindow = contextWindow
	return m.upsertSessionMetaUnsafe(current)
}

func (m *Manager) closeSessionUnsafe(key string) (*Session, error) {
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return nil, err
	}
	if session.ClosedAt != nil {
		return session, nil
	}
	now := m.now().UTC()
	session.ClosedAt = &now
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return nil, err
	}
	return session, nil
}

func (m *Manager) deleteSessionUnsafe(key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("session key required")
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	result, err := db.Exec(deleteSessionSQL, key)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return errSessionNotFound
	}
	if _, err := db.Exec(deleteBindingsBySessionSQL, key); err != nil {
		return err
	}
	delete(m.sessions, key)
	path, err := m.exchangePath(key)
	if err != nil {
		return err
	}
	delete(m.exchangeCursors, path)
	metaDir, err := m.root.EnsureMetaDir()
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(metaDir, filepath.FromSlash(path))); err != nil && !os.IsNotExist(err) {
		return err
	}
	auxPath, err := m.auxPath(key)
	if err != nil {
		return err
	}
	delete(m.exchangeCursors, auxPath)
	if err := os.Remove(filepath.Join(metaDir, filepath.FromSlash(auxPath))); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *Manager) Shutdown() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.db == nil {
		return nil
	}
	db := m.db
	m.db = nil
	return db.Close()
}

func (m *Manager) MetaDir() string {
	return m.root.MetaDir()
}

func (m *Manager) Root() fs.RootInfo {
	return m.root
}

func (m *Manager) ExchangeLogPath(key string) string {
	path, err := m.exchangePath(key)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(filepath.Join(".mindfs", path))
}

func (m *Manager) ExchangeLogAbsolutePath(key string) string {
	path, err := m.exchangePath(key)
	if err != nil {
		return ""
	}
	metaDir := m.root.MetaDir()
	if metaDir == "" {
		return ""
	}
	return filepath.Join(metaDir, filepath.FromSlash(path))
}

func (m *Manager) createSessionUnsafe(session *Session) error {
	if session == nil {
		return errors.New("session required")
	}
	if _, ok := m.sessions[session.Key]; ok {
		return fmt.Errorf("session already exists: %s", session.Key)
	}
	if _, err := m.getSessionMetaUnsafe(session.Key); err == nil {
		return fmt.Errorf("session already exists: %s", session.Key)
	} else if !errors.Is(err, errSessionNotFound) {
		return err
	}
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return err
	}
	path, err := m.exchangePath(session.Key)
	if err != nil {
		return err
	}
	_, statErr := m.root.ReadMetaFile(path)
	if statErr == nil {
		return nil
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	return m.root.WriteMetaFile(path, []byte{})
}

func (m *Manager) getSessionUnsafe(key string, afterSeq int) (*Session, error) {
	if afterSeq <= 0 {
		if cached, ok := m.sessions[key]; ok && cached != nil {
			return cached, nil
		}
	}
	loaded, err := m.loadSessionUnsafe(key, afterSeq)
	if err != nil {
		return nil, err
	}
	if afterSeq <= 0 {
		m.sessions[key] = loaded
	}
	return loaded, nil
}

// getSessionWindowUnsafe 返回按窗口切片后的 Session 与窗口元信息。
// beforeSeq>0：取 seq<beforeSeq 的尾部 limit 条（二分定位 idx=首个 seq>=beforeSeq，窗口 all[max(0,idx-limit):idx]，hasMore=idx-limit>0）；
// latest>0：取尾部 limit 条；两者皆 0：默认尾部 limit 条（等价于 latest=limit）。
// 复用 readExchangesFull 全量读后在内存倒序切片；不写入 m.sessions 全量缓存，避免污染 afterSeq<=0 缓存分支。
func (m *Manager) getSessionWindowUnsafe(key string, beforeSeq, limit, latest int) (*Session, *SessionWindowMeta, error) {
	if strings.TrimSpace(key) == "" {
		return nil, nil, errors.New("session key required")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	meta, err := m.getSessionMetaWithBindingsUnsafe(key)
	if err != nil {
		return nil, nil, err
	}
	path, err := m.exchangePath(key)
	if err != nil {
		return nil, nil, err
	}

	all := []Exchange{}
	if cached, ok := m.sessions[key]; ok && cached != nil && len(cached.Exchanges) > 0 {
		all = cached.Exchanges
	} else {
		read, _, rErr := m.readExchangesFull(path)
		if rErr != nil {
			return nil, nil, rErr
		}
		all = read
	}

	total := len(all)
	var window []Exchange
	hasMore := false
	switch {
	case latest > 0:
		start := total - latest
		if start < 0 {
			start = 0
		}
		window = all[start:]
		hasMore = start > 0
	case beforeSeq > 0:
		idx := firstIndexWhereSeq(all, beforeSeq)
		start := idx - limit
		if start < 0 {
			start = 0
		}
		window = all[start:idx]
		hasMore = start > 0
	default:
		start := total - limit
		if start < 0 {
			start = 0
		}
		window = all[start:]
		hasMore = start > 0
	}

	meta.Exchanges = window

	minSeq, maxSeq := 0, 0
	if len(window) > 0 {
		minSeq = window[0].Seq
		maxSeq = window[len(window)-1].Seq
	}
	return meta, &SessionWindowMeta{
		Total:   total,
		HasMore: hasMore,
		MinSeq:  minSeq,
		MaxSeq:  maxSeq,
	}, nil
}

// firstIndexWhereSeq 在按 Seq 升序的 exchanges 中二分定位首个 Seq>=target 的下标（lower_bound）。
func firstIndexWhereSeq(all []Exchange, target int) int {
	lo, hi := 0, len(all)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if all[mid].Seq >= target {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

func (m *Manager) getSessionMetaUnsafe(key string) (*Session, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("session key required")
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return nil, err
	}
	row := db.QueryRow(selectSessionSQL+`
WHERE key = ?`, key)
	session, err := scanSessionMetaRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (m *Manager) listSessionMetasUnsafe() ([]*Session, error) {
	return m.querySessionMetasUnsafe(selectSessionSQL+`
ORDER BY updated_at DESC`, nil)
}

// querySessionMetasUnsafe 一次拉取 session meta（不含 exchanges 文件），并单次 JOIN 填充 agent bindings。
// 列表/搜索只需 meta；此前逐 key 调 getSessionUnsafe 会导致每个会话全量读 JSONL。
func (m *Manager) querySessionMetasUnsafe(query string, args []any) ([]*Session, error) {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	items := make([]*Session, 0)
	for rows.Next() {
		item, err := scanSessionMetaRow(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(items) > 0 {
		if err := m.fillSessionBindingsUnsafe(items); err != nil {
			return nil, err
		}
		if err := m.applySessionNameAliasesUnsafe(items); err != nil {
			return nil, err
		}
	}
	return items, nil
}

// fillSessionBindingsUnsafe 用单个 IN 查询拉取一批 session 的 bindings，替代逐 session N+1 查询。
func (m *Manager) fillSessionBindingsUnsafe(items []*Session) error {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	keys := make([]any, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	rows, err := db.Query(selectAllAgentBindingsSQL+` WHERE session_key IN (`+placeholders+`)`, keys...)
	if err != nil {
		return err
	}
	defer rows.Close()
	byKey := make(map[string][]AgentBinding, len(items))
	for rows.Next() {
		var binding AgentBinding
		if err := scanAgentBinding(rows, &binding); err != nil {
			return err
		}
		byKey[binding.SessionKey] = append(byKey[binding.SessionKey], binding)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range items {
		for _, binding := range byKey[item.Key] {
			if strings.TrimSpace(binding.Agent) == "" {
				continue
			}
			item.AgentCtxSeq[binding.Agent] = binding.AgentCtxSeq
		}
	}
	return nil
}

func (m *Manager) listSessionsUnsafe(opts ListOptions) ([]*Session, error) {
	query := selectSessionSQL
	where, args := sessionListWhere(opts)
	if len(where) > 0 {
		query += `
WHERE ` + strings.Join(where, " AND ")
	}
	query += `
ORDER BY updated_at DESC`
	if opts.Limit > 0 {
		query += `
LIMIT ?`
		args = append(args, opts.Limit)
	}
	return m.querySessionMetasUnsafe(query, args)
}

func (m *Manager) listPinnedSessionsUnsafe(opts ListOptions) ([]*Session, error) {
	query := selectSessionSQL
	where, args := sessionListWhere(ListOptions{
		ParentSessionKey: opts.ParentSessionKey,
		TopLevelOnly:     opts.TopLevelOnly,
	})
	where = append(where, "pinned_at IS NOT NULL AND pinned_at != ''")
	if len(where) > 0 {
		query += `
WHERE ` + strings.Join(where, " AND ")
	}
	query += `
ORDER BY pinned_at DESC, updated_at DESC`
	return m.querySessionMetasUnsafe(query, args)
}

func (m *Manager) countSessionsUnsafe(opts ListOptions) (int, error) {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return 0, err
	}
	query := `
SELECT COUNT(*) FROM sessions`
	where, args := sessionListWhere(opts)
	if len(where) > 0 {
		query += `
WHERE ` + strings.Join(where, " AND ")
	}
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func sessionListWhere(opts ListOptions) ([]string, []any) {
	args := make([]any, 0, 2)
	where := make([]string, 0, 2)
	if parentKey := strings.TrimSpace(opts.ParentSessionKey); parentKey != "" {
		where = append(where, "parent_session_key = ?")
		args = append(args, parentKey)
	} else if opts.TopLevelOnly {
		where = append(where, "parent_session_key = ''")
	}
	if !opts.BeforeTime.IsZero() {
		where = append(where, "updated_at < ?")
		args = append(args, opts.BeforeTime.UTC().Format(time.RFC3339Nano))
	} else if !opts.AfterTime.IsZero() {
		where = append(where, "updated_at > ?")
		args = append(args, opts.AfterTime.UTC().Format(time.RFC3339Nano))
	}
	return where, args
}

func (m *Manager) loadSessionUnsafe(key string, afterSeq int) (*Session, error) {
	meta, err := m.getSessionMetaWithBindingsUnsafe(key)
	if err != nil {
		return nil, err
	}
	exchanges, _, err := m.loadExchanges(key, afterSeq)
	if err != nil {
		return nil, err
	}
	meta.Exchanges = exchanges
	return meta, nil
}

// getSessionMetaWithBindingsUnsafe 只加载 SQLite meta + bindings，不读 exchanges 文件。
func (m *Manager) getSessionMetaWithBindingsUnsafe(key string) (*Session, error) {
	meta, err := m.getSessionMetaUnsafe(key)
	if err != nil {
		return nil, err
	}
	bindings, err := m.listAgentBindingsUnsafe(key)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		if strings.TrimSpace(binding.Agent) == "" {
			continue
		}
		meta.AgentCtxSeq[binding.Agent] = binding.AgentCtxSeq
	}
	_ = m.applySessionNameAliasSingleUnsafe(meta)
	return meta, nil
}

func (m *Manager) upsertSessionMetaUnsafe(session *Session) error {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	if session == nil {
		return errors.New("session required")
	}
	normalizeSessionMeta(session)
	args, err := sessionMetaUpsertArgs(session)
	if err != nil {
		return err
	}
	_, err = db.Exec(upsertSessionMetaSQL, args...)
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) upsertSessionNameAliasUnsafe(key, name string) error {
	key = strings.TrimSpace(key)
	name = strings.TrimSpace(name)
	if key == "" || name == "" {
		return nil
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	// Persist alias keyed by internal key, plus best-known agent external identity
	// so a later re-import (new key, same agent_session_id) still resumes the alias.
	agent := ""
	agentSessionID := ""
	if rows, qErr := db.Query(`SELECT agent, agent_session_id FROM session_agent_bindings WHERE session_key = ? LIMIT 1`, key); qErr == nil {
		if rows.Next() {
			_ = rows.Scan(&agent, &agentSessionID)
		}
		_ = rows.Close()
	}
	_, err = db.Exec(upsertSessionNameAliasSQL, key, name, strings.TrimSpace(agent), strings.TrimSpace(agentSessionID), m.now().UTC().Format(time.RFC3339Nano))
	return err
}

func (m *Manager) upsertExternalSessionNameUnsafe(agent, agentSessionID, name string) error {
	agent = strings.TrimSpace(agent)
	agentSessionID = strings.TrimSpace(agentSessionID)
	name = strings.TrimSpace(name)
	if agent == "" || agentSessionID == "" || name == "" || name == "New Session" {
		return nil
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	_, err = db.Exec(upsertExternalSessionNameSQL, agent, agentSessionID, name, m.now().UTC().Format(time.RFC3339Nano))
	return err
}

func (m *Manager) agentSessionIDsUnsafe(key string) map[string]string {
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return nil
	}
	rows, err := db.Query(`SELECT agent, agent_session_id FROM session_agent_bindings WHERE session_key = ?`, strings.TrimSpace(key))
	if err != nil {
		return nil
	}
	defer rows.Close()
	ids := make(map[string]string)
	for rows.Next() {
		var agent, agentSessionID string
		if err := rows.Scan(&agent, &agentSessionID); err != nil {
			return nil
		}
		ids[strings.TrimSpace(agent)] = strings.TrimSpace(agentSessionID)
	}
	if err := rows.Err(); err != nil {
		return nil
	}
	return ids
}

func (m *Manager) lookupSessionAliasForAgentUnsafe(agent, agentSessionID string) (string, bool) {
	agent = strings.TrimSpace(agent)
	agentSessionID = strings.TrimSpace(agentSessionID)
	if agent == "" || agentSessionID == "" {
		return "", false
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return "", false
	}
	var name string
	if err := db.QueryRow(selectExternalSessionNameSQL, agent, agentSessionID).Scan(&name); err != nil {
		return "", false
	}
	if strings.TrimSpace(name) == "" {
		return "", false
	}
	return strings.TrimSpace(name), true
}

func (m *Manager) LookupAliasForAgent(agent, agentSessionID string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lookupSessionAliasForAgentUnsafe(agent, agentSessionID)
}

func (m *Manager) applySessionNameAliasesUnsafe(items []*Session) error {
	if len(items) == 0 {
		return nil
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	keys := make([]any, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.Key)
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	rows, err := db.Query(`SELECT key, name FROM session_name_aliases WHERE key IN (`+ph+`)`, keys...)
	if err != nil {
		return err
	}
	defer rows.Close()
	byKey := make(map[string]string, len(items))
	for rows.Next() {
		var k, n string
		if err := rows.Scan(&k, &n); err != nil {
			return err
		}
		if v := strings.TrimSpace(n); v != "" {
			byKey[strings.TrimSpace(k)] = v
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, it := range items {
		if v := byKey[strings.TrimSpace(it.Key)]; v != "" {
			it.Name = v
		}
	}
	// Also overlay aliases that were stored by agent_session_id for re-imported sessions
	// whose internal key changed, but never overwrite an existing session name.
	for _, it := range items {
		if byKey[strings.TrimSpace(it.Key)] != "" || strings.TrimSpace(it.Name) != "" {
			continue
		}
		// Prefer bindings already loaded by fillSessionBindingsUnsafe; fallback to DB lookup
		agent := InferAgentFromSession(it)
		var agentSessionID string
		if rows2, qErr := db.Query(`SELECT agent_session_id FROM session_agent_bindings WHERE session_key = ? LIMIT 1`, it.Key); qErr == nil {
			if rows2.Next() {
				_ = rows2.Scan(&agentSessionID)
			}
			_ = rows2.Close()
		}
		if v, ok := m.lookupSessionAliasForAgentUnsafe(agent, agentSessionID); ok {
			it.Name = v
		}
	}
	return nil
}

func (m *Manager) applySessionNameAliasSingleUnsafe(s *Session) error {
	if s == nil || strings.TrimSpace(s.Key) == "" {
		return nil
	}
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	var alias string
	err = db.QueryRow(selectSessionNameAliasSQL, strings.TrimSpace(s.Key)).Scan(&alias)
	if err == nil {
		if v := strings.TrimSpace(alias); v != "" {
			s.Name = v
			return nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Fallback: alias was stored under external identity (re-import after delete retains manual name)
	agent := InferAgentFromSession(s)
	var agentSessionID string
	if rows, qErr := db.Query(`SELECT agent_session_id FROM session_agent_bindings WHERE session_key = ? LIMIT 1`, s.Key); qErr == nil {
		if rows.Next() {
			_ = rows.Scan(&agentSessionID)
		}
		_ = rows.Close()
	}
	if v, ok := m.lookupSessionAliasForAgentUnsafe(agent, agentSessionID); ok {
		s.Name = v
	}
	return nil
}

func (m *Manager) loadExchanges(key string, afterSeq int) ([]Exchange, int, error) {
	path, err := m.exchangePath(key)
	if err != nil {
		return nil, 0, err
	}

	// 增量快路径：文件与游标一致 → 直接过滤内存缓存；追加 → 尾部增量读，避免全量读盘+反序列化。
	if cached, ok := m.sessions[key]; ok && cached != nil && len(cached.Exchanges) > 0 {
		if cursor, has := m.exchangeCursors[path]; has && cursor.Offset > 0 {
			if info, statErr := m.root.StatMetaFile(path); statErr == nil {
				size := info.Size()
				mtimeNs := info.ModTime().UnixNano()
				switch {
				case size == cursor.Size && mtimeNs == cursor.ModTimeNs:
					return filterExchanges(cached.Exchanges, afterSeq), cursor.MaxSeq, nil
				case size > cursor.Offset && mtimeNs != cursor.ModTimeNs:
					// 写路径（AddExchangeForAgent 等）会同步更新缓存但 cursor 不前进，
					// 以缓存 max seq 为基准去重，避免重复追加已读条目。
					baseSeq := cursor.MaxSeq
					if cachedMax := maxExchangeSeq(cached.Exchanges); cachedMax > baseSeq {
						baseSeq = cachedMax
					}
					added, maxSeq, ok := m.readExchangeTail(path, cursor.Offset, baseSeq)
					if ok {
						if len(added) > 0 {
							cached.Exchanges = append(cached.Exchanges, added...)
						}
						m.exchangeCursors[path] = exchangeFileCursor{Offset: size, Size: size, ModTimeNs: mtimeNs, MaxSeq: maxSeq}
						return filterExchanges(cached.Exchanges, afterSeq), maxSeq, nil
					}
				}
			}
		}
	}

	// 全量读（兜底：首读/文件被重写/尾部解析失败）
	exchanges, total, err := m.readExchangesFull(path)
	if err != nil {
		return nil, 0, err
	}
	if info, statErr := m.root.StatMetaFile(path); statErr == nil {
		m.exchangeCursors[path] = exchangeFileCursor{
			Offset:    info.Size(),
			Size:      info.Size(),
			ModTimeNs: info.ModTime().UnixNano(),
			MaxSeq:    total,
		}
	}
	if cached, ok := m.sessions[key]; ok && cached != nil {
		cached.Exchanges = exchanges
	}
	return filterExchanges(exchanges, afterSeq), total, nil
}

func (m *Manager) readExchangesFull(path string) ([]Exchange, int, error) {
	payload, err := m.root.ReadMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Exchange{}, 0, nil
		}
		return nil, 0, err
	}
	exchanges := make([]Exchange, 0)
	total := 0
	scanner := jsonlScanner(payload)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry Exchange
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Seq <= 0 {
			entry.Seq = total + 1
		}
		if entry.Seq > total {
			total = entry.Seq
		}
		exchanges = append(exchanges, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	return exchanges, total, nil
}

// readExchangeTail 从 offset 续读追加的 JSONL 行。跳过 seq <= baseSeq 的条目（缓存已包含），
// 返回真正新增的 exchanges 与新的 maxSeq。解析失败/读取错误返回 ok=false，由调用方回退全量读。
func (m *Manager) readExchangeTail(path string, offset int64, baseSeq int) ([]Exchange, int, bool) {
	file, err := m.root.OpenMetaFile(path)
	if err != nil {
		return nil, baseSeq, false
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, baseSeq, false
	}
	total := baseSeq
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxExchangeLineBytes)
	added := make([]Exchange, 0, 8)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry Exchange
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Seq <= 0 {
			entry.Seq = total + 1
		}
		if entry.Seq > total {
			total = entry.Seq
		}
		if entry.Seq <= baseSeq {
			continue
		}
		added = append(added, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, baseSeq, false
	}
	return added, total, true
}

func maxExchangeSeq(exchanges []Exchange) int {
	max := 0
	for _, e := range exchanges {
		if e.Seq > max {
			max = e.Seq
		}
	}
	return max
}

func filterExchanges(all []Exchange, afterSeq int) []Exchange {
	if afterSeq <= 0 {
		out := make([]Exchange, len(all))
		copy(out, all)
		return out
	}
	out := make([]Exchange, 0, len(all))
	for _, e := range all {
		if e.Seq > afterSeq {
			out = append(out, e)
		}
	}
	return out
}

func (m *Manager) appendExchange(key string, exchange Exchange) error {
	path, err := m.exchangePath(key)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(exchange)
	if err != nil {
		return err
	}
	file, err := m.root.OpenMetaFileAppend(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return err
	}
	return nil
}

func (m *Manager) loadExchangeAux(key string, afterSeq int) (map[int][]ExchangeAux, error) {
	entries, err := m.loadExchangeAuxEntries(key, afterSeq)
	if err != nil {
		return nil, err
	}
	items := make(map[int][]ExchangeAux)
	for _, entry := range entries {
		compacted, ok := CompactExchangeAux(entry)
		if !ok {
			continue
		}
		items[compacted.Seq] = append(items[compacted.Seq], compacted)
	}
	return items, nil
}

// loadExchangeAuxWindow 按窗口 seqSet 过滤 aux：仅保留 seq∈seqSet 的条目，避免 aux.line 错位。
func (m *Manager) loadExchangeAuxWindow(key string, seqSet map[int]bool) (map[int][]ExchangeAux, error) {
	if len(seqSet) == 0 {
		return map[int][]ExchangeAux{}, nil
	}
	// 窗口只覆盖尾部若干 seq，而 aux 按写入顺序（seq 升序）追加，故只需从文件尾反向读。
	// 全量读在实测会话上是 11.8MB/281ms 且持 m.mu（阻塞实时流式写入），而窗口真正要的
	// 只有 ~2MB 中的一小段，属纯浪费。
	minSeq := 0
	for seq := range seqSet {
		if minSeq == 0 || seq < minSeq {
			minSeq = seq
		}
	}
	path, err := m.auxPath(key)
	if err != nil {
		return nil, err
	}
	entries, ok := m.readAuxWindowTail(path, minSeq)
	if !ok {
		// 尾部扫描不可用（文件不可 seek / 读取异常）→ 回退全量读，保证正确性。
		entries, err = m.loadExchangeAuxEntries(key, 0)
		if err != nil {
			return nil, err
		}
	}
	items := make(map[int][]ExchangeAux)
	for _, entry := range entries {
		compacted, ok := CompactExchangeAux(entry)
		if !ok {
			continue
		}
		if !seqSet[compacted.Seq] {
			continue
		}
		items[compacted.Seq] = append(items[compacted.Seq], compacted)
	}
	return items, nil
}

func (m *Manager) loadExchangeAuxEntries(key string, afterSeq int) ([]ExchangeAux, error) {
	path, err := m.auxPath(key)
	if err != nil {
		return nil, err
	}

	// 增量快路径：仅 afterSeq>0 适用。afterSeq=0 需要全部 aux（如 GetFullToolCall 查找 callID），
	// aux 无内存缓存，必须全量读。增量场景文件未变 → 无新 aux；追加 → 尾部增量读。
	if afterSeq > 0 {
		if cursor, has := m.exchangeCursors[path]; has && cursor.Offset > 0 {
			if info, statErr := m.root.StatMetaFile(path); statErr == nil {
				size := info.Size()
				mtimeNs := info.ModTime().UnixNano()
				switch {
				case size == cursor.Size && mtimeNs == cursor.ModTimeNs:
					return []ExchangeAux{}, nil
				case size > cursor.Offset && mtimeNs != cursor.ModTimeNs:
					if items, ok := m.readAuxTail(path, cursor.Offset, afterSeq); ok {
						m.exchangeCursors[path] = exchangeFileCursor{Offset: size, Size: size, ModTimeNs: mtimeNs, MaxSeq: cursor.MaxSeq}
						return items, nil
					}
				}
			}
		}
	}

	// 全量读（兜底 / afterSeq=0）
	items, err := m.readAuxFile(path, afterSeq)
	if err != nil {
		return nil, err
	}
	if info, statErr := m.root.StatMetaFile(path); statErr == nil {
		m.exchangeCursors[path] = exchangeFileCursor{
			Offset:    info.Size(),
			Size:      info.Size(),
			ModTimeNs: info.ModTime().UnixNano(),
			MaxSeq:    0,
		}
	}
	return items, nil
}

// readAuxWindowTail 从 aux 文件尾部反向分块读取 seq>=minSeq 的条目。
// aux 按写入顺序（seq 升序）追加，因此尾部即最新 seq；窗口只覆盖尾部若干 seq，
// 无需全量读（实测某会话 aux 11.8MB/281ms 且持 m.mu，而窗口要的只是一小段）。
// 返回 ok=false 表示无法可靠完成（不可 seek / 读取异常），由调用方回退全量读。
func (m *Manager) readAuxWindowTail(path string, minSeq int) ([]ExchangeAux, bool) {
	if minSeq <= 0 {
		return nil, false
	}
	file, err := m.root.OpenMetaFile(path)
	if err != nil {
		// 文件缺失等价于「无 aux」，不是失败。
		if errors.Is(err, os.ErrNotExist) {
			return []ExchangeAux{}, true
		}
		return nil, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false
	}
	size := info.Size()
	if size == 0 {
		return []ExchangeAux{}, true
	}

	const chunk = int64(512 << 10)
	// 即使已看到 seq<minSeq 也再多读两块：aux 的写入顺序不保证跨轮严格单调
	// （子代理等可能在稍后追加较小 seq），留出余量避免漏条目。
	const safetyChunks = 2
	var rawLines [][]byte
	end := size
	safety := 0
	for end > 0 {
		start := end - chunk
		if start < 0 {
			start = 0
		}
		buf := make([]byte, int(end-start))
		if _, err := file.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return nil, false
		}
		if start > 0 {
			// 非文件头：首行可能是半行，丢弃到第一个换行为止。
			idx := bytes.IndexByte(buf, '\n')
			if idx < 0 {
				buf = nil
			} else {
				buf = buf[idx+1:]
			}
		}
		below := false
		part := make([][]byte, 0, 64)
		for _, raw := range bytes.Split(buf, []byte{'\n'}) {
			raw = bytes.TrimSpace(raw)
			if len(raw) == 0 {
				continue
			}
			part = append(part, raw)
			var probe struct {
				Seq int `json:"seq"`
			}
			if json.Unmarshal(raw, &probe) == nil && probe.Seq > 0 && probe.Seq < minSeq {
				below = true
			}
		}
		rawLines = append(part, rawLines...)
		if below {
			safety++
			if safety >= safetyChunks {
				break
			}
		}
		if start == 0 {
			break
		}
		end = start
	}

	items := make([]ExchangeAux, 0, len(rawLines))
	for _, raw := range rawLines {
		var entry ExchangeAux
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		if entry.Seq <= 0 || entry.Seq < minSeq {
			continue
		}
		items = append(items, entry)
	}
	return items, true
}

func (m *Manager) readAuxFile(path string, afterSeq int) ([]ExchangeAux, error) {
	payload, err := m.root.ReadMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []ExchangeAux{}, nil
		}
		return nil, err
	}
	items := make([]ExchangeAux, 0)
	scanner := jsonlScanner(payload)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry ExchangeAux
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Seq <= 0 {
			continue
		}
		if afterSeq > 0 && entry.Seq <= afterSeq {
			continue
		}
		items = append(items, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (m *Manager) readAuxTail(path string, offset int64, afterSeq int) ([]ExchangeAux, bool) {
	file, err := m.root.OpenMetaFile(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, false
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxExchangeLineBytes)
	items := make([]ExchangeAux, 0, 8)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry ExchangeAux
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Seq <= 0 {
			continue
		}
		if afterSeq > 0 && entry.Seq <= afterSeq {
			continue
		}
		items = append(items, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, false
	}
	return items, true
}

func mergeToolCall(base, next agenttypes.ToolCall) agenttypes.ToolCall {
	merged := base
	if strings.TrimSpace(next.CallID) != "" {
		merged.CallID = next.CallID
	}
	if strings.TrimSpace(next.Title) != "" {
		merged.Title = next.Title
	}
	if strings.TrimSpace(next.Status) != "" {
		merged.Status = next.Status
	}
	if next.Kind != "" {
		merged.Kind = next.Kind
	}
	if len(next.Content) > 0 {
		merged.Content = append([]agenttypes.ToolCallContentItem(nil), next.Content...)
	}
	if len(next.Locations) > 0 {
		merged.Locations = append([]agenttypes.ToolCallLocation(nil), next.Locations...)
	}
	if strings.TrimSpace(next.RawType) != "" {
		merged.RawType = next.RawType
	}
	if len(base.Meta) > 0 || len(next.Meta) > 0 {
		merged.Meta = make(map[string]any, len(base.Meta)+len(next.Meta))
		for key, value := range base.Meta {
			merged.Meta[key] = value
		}
		for key, value := range next.Meta {
			merged.Meta[key] = value
		}
	}
	return merged
}

func (m *Manager) pendingFullToolCallUnsafe(sessionKey, callID string) (*agenttypes.ToolCall, bool) {
	if m.pendingToolCalls == nil {
		return nil, false
	}
	toolCall, ok := m.pendingToolCalls[sessionKey][callID]
	if !ok {
		return nil, false
	}
	out := cloneToolCall(toolCall)
	return &out, true
}

func cloneToolCall(toolCall agenttypes.ToolCall) agenttypes.ToolCall {
	out := toolCall
	out.Content = append([]agenttypes.ToolCallContentItem(nil), toolCall.Content...)
	out.Locations = append([]agenttypes.ToolCallLocation(nil), toolCall.Locations...)
	if len(toolCall.Meta) > 0 {
		out.Meta = make(map[string]any, len(toolCall.Meta))
		for key, value := range toolCall.Meta {
			out.Meta[key] = value
		}
	}
	return out
}

func jsonlScanner(payload []byte) *bufio.Scanner {
	scanner := bufio.NewScanner(strings.NewReader(string(payload)))
	maxTokenSize := len(payload) + 1
	if maxTokenSize < 64*1024 {
		maxTokenSize = 64 * 1024
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxTokenSize)
	return scanner
}

func (m *Manager) appendExchangeAux(key string, aux ExchangeAux) error {
	path, err := m.auxPath(key)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(aux)
	if err != nil {
		return err
	}
	file, err := m.root.OpenMetaFileAppend(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return err
	}
	return nil
}

func (m *Manager) exchangePath(key string) (string, error) {
	if strings.TrimSpace(m.root.MetaDir()) == "" {
		return "", errors.New("managed dir required")
	}
	if key == "" {
		return "", errors.New("session key required")
	}
	if strings.Contains(key, "..") || strings.ContainsRune(key, filepath.Separator) || strings.Contains(key, "/") {
		return "", fmt.Errorf("invalid session key: %s", key)
	}
	return filepath.ToSlash(fmt.Sprintf(exchangeFileTpl, key)), nil
}

func (m *Manager) auxPath(key string) (string, error) {
	if strings.TrimSpace(m.root.MetaDir()) == "" {
		return "", errors.New("managed dir required")
	}
	if key == "" {
		return "", errors.New("session key required")
	}
	if strings.Contains(key, "..") || strings.ContainsRune(key, filepath.Separator) || strings.Contains(key, "/") {
		return "", fmt.Errorf("invalid session key: %s", key)
	}
	return filepath.ToSlash(fmt.Sprintf(auxFileTpl, key)), nil
}

func (m *Manager) ensureSessionMetaDBUnsafe() (*sql.DB, error) {
	if m.db != nil {
		return m.db, nil
	}
	metaDir, err := m.root.EnsureMetaDir()
	if err != nil {
		return nil, err
	}
	legacyDBFile := filepath.Join(metaDir, filepath.FromSlash(sessionDBPath))
	linkFile := legacyDBFile + sessionDBLinkExt
	if linkedDBFile, ok, err := readSessionDBLink(linkFile); err != nil {
		return nil, err
	} else if ok {
		db, err := openSessionMetaDB(linkedDBFile)
		if err != nil {
			return nil, err
		}
		m.db = db
		return m.db, nil
	}

	db, err := openSessionMetaDB(legacyDBFile)
	if err == nil {
		m.db = db
		return m.db, nil
	}
	legacyErr := err

	fallbackDBFile, err := userDataSessionDBFile(m.root.ID)
	if err != nil {
		return nil, fmt.Errorf("open legacy session db: %w; resolve fallback session db: %w", legacyErr, err)
	}
	db, err = openSessionMetaDB(fallbackDBFile)
	if err != nil {
		return nil, fmt.Errorf("open legacy session db: %w; open fallback session db: %w", legacyErr, err)
	}
	if err := writeSessionDBLink(linkFile, fallbackDBFile); err != nil {
		db.Close()
		return nil, fmt.Errorf("open legacy session db: %w; write session db link: %w", legacyErr, err)
	}
	log.Printf("[session/store] sqlite fallback root=%s legacy=%s fallback=%s err=%v", m.root.ID, legacyDBFile, fallbackDBFile, legacyErr)
	m.db = db
	return m.db, nil
}

func openSessionMetaDB(dbFile string) (db *sql.DB, err error) {
	defer func() {
		if r := recover(); r != nil {
			if db != nil {
				db.Close()
			}
			err = fmt.Errorf("sqlite init panic for %s: %v", dbFile, r)
		}
	}()
	if err := os.MkdirAll(filepath.Dir(dbFile), 0o755); err != nil {
		return nil, err
	}
	db, err = openSQLiteDB(dbFile)
	if err != nil {
		return nil, err
	}
	// WAL：读写不互相阻塞，避免高频读（列表/搜索/Get）阻塞低频写；busy_timeout 处理写锁竞争。
	// 多读连接：列表/搜索等读操作可并发，写仍由 SQLite 锁 + busy_timeout 保证串行。
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(sessionTableSchema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(agentBindingTableSchema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(sessionNameAliasTableSchema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(sessionExternalNameTableSchema); err != nil {
		db.Close()
		return nil, err
	}
	// 列表/搜索按 updated_at DESC 排序，无索引时全表排序。
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at DESC)`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_pinned_at ON sessions(pinned_at, updated_at DESC)`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN model TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN shell TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN plan_mode INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		db.Close()
		return nil, err
	}
	for _, stmt := range []string{
		`ALTER TABLE sessions ADD COLUMN parent_session_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN parent_tool_call_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN source TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN task_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN related_worktree_json TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN last_context_window_total_tokens INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN last_context_window_model_context_window INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN pinned_at TEXT`,
		`ALTER TABLE session_agent_bindings ADD COLUMN external_source_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE session_agent_bindings ADD COLUMN external_source_offset INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE session_agent_bindings ADD COLUMN external_source_mtime_ns INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE session_name_aliases ADD COLUMN agent TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE session_name_aliases ADD COLUMN agent_session_id TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			db.Close()
			return nil, err
		}
	}
	if normalized, err := normalizeForkParentLinks(db); err != nil {
		db.Close()
		return nil, err
	} else if normalized > 0 {
		log.Printf("[session/store] normalized fork parent links db=%s count=%d", dbFile, normalized)
	}
	if err := normalizeExternalSessionNames(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func normalizeExternalSessionNames(db *sql.DB) error {
	_, err := db.Exec(`
		INSERT INTO session_external_names (agent, agent_session_id, name, updated_at)
		SELECT b.agent, b.agent_session_id,
			COALESCE(NULLIF(TRIM(a.name), ''), s.name), s.updated_at
		FROM session_agent_bindings b
		JOIN sessions s ON s.key = b.session_key
		LEFT JOIN session_name_aliases a ON a.key = s.key
		WHERE TRIM(b.agent) != ''
			AND TRIM(b.agent_session_id) != ''
			AND TRIM(COALESCE(NULLIF(TRIM(a.name), ''), s.name)) != ''
			AND TRIM(COALESCE(NULLIF(TRIM(a.name), ''), s.name)) != 'New Session'
		ORDER BY s.updated_at, s.key
		ON CONFLICT(agent, agent_session_id) DO UPDATE SET
			name = excluded.name,
			updated_at = excluded.updated_at
		WHERE excluded.updated_at >= session_external_names.updated_at`)
	return err
}

func normalizeForkParentLinks(db *sql.DB) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	rows, err := tx.Query(`
		SELECT key, source
		FROM sessions
		WHERE parent_session_key != '' OR parent_tool_call_id != ''`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	keys := make([]string, 0)
	for rows.Next() {
		var key, source string
		if err := rows.Scan(&key, &source); err != nil {
			return 0, err
		}
		var metadata struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(source), &metadata) == nil && metadata.Type == "fork" {
			keys = append(keys, key)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, key := range keys {
		if _, err := tx.Exec(`
			UPDATE sessions
			SET parent_session_key = '', parent_tool_call_id = ''
			WHERE key = ?`, key); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	tx = nil
	return len(keys), nil
}

func readSessionDBLink(linkFile string) (string, bool, error) {
	payload, err := os.ReadFile(linkFile)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	dbFile := strings.TrimSpace(string(payload))
	if dbFile == "" {
		return "", false, fmt.Errorf("empty session db link: %s", linkFile)
	}
	if !filepath.IsAbs(dbFile) {
		return "", false, fmt.Errorf("session db link must be absolute: %s", linkFile)
	}
	return dbFile, true, nil
}

func writeSessionDBLink(linkFile, dbFile string) error {
	if strings.TrimSpace(dbFile) == "" {
		return errors.New("session db link target required")
	}
	if !filepath.IsAbs(dbFile) {
		return errors.New("session db link target must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(linkFile), 0o755); err != nil {
		return err
	}
	return os.WriteFile(linkFile, []byte(dbFile+"\n"), 0o644)
}

func userDataSessionDBFile(rootID string) (string, error) {
	configDir, err := mindFSConfigDir()
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(rootID)
	if id == "" {
		id = "default"
	}
	return filepath.Join(configDir, url.PathEscape(id), "session-list.db"), nil
}

func sessionMetaUpsertArgs(session *Session) ([]any, error) {
	if session == nil {
		return nil, errors.New("session required")
	}
	relatedFilesJSON, err := json.Marshal(session.RelatedFiles)
	if err != nil {
		return nil, err
	}
	relatedWorktreeJSON := ""
	if session.RelatedWorktree != nil && strings.TrimSpace(session.RelatedWorktree.Path) != "" {
		payload, err := json.Marshal(session.RelatedWorktree)
		if err != nil {
			return nil, err
		}
		relatedWorktreeJSON = string(payload)
	}
	var closedAt any
	if session.ClosedAt != nil {
		closedAt = session.ClosedAt.UTC().Format(time.RFC3339Nano)
	}
	var pinnedAt any
	if session.PinnedAt != nil {
		pinnedAt = session.PinnedAt.UTC().Format(time.RFC3339Nano)
	}
	return []any{
		session.Key,
		session.Type,
		session.ParentSessionKey,
		session.ParentToolCallID,
		session.Source,
		session.TaskID,
		session.Model,
		session.Shell,
		boolToSQLiteInt(session.PlanMode),
		session.Name,
		string(relatedFilesJSON),
		relatedWorktreeJSON,
		session.LastContextWindow.TotalTokens,
		session.LastContextWindow.ModelContextWindow,
		pinnedAt,
		session.CreatedAt.UTC().Format(time.RFC3339Nano),
		session.UpdatedAt.UTC().Format(time.RFC3339Nano),
		closedAt,
	}, nil
}

func validContextWindow(contextWindow agenttypes.ContextWindow) bool {
	return contextWindow.TotalTokens > 0 && contextWindow.ModelContextWindow > 0
}

func boolToSQLiteInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSessionMetaRow(scanner rowScanner) (*Session, error) {
	var (
		key                 string
		typ                 string
		parentSessionKey    string
		parentToolCallID    string
		source              string
		taskID              string
		model               string
		shell               string
		planMode            int
		name                string
		relatedFilesJSON    string
		relatedWorktreeJSON string
		contextTotalTokens  int
		contextModelWindow  int
		pinnedAtRaw         sql.NullString
		createdAtRaw        string
		updatedAtRaw        string
		closedAtRaw         sql.NullString
	)
	if err := scanner.Scan(
		&key,
		&typ,
		&parentSessionKey,
		&parentToolCallID,
		&source,
		&taskID,
		&model,
		&shell,
		&planMode,
		&name,
		&relatedFilesJSON,
		&relatedWorktreeJSON,
		&contextTotalTokens,
		&contextModelWindow,
		&pinnedAtRaw,
		&createdAtRaw,
		&updatedAtRaw,
		&closedAtRaw,
	); err != nil {
		return nil, err
	}
	session := &Session{
		Key:              key,
		Type:             typ,
		ParentSessionKey: parentSessionKey,
		ParentToolCallID: parentToolCallID,
		Source:           source,
		TaskID:           taskID,
		Model:            model,
		Shell:            shell,
		PlanMode:         planMode != 0,
		Name:             name,
		Exchanges:        []Exchange{},
		RelatedFiles:     []RelatedFile{},
		LastContextWindow: agenttypes.ContextWindow{
			TotalTokens:        contextTotalTokens,
			ModelContextWindow: contextModelWindow,
		},
	}
	if strings.TrimSpace(relatedFilesJSON) != "" {
		if err := json.Unmarshal([]byte(relatedFilesJSON), &session.RelatedFiles); err != nil {
			session.RelatedFiles = []RelatedFile{}
		}
	}
	if strings.TrimSpace(relatedWorktreeJSON) != "" {
		var relatedWorktree RelatedWorktree
		if err := json.Unmarshal([]byte(relatedWorktreeJSON), &relatedWorktree); err == nil && strings.TrimSpace(relatedWorktree.Path) != "" {
			session.RelatedWorktree = &relatedWorktree
		}
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtRaw)
	if err != nil {
		createdAt = time.Time{}
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedAtRaw)
	if err != nil {
		updatedAt = createdAt
	}
	session.CreatedAt = createdAt
	session.UpdatedAt = updatedAt
	if pinnedAtRaw.Valid && strings.TrimSpace(pinnedAtRaw.String) != "" {
		pinnedAt, err := time.Parse(time.RFC3339Nano, pinnedAtRaw.String)
		if err == nil {
			session.PinnedAt = &pinnedAt
		}
	}
	if closedAtRaw.Valid && strings.TrimSpace(closedAtRaw.String) != "" {
		closedAt, err := time.Parse(time.RFC3339Nano, closedAtRaw.String)
		if err == nil {
			session.ClosedAt = &closedAt
		}
	}
	normalizeSessionMeta(session)
	return session, nil
}

func normalizeSessionMeta(s *Session) {
	if s.AgentCtxSeq == nil {
		s.AgentCtxSeq = map[string]int{}
	}
	if s.RelatedFiles == nil {
		s.RelatedFiles = []RelatedFile{}
	}
	for i := range s.RelatedFiles {
		s.RelatedFiles[i].RootID = strings.TrimSpace(s.RelatedFiles[i].RootID)
		s.RelatedFiles[i].RepoPath = strings.TrimSpace(s.RelatedFiles[i].RepoPath)
		s.RelatedFiles[i].RepoName = strings.TrimSpace(s.RelatedFiles[i].RepoName)
		s.RelatedFiles[i].RepoKind = strings.TrimSpace(s.RelatedFiles[i].RepoKind)
		s.RelatedFiles[i].Path = strings.TrimSpace(s.RelatedFiles[i].Path)
		s.RelatedFiles[i].Head = strings.TrimSpace(s.RelatedFiles[i].Head)
		s.RelatedFiles[i].Relation = strings.TrimSpace(s.RelatedFiles[i].Relation)
	}
	if s.RelatedWorktree != nil {
		s.RelatedWorktree.RootID = strings.TrimSpace(s.RelatedWorktree.RootID)
		s.RelatedWorktree.Path = strings.TrimSpace(s.RelatedWorktree.Path)
		s.RelatedWorktree.Branch = strings.TrimSpace(s.RelatedWorktree.Branch)
		s.RelatedWorktree.Head = strings.TrimSpace(s.RelatedWorktree.Head)
		if s.RelatedWorktree.Path == "" {
			s.RelatedWorktree = nil
		}
	}
	if s.Exchanges == nil {
		s.Exchanges = []Exchange{}
	}
	s.ParentSessionKey = strings.TrimSpace(s.ParentSessionKey)
	s.ParentToolCallID = strings.TrimSpace(s.ParentToolCallID)
	s.Source = strings.TrimSpace(s.Source)
}

func relatedFileIdentity(file RelatedFile) string {
	return strings.Join([]string{
		strings.TrimSpace(file.RepoKind),
		cleanRelatedRepoPath(file.RepoPath),
		strings.TrimSpace(file.Head),
		strings.TrimSpace(file.Path),
	}, "\x00")
}

func cleanRelatedRepoPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

var errSessionNotFound = errors.New("session not found")

func normalizeSearchLimit(limit int) int {
	switch {
	case limit <= 0:
		return 20
	case limit > 50:
		return 50
	default:
		return limit
	}
}

func scoreSessionName(name, qLower string) int {
	name = strings.TrimSpace(name)
	if name == "" || qLower == "" {
		return 0
	}
	nameLower := strings.ToLower(name)
	switch {
	case nameLower == qLower:
		return 120
	case strings.HasPrefix(nameLower, qLower):
		return 100
	case strings.Contains(nameLower, qLower):
		return 80
	default:
		return 0
	}
}

func buildSearchHit(s *Session, matchType string, score, seq int, snippet string) SearchHit {
	return SearchHit{
		Key:              s.Key,
		Type:             s.Type,
		ParentSessionKey: s.ParentSessionKey,
		ParentToolCallID: s.ParentToolCallID,
		Agent:            InferAgentFromSession(s),
		Model:            s.Model,
		Shell:            s.Shell,
		Name:             s.Name,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
		ClosedAt:         s.ClosedAt,
		MatchType:        matchType,
		MatchScore:       score,
		Seq:              seq,
		Snippet:          strings.TrimSpace(snippet),
	}
}

func sortSearchHits(items []SearchHit) {
	sort.SliceStable(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		if left.MatchType != right.MatchType {
			return searchMatchTypeRank(left.MatchType) < searchMatchTypeRank(right.MatchType)
		}
		if left.MatchScore != right.MatchScore {
			return left.MatchScore > right.MatchScore
		}
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		return left.Key < right.Key
	})
}

func searchMatchTypeRank(matchType string) int {
	switch strings.TrimSpace(matchType) {
	case "name":
		return 0
	case "user":
		return 1
	case "reply":
		return 2
	default:
		return 3
	}
}

func (m *Manager) searchSessionContent(s *Session, qLower string) (SearchHit, bool, error) {
	path, err := m.exchangePath(s.Key)
	if err != nil {
		return SearchHit{}, false, err
	}
	file, err := m.root.OpenMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SearchHit{}, false, nil
		}
		return SearchHit{}, false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	best := SearchHit{}
	bestFound := false
	bestRoleUser := false
	bestPos := 0
	bestSeq := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.Contains(strings.ToLower(line), qLower) {
			continue
		}
		var entry Exchange
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		content := strings.TrimSpace(entry.Content)
		if content == "" {
			continue
		}
		lowerContent := strings.ToLower(content)
		pos := strings.Index(lowerContent, qLower)
		if pos < 0 {
			continue
		}
		roleUser := strings.EqualFold(strings.TrimSpace(entry.Role), "user")
		matchRunes := utf8.RuneCountInString(lowerContent[:pos])
		queryRunes := utf8.RuneCountInString(qLower)
		matchType := "reply"
		matchScore := 60
		if roleUser {
			matchType = "user"
			matchScore = 65
		}
		hit := buildSearchHit(s, matchType, matchScore, entry.Seq, buildSearchSnippet(content, matchRunes, queryRunes))
		if !bestFound || roleUser && !bestRoleUser || roleUser == bestRoleUser && (pos < bestPos || pos == bestPos && entry.Seq < bestSeq) {
			best = hit
			bestFound = true
			bestRoleUser = roleUser
			bestPos = pos
			bestSeq = entry.Seq
		}
	}
	if err := scanner.Err(); err != nil {
		return SearchHit{}, false, err
	}
	if !bestFound {
		return SearchHit{}, false, nil
	}
	return best, true, nil
}

func buildSearchSnippet(content string, matchRunes, queryRunes int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	runes := []rune(content)
	start := 0
	end := len(runes)
	if matchRunes >= 0 {
		const contextBefore = 10
		const contextAfter = 18
		start = matchRunes - contextBefore
		if start < 0 {
			start = 0
		}
		end = matchRunes + queryRunes + contextAfter
		if end > len(runes) {
			end = len(runes)
		}
	}
	snippet := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(runes) {
		snippet += "..."
	}
	return snippet
}

func generateKey() string {
	now := time.Now().UTC().Unix()
	buf := make([]byte, 6)
	_, err := rand.Read(buf)
	if err != nil {
		return fmt.Sprintf("%d", now)
	}
	return fmt.Sprintf("%d-%s", now, hex.EncodeToString(buf))
}
