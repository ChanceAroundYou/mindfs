package usecase

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

type ClientContext struct {
	CurrentRoot   string     `json:"current_root"`
	PluginCatalog string     `json:"plugin_catalog,omitempty"`
	Selection     *Selection `json:"selection,omitempty"`
}

type Selection struct {
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Text      string `json:"text,omitempty"`
}

type ListSessionsInput struct {
	RootID          string
	BeforeTime      time.Time
	AfterTime       time.Time
	Limit           int
	TopLevelOnly    bool
	IncludeChildren bool
	// ArchivedOnly = 只列已归档（归档区）。默认 false = 排除已归档（主面板）。
	ArchivedOnly bool
}

type ListSessionsOutput struct {
	Sessions []*session.Session
	// 置顶（pinned_items / pinned_keys）**不在这里**：权威是主节点的置顶表，
	// 由 api 层按项目取（http_pins.go 的 pinnedSessionsForRoot）。放这里就得让
	// 本机会话库再算一遍，等于把退役的 pinned_at 复活。
	TotalCount int
}

type ListMultiRootSessionsInput struct {
	LimitPerRoot int
}

type SessionRootGroup struct {
	RootID            string
	RootName          string
	LatestSessionTime time.Time
	Sessions          []*session.Session
	TotalCount        int
}

type ListMultiRootSessionsOutput struct {
	Groups []SessionRootGroup
}

type ListChildSessionsInput struct {
	RootID           string
	ParentSessionKey string
	BeforeTime       time.Time
	Limit            int
}

type SearchSessionsInput struct {
	RootID    string
	Query     string
	Limit     int
	MultiRoot bool
}

type SearchSessionsOutput struct {
	Items []SessionSearchHit
}

