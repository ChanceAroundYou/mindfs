package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
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
	// CUSTOM(G-X): 不再 SELECT/写入 pinned_at —— 置顶的唯一权威是主节点的 pins 表
	// （server/internal/pins）。两处一起断：只断一半会留下「写进去但读不回」的假退役。
	selectSessionSQL = `
	SELECT key, type, parent_session_key, parent_tool_call_id, source, task_id, model, shell, plan_mode, name, related_files_json, related_worktree_json, last_context_window_total_tokens, last_context_window_model_context_window, archived_at, created_at, updated_at, closed_at
	FROM sessions`
	deleteSessionSQL = `
DELETE FROM sessions
WHERE key = ?`
	deleteBindingsBySessionSQL = `
DELETE FROM session_agent_bindings
WHERE session_key = ?`
	upsertSessionMetaSQL = `
INSERT INTO sessions (
		key, type, parent_session_key, parent_tool_call_id, source, task_id, model, shell, plan_mode, name, related_files_json, related_worktree_json, last_context_window_total_tokens, last_context_window_model_context_window, archived_at, created_at, updated_at, closed_at
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
	archived_at = excluded.archived_at,
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
	archived_at TEXT,
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
	external_source_committed_offset INTEGER NOT NULL DEFAULT 0,
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
SELECT session_key, agent, agent_session_id, agent_ctx_seq, external_source_path, external_source_offset, external_source_mtime_ns, external_source_committed_offset
FROM session_agent_bindings`
	selectAgentBindingSQL = `
SELECT session_key, agent, agent_session_id, agent_ctx_seq, external_source_path, external_source_offset, external_source_mtime_ns, external_source_committed_offset
FROM session_agent_bindings
WHERE session_key = ? AND agent = ?`
	selectAgentBindingsBySessionSQL = `
SELECT session_key, agent, agent_session_id, agent_ctx_seq, external_source_path, external_source_offset, external_source_mtime_ns, external_source_committed_offset
FROM session_agent_bindings
WHERE session_key = ?`
	selectBindingByAgentSessionSQL = `
SELECT session_key, agent, agent_session_id, agent_ctx_seq, external_source_path, external_source_offset, external_source_mtime_ns, external_source_committed_offset
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

type Manager struct {
	root             fs.RootInfo
	mu               sync.Mutex
	db               *sql.DB
	sessions         map[string]*Session
	exchangeCursors  map[string]exchangeFileCursor
	pendingToolCalls map[string]map[string]agenttypes.ToolCall
	now              func() time.Time
	// fullExchangeReads 统计 readExchangesFull（全量解析主日志）的调用次数。
	// 窗口读必须 O(窗口)：冷缓存下不得走全量解析 —— 全量解析在实测最重会话上
	// 13.8ms/1.2MB 且持 m.mu（会卡住整个 root）。测试直接断言它保持为 0，比计时稳定。
	fullExchangeReads int64
	// fullAuxReads 统计 readAuxFile（全量解析 aux）的调用次数。单会话 aux 实测最大 19MB，
	// 窗口读 / 「只要最新状态」的场景必须走尾部读，测试断言它不被触发。
	fullAuxReads int64
	// fullBlockReads 统计 readBlocksAll（全量解析块流）的调用次数。CUSTOM(G-BH)：
	// 块流是窗口回包的载荷来源，而窗口读持 m.mu（每 root 一把），全量读会卡住整个 root；
	// 实测单会话一轮可达 83 步 / 974 条消息，块流是 MB 级。与上面两个同思路，
	// 测试断言它保持为 0 而不是断言计时。
	fullBlockReads int64
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
	// ExternalSourceCommittedOffset 是已提交给 MindFS 的字节位置（见 ExternalSessionCursor）。
	// 0 表示该会话从未按偏移同步过，导入器会退回时间戳引导。
	ExternalSourceCommittedOffset int64 `json:"external_source_committed_offset,omitempty"`
}

func (b AgentBinding) ExternalCursor() agenttypes.ExternalSessionCursor {
	return agenttypes.ExternalSessionCursor{
		SourcePath:      b.ExternalSourcePath,
		Offset:          b.ExternalSourceOffset,
		ModTimeUnixNano: b.ExternalSourceMtimeNS,
		CommittedOffset: b.ExternalSourceCommittedOffset,
	}
}

type ListOptions struct {
	BeforeTime       time.Time
	AfterTime        time.Time
	ParentSessionKey string
	TopLevelOnly     bool
	Limit            int
	// ArchivedMode 过滤归档。零值 "" = 排除已归档（主面板的默认），
	// "only" = 只返回已归档（归档区用）。用字符串枚举而不是两个 bool：
	// 「既排除又只要」不是合法状态，两个 bool 能表达出来就一定会有人传错。
	ArchivedMode string
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
	// 建会话时就记下 agent：列表行是 meta-only 的，只靠回合结束时写的绑定会让
	// 首轮运行期间的徽标落回「AI」占位。写失败只记日志——sessions 行已经插进去了，
	// 返回错误会让调用方拿到 session.create_failed 却留下一个孤儿会话。
	if err := m.ensureAgentBindingUnsafe(session.Key, initialAgent); err != nil {
		log.Printf("[session/store] ensure.binding session=%s agent=%s err=%v", session.Key, initialAgent, err)
	}
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

func (m *Manager) GetWindow(_ context.Context, key string, beforeSeq, limit, latest int) (*Session, *SessionWindowMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getSessionWindowUnsafe(key, beforeSeq, limit, latest)
}

func (m *Manager) GetExchangeAuxWindow(_ context.Context, key string, seqSet map[int]bool) (map[int][]ExchangeAux, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadExchangeAuxWindow(key, seqSet)
}

// GetMeta 只加载 SQLite meta（不含 exchanges 文件），用于列表/名称查询等不需要完整会话的场景。
func (m *Manager) GetMeta(_ context.Context, key string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getSessionMetaWithBindingsUnsafe(key)
}

func (m *Manager) GetExchangeAux(_ context.Context, key string, afterSeq int) (map[int][]ExchangeAux, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadExchangeAux(key, afterSeq)
}

func (m *Manager) AddExchangeForAgent(ctx context.Context, session *Session, role, content, agent, mode, effort, fastService string) error {
	_, err := m.AddExchangeForAgentAndGetSeq(ctx, session, role, content, agent, mode, effort, fastService)
	return err
}

func (m *Manager) AddExchangeForAgentAt(ctx context.Context, session *Session, role, content, agent, mode, effort, fastService string, timestamp time.Time) error {
	_, err := m.addExchangeForAgentAtWithIdentityFactory(session, role, content, agent, exchangeModelDisplayNameFromContext(ctx), exchangeTokenUsageFromContext(ctx), exchangeSourceFromContext(ctx), exchangeIdentityFromContext(ctx), nil, mode, effort, fastService, timestamp)
	return err
}

// AddExchangeForAgentAndGetSeq appends an exchange and returns the seq allocated
// from the manager's current, locked view of the session. Callers that attach
// blocks or auxiliary data must use this value instead of inspecting a possibly
// stale caller-owned Session pointer.
func (m *Manager) AddExchangeForAgentAndGetSeq(ctx context.Context, session *Session, role, content, agent, mode, effort, fastService string) (int, error) {
	return m.addExchangeForAgentAtWithIdentityFactory(session, role, content, agent, exchangeModelDisplayNameFromContext(ctx), exchangeTokenUsageFromContext(ctx), exchangeSourceFromContext(ctx), exchangeIdentityFromContext(ctx), nil, mode, effort, fastService, time.Time{})
}

// AddExchangeForAgentAtWithIdentityFactory allocates the row seq under the
// manager lock, then builds identity from that exact seq before persisting it.
// This keeps deterministic user-turn IDs and turn_seq aligned with the row even
// if the caller's session snapshot was stale.
func (m *Manager) AddExchangeForAgentAtWithIdentityFactory(ctx context.Context, session *Session, role, content, agent, mode, effort, fastService string, timestamp time.Time, identityForSeq func(int) ExchangeIdentity) (int, error) {
	if identityForSeq == nil {
		return 0, errors.New("exchange identity factory required")
	}
	return m.addExchangeForAgentAtWithIdentityFactory(session, role, content, agent, exchangeModelDisplayNameFromContext(ctx), exchangeTokenUsageFromContext(ctx), exchangeSourceFromContext(ctx), exchangeIdentityFromContext(ctx), identityForSeq, mode, effort, fastService, timestamp)
}

func (m *Manager) addExchangeForAgentAtWithIdentityFactory(session *Session, role, content, agent, modelDisplayName string, tokenUsage *agenttypes.TokenUsage, source string, identity ExchangeIdentity, identityForSeq func(int) ExchangeIdentity, mode, effort, fastService string, timestamp time.Time) (int, error) {
	if session == nil || strings.TrimSpace(session.Key) == "" {
		return 0, errors.New("session required")
	}
	callerSession := session
	// S2d-2 起正文归块流，主日志一行的 `content` 恒空 —— 这里再按「空正文」跳过就把
	// **每一行 user**（用户消息不再带正文）静默丢掉：轮次元数据（mindfs_id / agent_id）
	// 跟着没，前端按 seq 合并时那句话不显示，搜索的 user 命中优先也失效。
	// 该拦的是「什么都没带」的纯空行：空正文 + 空 agent + 无任何轮次身份。
	if strings.TrimSpace(content) == "" && strings.TrimSpace(agent) == "" &&
		identity.MindFSID == "" && identity.AgentID == "" && identityForSeq == nil {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.getSessionUnsafe(session.Key, 0)
	if err != nil {
		return 0, err
	}
	session = current
	if session.ClosedAt != nil {
		session.ClosedAt = nil
	}
	resolvedAgent := strings.TrimSpace(agent)
	// seq 按「最大 seq + 1」分配，不按缓存长度：文件与缓存一旦漂移（外部写入、手工修文件、
	// 缓存陈旧），长度+1 会撞上已有 seq（前端按 seq 合并 → 消息直接不显示）。
	nextSeq := maxExchangeSeq(session.Exchanges) + 1
	if identityForSeq != nil {
		identity = identityForSeq(nextSeq)
	}
	ts := timestamp.UTC()
	if ts.IsZero() {
		ts = m.now().UTC()
	}
	record := Exchange{
		Seq:              nextSeq,
		Role:             role,
		Source:           source,
		Agent:            resolvedAgent,
		Model:            session.Model,
		ModelDisplayName: strings.TrimSpace(modelDisplayName),
		Mode:             strings.TrimSpace(mode),
		Effort:           strings.TrimSpace(effort),
		FastService:      fastService,
		LegacyContent:    content,
		TokenUsage:       tokenUsage,
		Timestamp:        ts,
		// 轮次元数据（方案 §3.1）。MindFSID 是权威身份，TurnSeq 是人读排序键，
		// AgentID 是 agent 侧桥梁键（可空、可后补、fork 后可变更）。
		MindFSID: identity.MindFSID,
		TurnSeq:  identity.TurnSeq,
		AgentID:  identity.AgentID,
		Partial:  identity.Partial,
	}
	if err := m.appendExchange(session.Key, record); err != nil {
		log.Printf("[session/store] append.error session=%s seq=%d role=%s agent=%s err=%v", session.Key, record.Seq, role, resolvedAgent, err)
		return 0, err
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
		if callerSession != session {
			*callerSession = *session
		}
		return record.Seq, err
	}
	// getSessionUnsafe may have replaced a stale cache entry with a freshly loaded
	// pointer. Keep the caller's snapshot usable after a successful append while
	// retaining the manager's canonical cached pointer.
	if callerSession != session {
		*callerSession = *session
	}
	return record.Seq, nil
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

// ClearRelatedWorktree 解除会话与工作树的归属关系。
//
// 为什么不能靠 RecordRelatedWorktree 做到：它拒绝空 path（worktree path required），
// 而且已有归属时直接返回 false —— 那是「只记一次」的语义，repoint 需要的是**清除**。
// 清空后 sessionRuntimeRootPath 返回空，运行时 cwd 回到主 checkout。
func (m *Manager) ClearRelatedWorktree(_ context.Context, key string) (bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return false, errors.New("session key required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return false, err
	}
	if session.RelatedWorktree == nil || strings.TrimSpace(session.RelatedWorktree.Path) == "" {
		return false, nil
	}
	session.RelatedWorktree = nil
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) RepointAgentBinding(sessionKey, agent, previousAgentSessionID, agentSessionID string, cursor agenttypes.ExternalSessionCursor) error {
	sessionKey = strings.TrimSpace(sessionKey)
	agent = strings.TrimSpace(agent)
	agentSessionID = strings.TrimSpace(agentSessionID)
	if sessionKey == "" {
		return errors.New("session key required")
	}
	if agent == "" {
		return errors.New("agent required")
	}
	if agentSessionID == "" {
		return errors.New("agent session id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// ctx_seq 必须**原样保留**。它是 Full 同步的幂等护栏：externalSessionDeltaAfterCtxSeq
	// 用它切掉「库里已有」的前缀，写 0 会让 `agentCtxSeq <= 0` 分支直接放行全部，
	// 下次点「同步」就把早已渲染过的回合重导一遍 —— 2026-09-16 那个 bug 的形态。
	// repoint 只换 id 和游标、不增删 exchange，所以原值就是正确值。
	// 读和写都在事务内，避免并发同步在这两步之间改掉它。
	var existing AgentBinding
	if err := scanAgentBinding(tx.QueryRow(selectAgentBindingSQL, sessionKey, agent), &existing); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.Exec(upsertAgentBindingSQL, sessionKey, agent, agentSessionID, existing.AgentCtxSeq); err != nil {
		return err
	}

	if _, err := tx.Exec(
		`UPDATE session_agent_bindings SET external_source_path = ?, external_source_offset = ?, external_source_mtime_ns = ?, external_source_committed_offset = ? WHERE session_key = ? AND agent = ?`,
		strings.TrimSpace(cursor.SourcePath), cursor.Offset, cursor.ModTimeUnixNano, cursor.CommittedOffset, sessionKey, agent,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// 名字跟着换 id，否则重开会话时按新 agent_session_id 查不到名字、会退回默认名。
	// 这条走 session_external_names —— 它才是 LookupAliasForAgent 真正读的表
	//（selectExternalSessionNameSQL）。放在事务外：它写的是另一张表，失败不影响
	// 「id + 游标」这个必须原子的一致性。
	//
	// 旧 id 的那条必须删掉：upsertExternalSessionNameUnsafe 只 upsert 新 id，旧行会一直
	// 留着并继续被旧 id 查到。两个 id 同时能查到同一个名字，下次按旧 id 导入时就会
	// 认领到一条本该属于别的会话的名字。

	if previousAgentSessionID = strings.TrimSpace(previousAgentSessionID); previousAgentSessionID != "" && previousAgentSessionID != agentSessionID {
		if _, err := db.Exec(`DELETE FROM session_external_names WHERE agent = ? AND agent_session_id = ?`, agent, previousAgentSessionID); err != nil {
			return err
		}
	}
	current, err := m.getSessionUnsafe(sessionKey, 0)
	if err != nil {
		return err
	}
	return m.upsertExternalSessionNameUnsafe(agent, agentSessionID, current.Name)
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
			if m.sessionCacheFreshUnsafe(key) {
				return cached, nil
			}
			// 文件在盘上变了（外部写入 / 手工修文件 / 同步冲突）→ 别拿陈旧缓存当真相：
			// 写路径的 seq 与判重都基于这份缓存（实测 2026-09-14 手工改文件后不重启，
			// 缓存与文件错位 → 重复 seq / 空洞）。交给 loadExchanges 走增量或全量读。
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

// sessionCacheFreshUnsafe 报告内存缓存是否仍然与磁盘一致。
// 只有「stat 成功且 size/mtime 与读盘游标不符」才判定为陈旧；没游标（从未读过盘）或
// stat 失败一律返回 true —— 保持既有行为，不用一次偶发失败去清掉缓存。
func (m *Manager) sessionCacheFreshUnsafe(key string) bool {
	path, err := m.exchangePath(key)
	if err != nil {
		return true
	}
	cursor, ok := m.exchangeCursors[path]
	if !ok || cursor.Offset <= 0 {
		// 没有读盘基线（例如会话是刚 Create 出来的内存缓存）：文件里已经有内容，就说明
		// 缓存可能是空的或陈旧的 —— 读一次盘建立基线，代价只在这一次。
		if info, statErr := m.root.StatMetaFile(path); statErr == nil && info.Size() > 0 {
			return false
		}
		return true
	}
	info, err := m.root.StatMetaFile(path)
	if err != nil {
		return true
	}
	return info.Size() == cursor.Size && info.ModTime().UnixNano() == cursor.ModTimeNs
}

// getSessionWindowUnsafe 返回按窗口切片后的 Session 与窗口元信息。
// beforeSeq>0：取 seq<beforeSeq 的尾部 limit 条（二分定位 idx=首个 seq>=beforeSeq，窗口 all[max(0,idx-limit):idx]，hasMore=idx-limit>0）；
// latest>0：取尾部 limit 条；两者皆 0：默认尾部 limit 条（等价于 latest=limit）。
//
// 缓存已建立时在内存切片；**冷缓存下走 readExchangeWindow 只解析窗口所需的尾部记录**，
// 不再 readExchangesFull 全量解析（全量解析实测 13.8ms/1.2MB 且持 m.mu，会卡住整个 root）。
// 仍不写入 m.sessions 全量缓存，避免污染 afterSeq<=0 缓存分支。
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
func jsonlScanner(payload []byte) *bufio.Scanner {
	scanner := bufio.NewScanner(strings.NewReader(string(payload)))
	maxTokenSize := len(payload) + 1
	if maxTokenSize < 64*1024 {
		maxTokenSize = 64 * 1024
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxTokenSize)
	return scanner
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

func generateKey() string {
	now := time.Now().UTC().Unix()
	buf := make([]byte, 6)
	_, err := rand.Read(buf)
	if err != nil {
		return fmt.Sprintf("%d", now)
	}
	return fmt.Sprintf("%d-%s", now, hex.EncodeToString(buf))
}
