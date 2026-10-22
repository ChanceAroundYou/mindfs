package session

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"

	_ "modernc.org/sqlite"
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
	// BlockCount 是本次窗口里的正文块数（裁剪后）。0 表示没有块流（旧会话）。
	BlockCount int `json:"blockCount,omitempty"`
	// Truncated 表示窗口被**块数预算**裁过：更老的轮次被推给下一次 before_seq。
	Truncated bool `json:"truncated,omitempty"`
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