type SessionSearchHit struct {
	RootID           string     `json:"root_id,omitempty"`
	Key              string     `json:"key"`
	Type             string     `json:"type"`
	ParentSessionKey string     `json:"parent_session_key,omitempty"`
	ParentToolCallID string     `json:"parent_tool_call_id,omitempty"`
	Agent            string     `json:"agent,omitempty"`
	Model            string     `json:"model,omitempty"`
	Shell            string     `json:"shell,omitempty"`
	Name             string     `json:"name"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	MatchType        string     `json:"match_type"`
	MatchScore       int        `json:"match_score"`
	Seq              int        `json:"seq"`
	Snippet          string     `json:"snippet,omitempty"`
}

type BuildPromptInput struct {
	Session                       *session.Session
	Manager                       *session.Manager
	Agent                         string
	Message                       string
	ClientContext                 ClientContext
	AgentCtxSeq                   *int
	IsInitial                     bool
	IncludeReplyTipsInUserMessage bool
	// LinesBeforeThisTurn 是「本轮 user 行写入之前」的会话行数。本轮 user 行在
	// SendMessage 起手就已落盘（见 persistUserTurnExchange），所以 Session.Exchanges
	// 里已经包含它；切 agent 提示要读的「上一轮之后还剩几行」必须用写入前的计数，
	// 否则会把本轮自己的 user 行当成「需要回读的历史」。
	//
	// 必须是 *int 而不是 int：会话首轮的真实值**恰好也是 0**，用零值当「没传」的哨兵
	// 会让首轮回退到 len(Session.Exchanges)=1，于是 delta=1>0 —— 全新会话的第一轮就被
	// 告知「本会话从别处迁移而来，去读你自己的历史文件」，读到的是刚收到的那条消息。
	// 语义与同结构体的 AgentCtxSeq 一致，nil 即「没传」。
	LinesBeforeThisTurn *int
}

type SendMessageInput struct {
	RootID                 string
	RuntimeRootPath        string
	Key                    string
	Agent                  string
	Model                  string
	Mode                   string
	Effort                 string
	FastService            string
	PlanMode               *bool
	Shell                  string
	TerminalCols           int
	Content                string
	UserTimestamp          time.Time
	ClientCtx              ClientContext
	OnStart                func(MessageStart)
	OnUpdate               func(agenttypes.Event)
	OnSubSessionCreated    func(*session.Session)
	OnSubSessionUpdate     func(sessionKey string, update agenttypes.Event)
	OnAgentDefaultsChanged func(agentName string)
}

type MessageStart struct {
	Model            string
	ModelDisplayName string
	Mode             string
	Effort           string
	FastService      string
	// MindFSID is the canonical identity returned by the persisted user row.
	// Stream pending state must carry this value, not independently mint one.
	MindFSID string
	// UserExchangeSeq 是本轮用户消息**已落盘**的 seq（user 行在回合开始前就写入了，
	// 见 persistUserTurnExchange），不是「即将获得」的预测值。仅用于下发给客户端做本地
	// 乐观条目的收敛；0 表示未知（写入失败或会话里没有可落地的行），此时不要下发。
	//
	// 为什么必须是真值：客户端据此认定该行已持久化，于是 overlay 的
	// 「seq<=latestSeq 即让位给窗口」规则不再保留它。若这里给的是预测值，窗口
	// maxSeq 会在整轮执行期间都看不到它 —— 用户自己的消息就会从列表里消失，
	// 且点同步也刷不回来（2026-09-28 实测）。
	UserExchangeSeq int
}

func applyMessageRuntimeDefaultsFromStatus(
	in *SendMessageInput,
	status agent.Status,
	statusOK bool,
) {
	if in == nil || strings.TrimSpace(in.Model) != "" {
		return
	}
	in.Model = ""
	in.Effort = strings.TrimSpace(in.Effort)
	in.FastService = strings.TrimSpace(in.FastService)
	if !statusOK {
		return
	}
	in.Model = strings.TrimSpace(status.DefaultModelID)
	if in.Model == "" {
		in.Model = strings.TrimSpace(status.CurrentModelID)
	}
	if in.Effort == "" {
		in.Effort = strings.TrimSpace(status.DefaultEffort)
	}
	if in.FastService == "" {
		in.FastService = strings.TrimSpace(status.DefaultFastService)
	}
}

func (s *Service) applyMessageRuntimeDefaults(in *SendMessageInput) {
	if in == nil || strings.TrimSpace(in.Model) != "" {
		return
	}
	if s == nil || s.Registry == nil || s.Registry.GetProber() == nil {
		applyMessageRuntimeDefaultsFromStatus(in, agent.Status{}, false)
		return
	}
	status, ok := s.Registry.GetProber().GetStatus(strings.TrimSpace(in.Agent))
	if ok {
		if prefs := s.Registry.GetPreferences(); prefs != nil {
			status = prefs.ApplyAgentDefaults([]agent.Status{status})[0]
		}
	}
	applyMessageRuntimeDefaultsFromStatus(in, status, ok)
}

func sendMessageUserTimestamp(in SendMessageInput, fallback time.Time) time.Time {
	if !in.UserTimestamp.IsZero() {
		return in.UserTimestamp.UTC()
	}
	if fallback.IsZero() {
		return time.Now().UTC()
	}
	return fallback.UTC()
}

type AnswerQuestionInput struct {
	RootID     string
	SessionKey string
	Agent      string
	ToolUseID  string
	Answers    map[string]string
}

type CancelSessionTurnInput struct {
	RootID string
	Key    string

	// TargetTurnID / TargetPinned 描述调用方「点下取消那一刻」在跑的是哪一轮。
	//
	// 取消的落地晚于它的决定：请求从用户点到执行可能隔几百毫秒（ws.go 怕阻塞读循环
	// 用 goroutine 派发），这期间同一会话完全可能已经开了新一轮。实测 2026-10-02：
	// 用户在空闲会话上点停止，几百毫秒后又发了一条消息，那一轮刚 send.begin 就被这个
	// 迟到的取消掐掉 —— dsh 表现为「733ms 就完成了，什么都没吐」。
	//
	//	TargetPinned=false            服务端自己发起的取消（如「立即发送」挤掉当前轮），
	//	                              不校验，作用于此刻活跃的那一轮。
	//	TargetPinned=true, ID=非 0    只作用于 id 相同的那一轮；对不上就丢弃。
	//	TargetPinned=true, ID=0       决定取消时根本没有在跑的一轮 → 直接丢弃，
	//	                              不能落到之后起来的新轮上。
	TargetTurnID uint64
	TargetPinned bool
}

type SuggestSessionNameInput struct {
	RootID       string
	SessionKey   string
	Agent        string
	Model        string
	FirstMessage string
}

func calculateSwitchReadLines(total, lastCtxSeq int) int {
	delta := total - lastCtxSeq
	if delta < 0 {
		return 0
	}
	if delta > switchContextTailLines {
		return switchContextTailLines
	}
	return delta
}

// switchReadHintPath 一律返回绝对路径。
//
// 早先项目内 meta（非 MetaLocationHome）会走 filepath.Rel 产出相对 runtime root 的
// 路径（worktree 下是 ../../.mindfs/...）。但提示文案里没有说明基准目录，agent 只能
// 猜：猜成 home 就变成 ~/.mindfs/sessions/...，读不到文件，误判成「历史丢失」而停摆。
// Rel 失败时还会回退到无基准的 .mindfs/... —— 同一段文案出现三种路径形态。
// 绝对路径把基准歧义整个消掉，worktree 与否不再影响结果，runtimeRootAbs 参数随之作废。
func switchReadHintPath(manager *session.Manager, sessionKey string) string {
	if manager == nil {
		return ""
	}
	absLogPath := manager.ExchangeLogAbsolutePath(sessionKey)
	if strings.TrimSpace(absLogPath) == "" {
		return manager.ExchangeLogPath(sessionKey)
	}
	return filepath.ToSlash(absLogPath)
}

func buildSwitchReadHint(exchangeLogPath string, lines int) string {
	return "This session was migrated from elsewhere. Your context may lag behind this session;\n" +
		"Before replying, read the last " + strconv.Itoa(lines) + " lines from " + exchangeLogPath + " to recover context.\n" +
		"If you still need more context, decide and read older history yourself.\n" +
		"When continuing to read, keep each backward batch to about " + strconv.Itoa(switchContextTailLines) + " lines.\n\n" +
		"Execution order: read history first, then compose the final answer.\n" +
		"Note: do not send any natural-language response before finishing the required history reads. Start reading immediately via tools/commands.\n" +
		"Only if reading fails, output a brief error and stop.\n\n"
}

func (s *Service) CancelSessionTurn(ctx context.Context, in CancelSessionTurnInput) error {
	if err := s.ensureRegistry(); err != nil {
		return err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		return errors.New("session key required")
	}
	if strings.HasPrefix(key, "transient-") {
		active := getActiveTurn(in.RootID, key)
		if active == nil || staleCancelTarget(in, active) {
			return nil
		}
		active.cancel()
		if active.session != nil {
			if err := active.session.CancelCurrentTurn(); err != nil {
				log.Printf("[session] turn.cancel.error root=%s session=%s err=%v", in.RootID, key, err)
			}
		}
		return nil
	}
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		return err
	}
	active := getActiveTurn(in.RootID, current.Key)
	if active == nil || staleCancelTarget(in, active) {
		return nil
	}
	if active.session != nil {
		// Let the runtime emit its own turn boundary after interrupt. Canceling
		// turnCtx first can dequeue the current waiter, so a late ResultMessage
		// may be delivered to the next queued turn.
		if err := active.session.CancelCurrentTurn(); err != nil {
			log.Printf("[session] turn.cancel.error root=%s session=%s err=%v", in.RootID, current.Key, err)
			active.cancel()
			return err
		}
		return nil
	}
	active.cancel()
	return nil
}
