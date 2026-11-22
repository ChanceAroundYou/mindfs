package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	RootID              string
	RuntimeRootPath     string
	Key                 string
	Agent               string
	Model               string
	Mode                string
	Effort              string
	FastService         string
	PlanMode            *bool
	Shell               string
	TerminalCols        int
	Content             string
	UserTimestamp       time.Time
	ClientCtx           ClientContext
	OnStart             func(MessageStart)
	OnUpdate            func(agenttypes.Event)
	OnSubSessionCreated func(*session.Session)
	OnSubSessionUpdate  func(sessionKey string, update agenttypes.Event)
	// OnSubAgentAttached / OnSubAgentTurnFinished 是子会话回合身份的收发两端，由
	// attach() 与 close() 配对调用。详见 subagentSessionInput 上的同名字段。
	OnSubAgentAttached     func(child *session.Session, turnID string)
	OnSubAgentTurnFinished func(sessionKey, turnID string)
	OnAgentDefaultsChanged func(agentName string)
	// Router 会话专用（TypeRouter）。普通会话全部留空：
	// DeveloperInstructions 每轮都要带（resume 也带），ConfigDir 隔离用户配置，
	// Tools/ToolPolicy 是进程内 MCP 与 deny-by-default 闸门。
	DeveloperInstructions string
	ConfigDir             string
	Tools                 []agenttypes.ToolSpec
	ToolPolicy            *agenttypes.ToolPolicy
	// hygieneCompact 标记这是会话卫生自动触发的压缩回合：它不参与观测，也不
	// 再触发下一轮自动压缩。只有 Usecase 内部的 scheduleHygieneAutoCompact 会置位。
	hygieneCompact bool
	// onCompactSeen 在本回合观察到 harness 压缩时回调一次。自动触发用它区分
	// 「压缩真的发生了」与「命令被当普通消息发出去了」——后者必须按失败处理，
	// 否则会每轮重发。
	onCompactSeen func()
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

// switchReadTarget 是提示里列出的一个候选历史文件。
type switchReadTarget struct {
	Path  string
	Lines int
	Kind  string // "blocks"（正文）| "log"（交换元数据）
}

// switchReadHintTargets 决定提示让 agent 去读哪些文件、各读多少行。
//
// 主日志自 S2d-2（G-BH）起 content 恒空，正文只在块流行里 —— 把「读最后 N 行」
// 指向主日志，agent 拿到的是一串没有内容的行，恢复不出任何上下文（2026-11-12 fn
// 实测：43 行交换行全是空 content，正文 596 个块另存）。所以块流可用时优先块流，
// 行数换算成「未见过的块行数」；主日志只作为第二个候选一并列出。
//
// 只列出真实存在的文件。一个都不存在时返回空列表，由提示自己兜底：让 agent 去读
// 自己的会话记录或直接用现有上下文继续，而不是让本轮停摆（2026-11-19 fn 实测：
// 压缩后残留的失效路径让 agent 直接停下）。
//
// seen 是 agent 已看到的交换行数，exchangeLines 是主日志口径的读取量。
func switchReadHintTargets(manager *session.Manager, sessionKey string, exchanges []session.Exchange, seen, exchangeLines int) []switchReadTarget {
	if manager == nil {
		return nil
	}
	var targets []switchReadTarget
	if blocksPath := manager.BlocksLogAbsolutePath(sessionKey); strings.TrimSpace(blocksPath) != "" {
		if unseen, err := manager.CountBlocksAfterSeq(sessionKey, lastSeenExchangeSeq(exchanges, seen)); err == nil && unseen > 0 {
			if unseen > switchContextTailBlocks {
				unseen = switchContextTailBlocks
			}
			targets = append(targets, switchReadTarget{Path: filepath.ToSlash(blocksPath), Lines: unseen, Kind: "blocks"})
		}
	}
	if logPath := switchReadHintPath(manager, sessionKey); strings.TrimSpace(logPath) != "" && exchangeLines > 0 {
		targets = append(targets, switchReadTarget{Path: filepath.ToSlash(logPath), Lines: exchangeLines, Kind: "log"})
	}
	readable := targets[:0]
	for _, target := range targets {
		if _, err := os.Stat(filepath.FromSlash(target.Path)); err == nil {
			readable = append(readable, target)
		}
	}
	return readable
}

