package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

var openSQLiteDB = func(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path)
}

var mindFSConfigDir = configpkg.MindFSConfigDir

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

func (m *Manager) List(_ context.Context, opts ListOptions) ([]*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listSessionsUnsafe(opts)
}

// ListByKeys 按 key 取会话元数据（不读转录正文），按传入顺序返回。
//
// 与 Get 的区别是刻意不碰转录：置顶表里可能装着**别的机器**上的会话键，
// 而本机的会话库里根本没有那些行。Get 会去 loadSession 读 JSONL，
// 对一个本机不存在的 key 那是白读一趟；对存在的 key 又太重（要按需拉正文）。
// 这里只查 session meta 表，命中不了就是命中不了，交给调用方丢弃。
func (m *Manager) ListByKeys(_ context.Context, keys []string) ([]*Session, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.ensureSessionMetaDBUnsafe(); err != nil {
		return nil, err
	}
	unique := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}
	if len(unique) == 0 {
		return nil, nil
	}
	query := selectSessionSQL + `
WHERE key IN (?` + strings.Repeat(`,?`, len(unique)-1) + `)`
	args := make([]any, 0, len(unique))
	for _, key := range unique {
		args = append(args, key)
	}
	found, err := m.querySessionMetasUnsafe(query, args)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]*Session, len(found))
	for _, s := range found {
		if s != nil {
			byKey[s.Key] = s
		}
	}
	out := make([]*Session, 0, len(found))
	for _, key := range unique {
		if s, ok := byKey[key]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *Manager) Count(_ context.Context, opts ListOptions) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.countSessionsUnsafe(opts)
}

// LatestSessionUpdatedAt returns the newest visible session timestamp in this
// root, including child sessions. It reads the indexed metadata table, so the
// multi-root sidebar does not have to load every transcript just to rank roots.
func (m *Manager) LatestSessionUpdatedAt(_ context.Context) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return time.Time{}, err
	}
	var latest sql.NullString
	if err := db.QueryRow(`
		SELECT updated_at
		FROM sessions
		WHERE archived_at IS NULL
		ORDER BY julianday(updated_at) DESC
		LIMIT 1
	`).Scan(&latest); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, err
	}
	if !latest.Valid || strings.TrimSpace(latest.String) == "" {
		return time.Time{}, nil
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, latest.String)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse latest session timestamp %q: %w", latest.String, err)
	}
	return updatedAt, nil
}

func (m *Manager) ListMetas(_ context.Context) ([]*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listSessionMetasUnsafe()
}

type exchangeModelDisplayNameContextKey struct{}
type exchangeTokenUsageContextKey struct{}
type exchangeSourceContextKey struct{}
type exchangeIdentityContextKey struct{}

// ExchangeIdentity 是落在 exchange 行上的**轮次**次元数据（方案 §3.1）。
//
// 为什么用 context 而不是给 addExchangeForAgentAt 加参数：identity 是轮次级 ——
// 同一轮的用户行与助手行必须带**同一个** mindfs_id，而调用点已有 8 处。
// 与 WithExchangeSource / WithExchangeModelDisplayName 同一套机制。
type ExchangeIdentity struct {
	// MindFSID 是 MindFS 权威身份：落盘即定、永不改。由写入点统一 mint
	// (NewMindFSID, UUIDv5 确定性)，不再由客户端生成、也不再用随机 UUIDv4。
	MindFSID string
	// TurnSeq 是「打开该轮的 user 行 seq」，排序 / 人读用。
	TurnSeq int
	// AgentID 是 agent 侧的身份（可空、可后补）：claude 转录 uuid / ACP messageId。
	AgentID string
	// Partial 标记「本轮未收尾」：崩溃 / 中断丢缓冲区时靠 import 补齐（方案 D13）。
	Partial bool
}

// WithExchangeIdentity 标注这一行所属的轮次身份。零值 = 历史数据 / 无身份。
func WithExchangeIdentity(ctx context.Context, identity ExchangeIdentity) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, exchangeIdentityContextKey{}, identity)
}