// lastSeenExchangeSeq 取 agent 最后看过的那条交换行的 seq。
//
// 老数据可能没有 seq（零值），此时按「seq == 行序」的等价关系用 seen 兜底；
// 换算只是给读取量一个量级，偏差一两行不影响 agent 恢复上下文。
func lastSeenExchangeSeq(exchanges []session.Exchange, seen int) int {
	if seen <= 0 || len(exchanges) == 0 {
		return 0
	}
	idx := seen - 1
	if idx >= len(exchanges) {
		idx = len(exchanges) - 1
	}
	if seq := exchanges[idx].Seq; seq > 0 {
		return seq
	}
	return seen
}

const (
	// switchHintProbe 是迁移提示的稳定指纹：导入/标题剥离都按它判断是不是提示。
	switchHintProbe = "This session was migrated from elsewhere."
	// switchHintPrefix 是提示的第一行，必须与 switchHintProbe 同源。
	switchHintPrefix = switchHintProbe + " Your context may lag behind this session."
	// switchHintTail 是提示的最后一行；剥离逻辑切开它之后的内容。
	switchHintTail = "Never stop just because a history file is missing; continue with the request and state any assumptions you had to make."
	// legacySwitchHintTail 是旧提示的末句，仅用于剥离历史里已经落盘的老文案。
	legacySwitchHintTail = "Only if reading fails, output a brief error and stop."
)

// buildSwitchReadHint 生成迁移提示。
//
// 它是**可选的补上下文线索**，不是硬性要求：列出的路径可能因为压缩后残留、
// 账户/项目 meta 迁移、文件被清理等原因失效，所以文案明确「跳过不存在的文件」，
// 并在全部读不到时让 agent 去读自己的会话记录或直接用现有上下文继续，绝不喊停。
func buildSwitchReadHint(targets []switchReadTarget) string {
	var b strings.Builder
	b.WriteString(switchHintPrefix)
	b.WriteString("\nThese notes are optional hints for catching up; they are not a blocking requirement.\n")
	if len(targets) == 0 {
		b.WriteString("No MindFS-side history file could be located for this session.\n")
	} else {
		b.WriteString("Candidate history files, newest lines first — try them in order and skip any that do not exist:\n")
		for i, target := range targets {
			label := "MindFS exchange log; turn metadata, text may be empty on newer sessions"
			if target.Kind == "blocks" {
				label = "MindFS block stream; carries the full message text"
			}
			fmt.Fprintf(&b, "%d. %s (%s); read about the last %d lines.\n", i+1, target.Path, label, target.Lines)
		}
	}
	b.WriteString("If you cannot find or read the history, read the session record kept by your own agent runtime (Codex keeps transcripts under ~/.codex/sessions; Claude Code under ~/.claude/projects), or simply continue with the context you already have.\n")
	b.WriteString(switchHintTail)
	b.WriteString("\n\n")
	return b.String()
}

// StripSwitchHint 去掉开头这段迁移提示（若存在）。
//
// 同时认新文案与旧文案：历史里落盘的旧提示仍会出现在导入会话的首条气泡里。
func StripSwitchHint(text string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(text), "\\n", "\n")
	if !strings.HasPrefix(normalized, switchHintProbe) {
		return text
	}
	end := -1
	for _, tail := range []string{switchHintTail, legacySwitchHintTail} {
		if idx := strings.Index(normalized, tail); idx >= 0 {
			if next := idx + len(tail); end < 0 || next < end {
				end = next
			}
		}
	}
	if end < 0 {
		return text
	}
	return strings.TrimSpace(normalized[end:])
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