// mindfsUUIDNamespace 是 MindFS 铸 id 使用的 UUIDv5 namespace。
//
// 固定常量而非随机生成：确定性 id 的全部意义在于「同一个 seed 在任何机器、
// 任何一次进程重启都算出同一个 id」。namespace 变了，历史 id 就对不上了。
// 换 namespace = 全库身份作废，只在「身份方案重做」时动它。
var mindfsUUIDNamespace = "6f2b1c4e-5a3d-4f87-9e12-3c8a7b0d5f61"

// NewMindFSID mint 一个**确定性** mindfs_id（UUIDv5，方案 D4/D5）。
//
// 为什么从 UUIDv4 换成 UUIDv5：v4 每次调用都给新 id，于是「同一条消息」在两个写入者
// （实时 live / 转录导入 import）手里各有一个 id，判重只能退回内容比对 —— 那正是
// 2026-10-07 实测 9 对 live/import 重复行的成因。UUIDv5 让 id 由内容派生：
// 同一个 seed 永远得到同一个 id，重导天然幂等，两个写入者也自动对齐。
//
// seed 优先级：agentID（agent 侧身份）优先，为空时退到 fallbackSeed（sessionKey|role|seq）。
// 两者都空返回空串 = 「这一行没有身份」，调用方自己决定怎么处理。
func NewMindFSID(agentID, fallbackSeed string) string {
	seed := strings.TrimSpace(agentID)
	if seed == "" {
		seed = strings.TrimSpace(fallbackSeed)
	}
	if seed == "" {
		return ""
	}
	// UUIDv5 = SHA1(namespaceBytes + seed)，自己算而不引 google/uuid：
	// 只需「格式正确的确定性 UUID」，crypto/sha1 在 stdlib 里。
	h := sha1.New() //nolint:gosec // UUIDv5 规范指定 SHA-1，非安全用途
	h.Write([]byte(mindfsUUIDNamespace))
	h.Write([]byte{0}) // namespace 与 name 的分隔，避免拼接歧义
	h.Write([]byte(seed))
	sum := h.Sum(nil)
	var buf [16]byte
	copy(buf[:], sum[:16])
	buf[6] = (buf[6] & 0x0f) | 0x50 // version 5
	buf[8] = (buf[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// NewUserTurnMindFSID derives a user's turn identity from the allocated user
// seq. The live path passes the persisted value downstream; this helper also
// supports callers that only have the seq available.
func NewUserTurnMindFSID(sessionKey string, userSeq int) string {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return ""
	}
	return NewMindFSID("", fmt.Sprintf("%s|user|%d", sessionKey, userSeq))
}

// IsTurnIDShape 校验 mindfs_id 的形态（8-4-4-4-12，十六进制，大小写不敏感）。
//
// S2e 起 pending 的回合身份也用 mindfs_id，两处必须认同一种形状 —— 于是判据只有一份，
// 放在 MindFSID 字段旁边。ACP 的 messageId 规范（「客户端与 agent 必须使用 UUID 格式」）
// 要求的正是这一种；不认识的形状一律当「无身份」，别拿它当键去对。
func IsMindFSIDShape(raw string) bool {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if len(normalized) != 36 {
		return false
	}
	for i, r := range normalized {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if r != '-' {
				return false
			}
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

func exchangeIdentityFromContext(ctx context.Context) ExchangeIdentity {
	if ctx == nil {
		return ExchangeIdentity{}
	}
	identity, _ := ctx.Value(exchangeIdentityContextKey{}).(ExchangeIdentity)
	return identity
}

// WithExchangeSource 标注这一行由哪个写入者落盘（ExchangeSourceLive / ExchangeSourceImport）。
// 不传则 Source 为空 = 历史数据；来源字段供会话所有权与同步逻辑使用。
func WithExchangeSource(ctx context.Context, source string) context.Context {
	source = strings.TrimSpace(source)
	if source == "" {
		return ctx
	}
	return context.WithValue(ctx, exchangeSourceContextKey{}, source)
}

func exchangeSourceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(exchangeSourceContextKey{}).(string)
	return strings.TrimSpace(value)
}

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

// RepointAgentBinding 在**同一个事务**里换 agent_session_id 并改写游标。
//
// 为什么不复用 UpsertAgentBinding + UpdateExternalSessionCursor 分两次写：repoint 的
// 语义要求「换 id」和「游标指向新转录的末尾」同时成立。分两次写时，若第二次失败，
// binding 已经是新 id、游标却还指着旧路径的冻结偏移——导入器的 externalSessionFileCursor
// 按 SourcePath 判「变没变」，路径一变就算变了，于是从 committed（live-owned 会话为 0）
// 整份重读，正是 2026-09-16「ask 下面又渲染了一轮出现过的文字」的成因。合成一个事务后
// 要么都成、要么都没动，不存在这个中间态。
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

func (m *Manager) UpdateExternalSessionCursor(_ context.Context, sessionKey, agent string, cursor agenttypes.ExternalSessionCursor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE session_agent_bindings SET external_source_path = ?, external_source_offset = ?, external_source_mtime_ns = ?, external_source_committed_offset = ? WHERE session_key = ? AND agent = ?`,
		strings.TrimSpace(cursor.SourcePath), cursor.Offset, cursor.ModTimeUnixNano, cursor.CommittedOffset, strings.TrimSpace(sessionKey), strings.TrimSpace(agent))
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

// EnsureAgentBinding 记下「这个会话在用哪个 agent」，幂等。
// 已有绑定行时（不论 agent_session_id 是否为空）原样返回，绝不覆盖真实转录 id。
//
// 约定：空 agent_session_id 表示「agent 已选定、尚无转录」——所有读者本来就把空 id
// 当成无绑定处理（别名回填/外部名同步/repoint/fork 都有空 id 守卫），唯一的可见效果
// 是列表路径拿到 AgentCtxSeq 单键 → InferAgentFromSession 能报出 agent，徽标不再落回「AI」。
func (m *Manager) EnsureAgentBinding(_ context.Context, sessionKey, agent string) error {
	if strings.TrimSpace(sessionKey) == "" {
		return errors.New("session key required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureAgentBindingUnsafe(sessionKey, agent)
}

func (m *Manager) ensureAgentBindingUnsafe(sessionKey, agent string) error {
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return nil
	}
	sessionKey = strings.TrimSpace(sessionKey)
	db, err := m.ensureSessionMetaDBUnsafe()
	if err != nil {
		return err
	}
	var existing AgentBinding
	err = scanAgentBinding(db.QueryRow(selectAgentBindingSQL, sessionKey, agent), &existing)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// agent_ctx_seq 必须留 0：非 0 会被 prependSwitchHint 当成「已同步到此行」而吞掉 agent 切换提示。
	_, err = db.Exec(upsertAgentBindingSQL, sessionKey, agent, "", 0)
	return err
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
	return scanner.Scan(&binding.SessionKey, &binding.Agent, &binding.AgentSessionID, &binding.AgentCtxSeq, &binding.ExternalSourcePath, &binding.ExternalSourceOffset, &binding.ExternalSourceMtimeNS, &binding.ExternalSourceCommittedOffset)
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

// SetArchived 归档/取消归档一个会话。归档只打标记：JSONL 正文一行不动，
// 深链接仍能打开、搜索仍搜得到，只有 Delete 才真正清内容。
// 与 SetPinned 同形状：已是目标态就早返回，不白写一次库（也就不会改动 updated_at）。
func (m *Manager) SetArchived(_ context.Context, key string, archived bool) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return nil, err
	}
	if archived {
		if session.ArchivedAt != nil {
			return session, nil
		}
		now := m.now().UTC()
		session.ArchivedAt = &now
	} else {
		if session.ArchivedAt == nil {
			return session, nil
		}
		session.ArchivedAt = nil
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
	// 块流是正文载体（CUSTOM(G-BH)）：删会话必须连它一起删，否则正文留在盘上，
	// 既是隐私残留，也会在会话 key 复用时被当成「这个会话本来就有正文」读出来。
	blocksPath, err := m.blocksPath(key)
	if err != nil {
		return err
	}
	delete(m.exchangeCursors, blocksPath)
	if err := os.Remove(filepath.Join(metaDir, filepath.FromSlash(blocksPath))); err != nil && !os.IsNotExist(err) {
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

	var window []Exchange
	total := 0
	hasMore := false
	cached, hasCache := m.sessions[key]
	if hasCache && cached != nil && len(cached.Exchanges) > 0 {
		window, total, hasMore = sliceExchangeWindow(cached.Exchanges, beforeSeq, limit, latest)
	} else if read, count, more, ok := m.readExchangeWindow(path, beforeSeq, limit, latest); ok {
		window, total, hasMore = read, count, more
	} else {
		// 尾部读不可用（不可 seek / 读取异常）→ 回退全量解析，保证正确性。
		all, _, rErr := m.readExchangesFull(path)
		if rErr != nil {
			return nil, nil, rErr
		}
		window, total, hasMore = sliceExchangeWindow(all, beforeSeq, limit, latest)
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

// sliceExchangeWindow 在已按 seq 升序排列的 exchanges 上取窗口，返回 (窗口, 总数, 是否还有更早)。
// 语义与 readExchangeWindow 必须逐条一致：latest>0 时窗口就是尾部 latest 条（与 limit 解耦）。
func sliceExchangeWindow(all []Exchange, beforeSeq, limit, latest int) ([]Exchange, int, bool) {
	total := len(all)
	switch {
	case latest > 0:
		start := total - latest
		if start < 0 {
			start = 0
		}
		return all[start:], total, start > 0
	case beforeSeq > 0:
		idx := firstIndexWhereSeq(all, beforeSeq)
		start := idx - limit
		if start < 0 {
			start = 0
		}
		return all[start:idx], total, start > 0
	default:
		start := total - limit
		if start < 0 {
			start = 0
		}
		return all[start:], total, start > 0
	}
}

// readExchangeWindow 冷缓存下的窗口读：只解析窗口所需的尾部记录，不构建全量切片。
//
// 返回 (窗口, 记录总数, 是否还有更早, ok)。ok=false 表示读盘失败，调用方回退全量解析。
// 成本：一次读盘 + 一遍字节级行计数（不解析 JSON）+ 只解析尾部窗口条数。
// 实测 1.19MB / 206 行（每行 ~5.7KB 正文）：全量解析 15.3ms → 1.4ms，
// 其中读盘 0.9ms、行计数 0.14ms、解析 9 条 0.8ms。真实最重会话 391KB 时按比例更低。
func (m *Manager) readExchangeWindow(path string, beforeSeq, limit, latest int) ([]Exchange, int, bool, bool) {
	payload, err := m.root.ReadMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Exchange{}, 0, false, true
		}
		return nil, 0, false, false
	}
	if len(payload) == 0 {
		return []Exchange{}, 0, false, true
	}

	// latest>0 时窗口就是尾部 latest 条，与 beforeSeq 互斥（与 sliceExchangeWindow 一致）。
	need := limit
	if latest > 0 {
		need = latest
		beforeSeq = 0
	}
	if need <= 0 {
		need = 50
	}
	window, hasMore := tailExchangeWindow(payload, beforeSeq, need)
	return window, countExchangeRecords(payload), hasMore, true
}

// countExchangeRecords 字节级统计非空行数（不解析 JSON）。
// 语义上等于「记录条数」：实测 310 份真实主日志无空行、无同 seq 多行，
// 退化数据（同 seq 多行）由全量读的 mergeExchangeRecord 负责合并。
// 用 bytes.IndexByte 而不是逐字节 switch：后者在 1.19MB 上要 3ms，前者 0.1ms 量级。
func countExchangeRecords(payload []byte) int {
	count := 0
	for len(payload) > 0 {
		idx := bytes.IndexByte(payload, '\n')
		var line []byte
		if idx < 0 {
			line = payload
			payload = nil
		} else {
			line = payload[:idx]
			payload = payload[idx+1:]
		}
		if len(bytes.TrimSpace(line)) > 0 {
			count++
		}
	}
	return count
}

// tailExchangeWindow 从 payload 尾部向前取 need+1 条记录（升序返回），
// hasMore 报告窗口之前还有更早的记录。只解析取到的那些行，其余行仅被跳过。
//
// bytes.LastIndexByte 从尾部向前扫，命中即停，所以每行定位成本是 O(行长) 而非 O(文件)。
func tailExchangeWindow(payload []byte, beforeSeq, need int) ([]Exchange, bool) {
	target := need + 1
	records := make([]Exchange, 0, target)
	end := len(payload)
	for end > 0 && len(records) < target {
		if payload[end-1] == '\n' {
			end--
			continue
		}
		start := bytes.LastIndexByte(payload[:end], '\n') + 1
		raw := bytes.TrimSpace(payload[start:end])
		end = start
		if len(raw) == 0 {
			continue
		}
		entry, ok, _ := parseExchangeLine(string(raw))
		if !ok {
			continue
		}
		if beforeSeq > 0 && entry.Seq >= beforeSeq {
			continue
		}
		records = append(records, entry)
	}
	hasMore := len(records) > need
	if len(records) > need {
		records = records[:need]
	}
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	return records, hasMore
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
	// 归档过滤放在这里（唯一的 WHERE 构造器），四个列表入口一次覆盖：
	// 主列表、置顶列表、计数、子会话列表。置顶的归档会话会从置顶区掉进归档区 ——
	// 这是期望行为，归档就该从主视图消失。
	if strings.TrimSpace(opts.ArchivedMode) == "only" {
		where = append(where, "archived_at IS NOT NULL")
	} else {
		where = append(where, "archived_at IS NULL")
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

	// 本地会话库已存在却打不开时，绝不回退到空库：回退会让全部历史会话在界面上凭空消失，
	// 而根因（多半是某条 schema 迁移/索引在旧库上报错）仍然留在原地，下次启动照样复现。
	// 只有本地库压根不存在时，"从零新建"才是正确行为。
	if _, statErr := os.Stat(legacyDBFile); statErr == nil {
		return nil, fmt.Errorf("open legacy session db: %w (refusing to fall back to an empty db: %s exists)", legacyErr, legacyDBFile)
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("open legacy session db: %w; stat %s: %v", legacyErr, legacyDBFile, statErr)
	}

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
		`ALTER TABLE sessions ADD COLUMN archived_at TEXT`,
		`ALTER TABLE session_agent_bindings ADD COLUMN external_source_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE session_agent_bindings ADD COLUMN external_source_offset INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE session_agent_bindings ADD COLUMN external_source_mtime_ns INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE session_agent_bindings ADD COLUMN external_source_committed_offset INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE session_name_aliases ADD COLUMN agent TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE session_name_aliases ADD COLUMN agent_session_id TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
			db.Close()
			return nil, err
		}
	}
	// 索引必须建在列补齐之后：CREATE INDEX 引用尚不存在的列会报 "no such column"，
	// 让整个 openSessionMetaDB 失败，进而把完好的旧库误判成不可用并静默回退到空库。
	// 列表/搜索按 updated_at DESC 排序，无索引时全表排序。
	// 不再建 idx_sessions_pinned_at：该列已停用（见 selectSessionSQL 上的 CUSTOM(G-X)），
	// 索引只会给一个恒为 NULL 的列白占写入成本。存量索引由旧版建过，留着也无害。
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_archived_at ON sessions(archived_at, updated_at DESC)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
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
	var archivedAt any
	if session.ArchivedAt != nil {
		archivedAt = session.ArchivedAt.UTC().Format(time.RFC3339Nano)
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
		archivedAt,
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
		archivedAtRaw       sql.NullString
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
		&archivedAtRaw,
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
	if archivedAtRaw.Valid && strings.TrimSpace(archivedAtRaw.String) != "" {
		archivedAt, err := time.Parse(time.RFC3339Nano, archivedAtRaw.String)
		if err == nil {
			session.ArchivedAt = &archivedAt
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

func generateKey() string {
	now := time.Now().UTC().Unix()
	buf := make([]byte, 6)
	_, err := rand.Read(buf)
	if err != nil {
		return fmt.Sprintf("%d", now)
	}
	return fmt.Sprintf("%d-%s", now, hex.EncodeToString(buf))
}
