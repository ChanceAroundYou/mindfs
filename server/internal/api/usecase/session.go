package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/commandexec"
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

type RunTransientSlashCommandInput struct {
	RootID      string
	Key         string
	Agent       string
	Model       string
	Mode        string
	Effort      string
	FastService string
	Command     string
	OnUpdate    func(agenttypes.Event)
}

type codexDeviceCodeLoginSession interface {
	LoginChatGPTDeviceCode(context.Context) error
}

var activeSubagentSubscriptions sync.Map

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

const (
	switchContextTailLines   = 20
	sessionNameTimeout       = 30 * time.Second
	sessionNameMinMessageLen = 12
	sessionRecoveryAttempts  = 3
	sessionRecoveryDelay     = 30 * time.Second
)

type SuggestSessionNameInput struct {
	RootID       string
	SessionKey   string
	Agent        string
	Model        string
	FirstMessage string
}

var (
	sessionSendLocksMu sync.Mutex
	sessionSendLocks   = make(map[string]*sync.Mutex)
	activeTurnsMu      sync.Mutex
	activeTurns        = make(map[string]*activeTurnState)
	activeTurnSeq      uint64
)

type activeTurnState struct {
	// id 标识「具体是哪一轮」。取消是异步落地的（ws.go 异步派发，因为 Interrupt
	// 可能阻塞），从用户点到执行可以隔上几百毫秒；这期间同一会话可能已经开了新的
	// 一轮（用户点完停止紧接着发消息）。调用方在收到请求的那一刻就记下当时的 id，
	// 落地时 id 对不上就说明它要停的那一轮已经结束了 —— 直接放弃，别误伤新轮。
	id      uint64
	cancel  context.CancelFunc
	session agenttypes.Session
}

func getSessionSendLock(sessionKey string) *sync.Mutex {
	sessionSendLocksMu.Lock()
	defer sessionSendLocksMu.Unlock()
	lock := sessionSendLocks[sessionKey]
	if lock == nil {
		lock = &sync.Mutex{}
		sessionSendLocks[sessionKey] = lock
	}
	return lock
}

func activeTurnKey(rootID, sessionKey string) string {
	return rootID + "::" + sessionKey
}

func registerActiveTurn(rootID, sessionKey string, cancel context.CancelFunc) {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(sessionKey) == "" || cancel == nil {
		return
	}
	activeTurnsMu.Lock()
	activeTurnSeq++
	activeTurns[activeTurnKey(rootID, sessionKey)] = &activeTurnState{id: activeTurnSeq, cancel: cancel}
	activeTurnsMu.Unlock()
}

// ActiveTurnID 返回此刻在跑的那一轮的 id，没有在跑的一轮时返回 0。
//
// 调用方必须在**决定取消的那一刻**同步取证（ws.go 的读循环里），再拿着这个 id
// 去做异步的取消：取消落地时 id 不一致就说明目标轮次已经结束，必须放弃。
func ActiveTurnID(rootID, sessionKey string) uint64 {
	activeTurnsMu.Lock()
	defer activeTurnsMu.Unlock()
	if state := activeTurns[activeTurnKey(rootID, sessionKey)]; state != nil {
		return state.id
	}
	return 0
}

func setActiveTurnSession(rootID, sessionKey string, sess agenttypes.Session) {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(sessionKey) == "" || sess == nil {
		return
	}
	activeTurnsMu.Lock()
	state := activeTurns[activeTurnKey(rootID, sessionKey)]
	if state != nil {
		state.session = sess
	}
	activeTurnsMu.Unlock()
}

func unregisterActiveTurn(rootID, sessionKey string) {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(sessionKey) == "" {
		return
	}
	activeTurnsMu.Lock()
	delete(activeTurns, activeTurnKey(rootID, sessionKey))
	activeTurnsMu.Unlock()
}

func getActiveTurn(rootID, sessionKey string) *activeTurnState {
	activeTurnsMu.Lock()
	defer activeTurnsMu.Unlock()
	return activeTurns[activeTurnKey(rootID, sessionKey)]
}

func agentPoolSessionKey(sessionKey, agentName string) string {
	trimmedSessionKey := strings.TrimSpace(sessionKey)
	if trimmedSessionKey == "" {
		return ""
	}
	trimmedAgent := strings.TrimSpace(agentName)
	if trimmedAgent == "" {
		return trimmedSessionKey
	}
	return strings.ToLower(trimmedAgent) + "-" + trimmedSessionKey
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

func isCanceledTurnError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "context canceled") ||
		strings.Contains(value, "context cancelled") ||
		strings.Contains(value, "turn canceled") ||
		strings.Contains(value, "turn cancelled") ||
		strings.Contains(value, "cancelled")
}

func isNonRecoverableAgentError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	if value == "" {
		return false
	}
	needles := []string{
		"429",
		"too many requests",
		"exceeded retry limit",
		"rate limit",
		"ratelimit",
		"usage limit",
		"usagelimitexceeded",
		"remote compaction failed",
		"compact_remote",
		"responsetoomanyfailedattempts",
	}
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func cancelRuntimeAfterNonRecoverableError(sess agenttypes.Session, pool *agent.Pool, agentName string, cause error) {
	agentName = strings.TrimSpace(agentName)
	if sess != nil {
		if err := sess.CancelCurrentTurn(); err != nil {
			log.Printf("[session] turn.cancel_after_non_recoverable.error agent=%s cause=%v err=%v", agentName, cause, err)
		}
	}
	if pool != nil && agentName != "" {
		if _, ok := pool.KillAgentProcess(agentName, 0); ok {
			log.Printf("[session] runtime.kill_after_non_recoverable.done agent=%s cause=%v", agentName, cause)
			return
		}
	}
	if sess != nil {
		if err := sess.Close(); err != nil {
			log.Printf("[session] runtime.close_after_non_recoverable.error agent=%s cause=%v err=%v", agentName, cause, err)
		}
	}
}

func (s *Service) ensureAgentSession(
	ctx context.Context,
	pool *agent.Pool,
	manager *session.Manager,
	current *session.Session,
	agentName string,
	model string,
	mode string,
	effort string,
	fastService string,
	rootAbs string,
	developerInstructions string,
) (agenttypes.Session, *int, error) {
	poolSessionKey := agentPoolSessionKey(current.Key, agentName)
	nextModel := resolveRuntimeModel(current, nil, model)
	nextMode := resolveRuntimeMode(current, mode)
	nextEffort := resolveRuntimeEffort(agentName, current, effort)
	nextFastService := resolveRuntimeFastService(agentName, current, fastService)
	nextPlanMode := current != nil && current.PlanMode
	var binding *session.AgentBinding
	if manager != nil {
		var err error
		binding, err = manager.FindAgentBinding(ctx, current.Key, agentName)
		if err != nil {
			return nil, nil, err
		}
	}
	currentMode := ""
	currentEffort := ""
	currentFastService := ""
	currentPlanMode := false
	if current != nil {
		currentMode = resolveSessionExchangeMode(current)
		currentEffort = session.InferEffortFromSession(current)
		currentFastService = inferFastServiceFromSession(current)
		currentPlanMode = current.PlanMode
	}
	if existing, ok := pool.Get(poolSessionKey); ok {
		currentModel := currentModelForRuntime(current, existing)
		if !shouldReopenSessionForSetting(pool, agentName, currentEffort, nextEffort) &&
			currentFastService == nextFastService {
			if current != nil && currentModel != nextModel {
				log.Printf("[session/model] switch.detected session=%s agent=%s from=%q to=%q action=set_runtime_model", current.Key, agentName, currentModel, nextModel)
				if err := existing.SetModel(ctx, nextModel); err != nil {
					if prober := s.Registry.GetProber(); prober != nil {
						prober.ReportRuntimeFailure(agentName, err)
					}
					log.Printf("[session/model] switch.error session=%s agent=%s model=%q pool_session=%s err=%v", current.Key, agentName, nextModel, poolSessionKey, err)
					return nil, nil, err
				}
				log.Printf("[session/model] switch.done session=%s agent=%s model=%q pool_session=%s", current.Key, agentName, nextModel, poolSessionKey)
			}
			if current != nil && currentMode != nextMode {
				log.Printf("[session/mode] switch.detected session=%s agent=%s from=%q to=%q action=set_runtime_mode", current.Key, agentName, currentMode, nextMode)
				if err := existing.SetMode(ctx, nextMode); err != nil {
					if prober := s.Registry.GetProber(); prober != nil {
						prober.ReportRuntimeFailure(agentName, err)
					}
					log.Printf("[session/mode] switch.error session=%s agent=%s mode=%q pool_session=%s err=%v", current.Key, agentName, nextMode, poolSessionKey, err)
					return nil, nil, err
				}
				log.Printf("[session/mode] switch.done session=%s agent=%s mode=%q pool_session=%s", current.Key, agentName, nextMode, poolSessionKey)
			}
			if current != nil && currentPlanMode != nextPlanMode {
				log.Printf("[session/plan] switch.detected session=%s agent=%s from=%t to=%t action=set_plan_mode", current.Key, agentName, currentPlanMode, nextPlanMode)
				if err := existing.SetPlanMode(ctx, nextPlanMode); err != nil {
					if prober := s.Registry.GetProber(); prober != nil {
						prober.ReportRuntimeFailure(agentName, err)
					}
					log.Printf("[session/plan] switch.error session=%s agent=%s plan_mode=%t pool_session=%s err=%v", current.Key, agentName, nextPlanMode, poolSessionKey, err)
					return nil, nil, err
				}
				log.Printf("[session/plan] switch.done session=%s agent=%s plan_mode=%t pool_session=%s", current.Key, agentName, nextPlanMode, poolSessionKey)
			}
			var currentSeq *int
			if current != nil {
				last := current.AgentCtxSeq[agentName]
				currentSeq = &last
			}
			return existing, currentSeq, nil
		}
		log.Printf("[session/settings] reopen.detected session=%s agent=%s effort_from=%q effort_to=%q fast_service_from=%q fast_service_to=%q action=resume_runtime_session", current.Key, agentName, currentEffort, nextEffort, currentFastService, nextFastService)
		pool.Close(poolSessionKey)
	}

	openCtx := pool.Context()
	if openCtx == nil {
		openCtx = ctx
	}

	openInput := agenttypes.OpenSessionInput{
		SessionKey:            poolSessionKey,
		AgentName:             agentName,
		Model:                 nextModel,
		Mode:                  nextMode,
		Effort:                nextEffort,
		FastService:           nextFastService,
		PlanMode:              nextPlanMode,
		RootPath:              rootAbs,
		DeveloperInstructions: developerInstructions,
		AgentSessionID: strings.TrimSpace(func() string {
			if binding == nil {
				return ""
			}
			return binding.AgentSessionID
		}()),
		AgentCtxSeq: func() int {
			if binding == nil {
				return 0
			}
			return binding.AgentCtxSeq
		}(),
	}
	if openInput.AgentSessionID != "" {
		log.Printf("[session/model] open session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s action=resume_runtime_session agent_session_id=%s agent_ctx_seq=%d", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey, openInput.AgentSessionID, openInput.AgentCtxSeq)
	} else {
		log.Printf("[session/model] open session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s action=open_new_runtime_session", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey)
	}
	sess, err := pool.GetOrCreate(openCtx, openInput)
	var ctxSeqOverride *int
	if err != nil {
		if openInput.AgentSessionID != "" {
			log.Printf("[session/model] resume.error session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s agent_session_id=%s err=%v fallback=open_new_runtime_session", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey, openInput.AgentSessionID, err)
			openInput.AgentSessionID = ""
			openInput.AgentCtxSeq = 0
			sess, err = pool.GetOrCreate(openCtx, openInput)
			if err == nil {
				zero := 0
				ctxSeqOverride = &zero
			}
		}
	}
	if err != nil {
		if prober := s.Registry.GetProber(); prober != nil {
			prober.ReportRuntimeFailure(agentName, err)
		}
		log.Printf("[session/model] open.error session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s err=%v", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey, err)
		return nil, nil, err
	}
	if ctxSeqOverride == nil && binding != nil && openInput.AgentSessionID != "" {
		last := binding.AgentCtxSeq
		ctxSeqOverride = &last
	}
	log.Printf("[session/model] open.done session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey)
	return sess, ctxSeqOverride, nil
}

func (s *Service) RunTransientSlashCommand(ctx context.Context, in RunTransientSlashCommandInput) error {
	if err := s.ensureRegistry(); err != nil {
		return err
	}
	command := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(in.Command)), "/")
	agentName := strings.TrimSpace(in.Agent)
	if agentName != "codex" || (command != "status" && command != "login") {
		return errors.New("unsupported transient slash command")
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		key = fmt.Sprintf("transient-%s-%d", command, time.Now().UnixNano())
	}
	sendLock := getSessionSendLock(key)
	sendLock.Lock()
	defer sendLock.Unlock()

	turnCtx, turnCancel := context.WithCancel(ctx)
	registerActiveTurn(in.RootID, key, turnCancel)
	defer unregisterActiveTurn(in.RootID, key)

	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return err
	}
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		current = &session.Session{
			Key:         key,
			Type:        session.TypeChat,
			Model:       strings.TrimSpace(in.Model),
			AgentCtxSeq: map[string]int{},
			Name:        "Transient slash command",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
	} else {
		if current.Type == session.TypeCommand {
			return errors.New("slash commands are not supported for command sessions")
		}
		if currentAgent := strings.TrimSpace(session.InferAgentFromSession(current)); currentAgent != "" && currentAgent != agentName {
			return errors.New("slash command agent does not match session agent")
		}
	}
	if err := s.validateAgentModel(agentName, in.Model); err != nil {
		return err
	}
	agentPool := s.Registry.GetAgentPool()
	if agentPool == nil {
		return errors.New("agent pool not configured")
	}
	root := manager.Root()
	rootAbs, _ := root.RootDir()
	sess, _, err := s.ensureAgentSession(turnCtx, agentPool, manager, current, agentName, in.Model, in.Mode, in.Effort, in.FastService, rootAbs, "")
	if err != nil {
		return err
	}
	setActiveTurnSession(in.RootID, current.Key, sess)

	sess.OnUpdate(func(update agenttypes.Event) {
		update = normalizeAgentUpdatePaths(root, update)
		clientUpdate := compactAgentUpdate(update)
		if in.OnUpdate != nil {
			in.OnUpdate(clientUpdate)
		}
	})

	if command == "login" {
		loginSess, ok := sess.(codexDeviceCodeLoginSession)
		if !ok {
			return errors.New("codex login is not supported by this runtime")
		}
		err = loginSess.LoginChatGPTDeviceCode(turnCtx)
	} else {
		finishUse := agentPool.BeginSessionUse(agentPoolSessionKey(current.Key, agentName))
		defer finishUse()
		err = sess.SendMessage(turnCtx, "/"+command)
	}
	if err != nil && !isCanceledTurnError(err) {
		if prober := s.Registry.GetProber(); prober != nil {
			prober.ReportRuntimeFailure(agentName, err)
		}
		return err
	}
	return err
}

type subagentSessionInput struct {
	RootID      string
	Parent      *session.Session
	Agent       string
	Model       string
	Mode        string
	Effort      string
	FastService string
	RootAbs     string
	Pool        *agent.Pool
	Manager     *session.Manager
	ToolCall    agenttypes.ToolCall
	OnCreated   func(*session.Session)
	OnUpdate    func(sessionKey string, update agenttypes.Event)
}

type claudeSubagentRouter struct {
	in       subagentSessionInput
	byRef    map[string]*claudeSyntheticSubagent
	children []*claudeSyntheticSubagent
}

type claudeSyntheticSubagent struct {
	key     string
	child   *session.Session
	runtime *syntheticAgentSession
	done    func()
	closed  bool
}

func (s *syntheticAgentSession) SendMessage(context.Context, string) error { return nil }
func (s *syntheticAgentSession) AnswerQuestion(context.Context, agenttypes.AskUserAnswer) error {
	return nil
}
func (s *syntheticAgentSession) CurrentModel() string                   { return "" }
func (s *syntheticAgentSession) SetModel(context.Context, string) error { return nil }
func (s *syntheticAgentSession) ListModels(context.Context) (agenttypes.ModelList, error) {
	return agenttypes.ModelList{}, nil
}
func (s *syntheticAgentSession) SetMode(context.Context, string) error   { return nil }
func (s *syntheticAgentSession) SetPlanMode(context.Context, bool) error { return nil }
func (s *syntheticAgentSession) ListModes(context.Context) (agenttypes.ModeList, error) {
	return agenttypes.ModeList{}, nil
}
func (s *syntheticAgentSession) ListCommands(context.Context) (agenttypes.CommandList, error) {
	return agenttypes.CommandList{}, nil
}
func (s *syntheticAgentSession) CancelCurrentTurn() error { return nil }
func (s *syntheticAgentSession) OnUpdate(onUpdate func(agenttypes.Event)) {
	s.onUpdate = onUpdate
}
func (s *syntheticAgentSession) SessionID() string { return s.id }

func (s *syntheticAgentSession) Close() error { return nil }
func (s *syntheticAgentSession) emit(event agenttypes.Event) {
	if s.onUpdate != nil {
		s.onUpdate(event)
	}
}

func (s *Service) ensureSubagentSessions(ctx context.Context, in subagentSessionInput) {
	if in.Parent == nil || in.Pool == nil || in.Manager == nil || in.ToolCall.RawType != "collabToolCall" {
		return
	}
	for _, receiverThreadID := range stringSliceMeta(in.ToolCall.Meta, "receiverThreadIds") {
		receiverThreadID = strings.TrimSpace(receiverThreadID)
		if receiverThreadID == "" {
			continue
		}
		child, err := s.ensureSubagentSession(ctx, in, receiverThreadID)
		if err != nil {
			log.Printf("[subagent] session.ensure.error root=%s parent=%s receiver=%s err=%v", in.RootID, in.Parent.Key, receiverThreadID, err)
			continue
		}
		if child != nil {
			s.startSubagentSubscription(in, child, receiverThreadID)
		}
	}
}

func (s *Service) ensureSubagentSession(ctx context.Context, in subagentSessionInput, receiverThreadID string) (*session.Session, error) {
	if existing, err := in.Manager.FindAgentBindingByAgentSession(ctx, in.Agent, receiverThreadID); err != nil {
		return nil, err
	} else if existing != nil {
		return in.Manager.Get(ctx, existing.SessionKey, 0)
	}
	child, err := in.Manager.Create(ctx, session.CreateInput{
		Type:             session.TypeChat,
		ParentSessionKey: in.Parent.Key,
		ParentToolCallID: in.ToolCall.CallID,
		Agent:            in.Agent,
		Model:            firstNonEmptyString(stringMeta(in.ToolCall.Meta, "model"), in.Model),
		PlanMode:         false,
		Name:             subagentSessionName(in.ToolCall, receiverThreadID),
	})
	if err != nil {
		return nil, err
	}
	if err := in.Manager.UpsertAgentBinding(ctx, session.AgentBinding{
		SessionKey:     child.Key,
		Agent:          in.Agent,
		AgentSessionID: receiverThreadID,
	}); err != nil {
		return nil, err
	}
	if in.OnCreated != nil {
		in.OnCreated(child)
	}
	return child, nil
}

func (s *Service) startSubagentSubscription(in subagentSessionInput, child *session.Session, receiverThreadID string) {
	key := in.RootID + ":" + child.Key + ":" + receiverThreadID
	if _, loaded := activeSubagentSubscriptions.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	go func() {
		defer activeSubagentSubscriptions.Delete(key)
		ctx, cancel := context.WithCancel(in.Pool.Context())
		registerActiveTurn(in.RootID, child.Key, cancel)
		defer unregisterActiveTurn(in.RootID, child.Key)
		runtime, err := in.Pool.GetOrCreate(ctx, agenttypes.OpenSessionInput{
			SessionKey:     agentPoolSessionKey(child.Key, in.Agent),
			AgentName:      in.Agent,
			Model:          firstNonEmptyString(stringMeta(in.ToolCall.Meta, "model"), in.Model),
			Mode:           in.Mode,
			Effort:         firstNonEmptyString(stringMeta(in.ToolCall.Meta, "reasoningEffort"), in.Effort),
			FastService:    in.FastService,
			PlanMode:       false,
			RootPath:       in.RootAbs,
			AgentSessionID: receiverThreadID,
			AgentCtxSeq:    child.AgentCtxSeq[in.Agent],
		})
		if err != nil {
			log.Printf("[subagent] subscription.open.error root=%s session=%s receiver=%s err=%v", in.RootID, child.Key, receiverThreadID, err)
			return
		}
		finishUse := in.Pool.BeginSessionUse(agentPoolSessionKey(child.Key, in.Agent))
		defer finishUse()
		setActiveTurnSession(in.RootID, child.Key, runtime)
		subscriber, ok := runtime.(agenttypes.ThreadEventSubscriber)
		if !ok {
			log.Printf("[subagent] subscription.unsupported root=%s session=%s receiver=%s", in.RootID, child.Key, receiverThreadID)
			return
		}
		markDone := attachBackgroundSessionUpdates(ctx, in, child, runtime)
		if err := subscriber.SubscribeThreadEvents(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[subagent] subscription.error root=%s session=%s receiver=%s err=%v", in.RootID, child.Key, receiverThreadID, err)
		}
		markDone()
	}()
}

func attachBackgroundSessionUpdates(ctx context.Context, in subagentSessionInput, child *session.Session, runtime agenttypes.Session) func() {
	var responseText string
	// 子会话是三条服务端发起的轮次路径之一（方案 C2）：它没有 user 行
	// （子会话的转录就是子代理自己的），所以 TurnSeq 留 0，只 mint mindfs_id。
	// 一个订阅 = 一条助手行 = 一轮；mindfs_id 在分配真实 seq 时 mint。
	auxBuffer := make([]session.ExchangeAux, 0, 8)
	var thoughtBuffer strings.Builder
	lastResponseUpdateType := ""
	var doneMu sync.Mutex
	doneSent := false
	currentThoughtID := ""
	flushThought := func() {
		thought := thoughtBuffer.String()
		if strings.TrimSpace(thought) == "" {
			thoughtBuffer.Reset()
			currentThoughtID = ""
			return
		}
		thoughtID := currentThoughtID
		thoughtBuffer.Reset()
		currentThoughtID = ""
		auxBuffer = append(auxBuffer, session.ExchangeAux{
			Line:      currentAssistantLine(responseText),
			Thought:   thought,
			ThoughtID: thoughtID,
		})
	}
	finish := func(emit bool) {
		doneMu.Lock()
		if doneSent {
			doneMu.Unlock()
			return
		}
		doneSent = true
		doneMu.Unlock()
		ctx = session.WithExchangeSource(ctx, session.ExchangeSourceLive)
		defer in.Manager.ClearPendingExchangeAux(context.Background(), child.Key)
		flushThought()
		var subTurnID string
		subAgentSeq, err := in.Manager.AddExchangeForAgentAtWithIdentityFactory(
			ctx, child, "agent", responseText, in.Agent, in.Mode, in.Effort, in.FastService, time.Time{},
			func(seq int) session.ExchangeIdentity {
				subTurnID = newMindFSID("", fmt.Sprintf("%s|agent|%d", child.Key, seq))
				return session.ExchangeIdentity{MindFSID: subTurnID}
			},
		)
		if err != nil {
			log.Printf("[subagent] persist.agent.error root=%s session=%s err=%v", in.RootID, child.Key, err)
			return
		}
		subAux := dedupeExchangeAuxBuffer(auxBuffer)
		for i := range subAux {
			subAux[i].Seq = subAgentSeq
			subAux[i] = hydratePendingToolCallAux(ctx, in.Manager, child.Key, subAux[i])
			if err := in.Manager.AddExchangeAux(ctx, child.Key, subAux[i]); err != nil {
				log.Printf("[subagent] persist.aux.error root=%s session=%s err=%v", in.RootID, child.Key, err)
				return
			}
		}
		// 双写块流（方案 S2a）。子代理块带 parentToolUseId / taskId / subagentType（D16）由
		// aux 的既有归属字段承载，S2c 接线；这里先保证正文不丢。
		if err := persistTurnBlocks(ctx, in.Manager, child.Key, subAgentSeq, subTurnID, responseText, subAux); err != nil {
			log.Printf("[subagent] persist.blocks.error root=%s session=%s seq=%d err=%v", in.RootID, child.Key, subAgentSeq, err)
		}
		if err := in.Manager.UpdateAgentState(ctx, child, in.Agent, contextLineCount(child.Exchanges), runtime.SessionID()); err != nil {
			log.Printf("[subagent] persist.agent_state.error root=%s session=%s err=%v", in.RootID, child.Key, err)
		}
		if emit && in.OnUpdate != nil {
			in.OnUpdate(child.Key, agenttypes.Event{Type: agenttypes.EventTypeMessageDone, Data: agenttypes.MessageDone{}})
		}
	}
	runtime.OnUpdate(func(update agenttypes.Event) {
		switch update.Type {
		case agenttypes.EventTypeThoughtChunk:
			if chunk, ok := update.Data.(agenttypes.ThoughtChunk); ok && chunk.Content != "" {
				if currentThoughtID == "" {
					currentThoughtID = "thought-" + randomHex(8)
				}
				chunk.ID = currentThoughtID
				update.Data = chunk
				thoughtBuffer.WriteString(chunk.Content)
			}
		case agenttypes.EventTypeToolCall, agenttypes.EventTypeToolUpdate, agenttypes.EventTypeTodoUpdate, agenttypes.EventTypePlanUpdate, agenttypes.EventTypeCompact, agenttypes.EventTypeMessageChunk, agenttypes.EventTypeMessageDone:
			flushThought()
		}
		clientUpdate := compactAgentUpdate(update)
		if update.Type == agenttypes.EventTypeToolCall || update.Type == agenttypes.EventTypeToolUpdate {
			if toolCall, ok := update.Data.(agenttypes.ToolCall); ok {
				if shouldPersistToolCallAux(toolCall) {
					toolCallCopy := toolCall
					auxBuffer = append(auxBuffer, session.ExchangeAux{
						Line:     currentAssistantLine(responseText),
						ToolCall: &toolCallCopy,
					})
					_ = in.Manager.UpsertPendingExchangeAux(context.Background(), child.Key, session.ExchangeAux{ToolCall: &toolCallCopy})
				}
			}
		}
		if update.Type == agenttypes.EventTypePlanUpdate {
			if plan, ok := update.Data.(agenttypes.PlanUpdate); ok && strings.TrimSpace(plan.Content) != "" {
				planCopy := plan
				auxBuffer = append(auxBuffer, session.ExchangeAux{
					Line: currentAssistantLine(responseText),
					Plan: &planCopy,
				})
			}
		}
		if update.Type == agenttypes.EventTypeTodoUpdate {
			if todo, ok := update.Data.(agenttypes.TodoUpdate); ok && len(todo.Items) > 0 {
				todoCopy := todo
				auxBuffer = append(auxBuffer, session.ExchangeAux{
					Line: currentAssistantLine(responseText),
					Todo: &todoCopy,
				})
			}
		}
		if update.Type == agenttypes.EventTypeCompact {
			if compact, ok := update.Data.(agenttypes.CompactNotice); ok {
				compactCopy := compact
				auxBuffer = append(auxBuffer, session.ExchangeAux{
					Line:    currentAssistantLine(responseText),
					Compact: &compactCopy,
				})
			}
		}
		if update.Type == agenttypes.EventTypeMessageChunk {
			if chunk, ok := update.Data.(agenttypes.MessageChunk); ok {
				responseText = appendResponseChunk(responseText, lastResponseUpdateType, chunk.Content)
				lastResponseUpdateType = string(update.Type)
			}
		} else if update.Type == agenttypes.EventTypeThoughtChunk ||
			update.Type == agenttypes.EventTypeToolCall ||
			update.Type == agenttypes.EventTypeToolUpdate ||
			update.Type == agenttypes.EventTypeTodoUpdate ||
			update.Type == agenttypes.EventTypePlanUpdate ||
			update.Type == agenttypes.EventTypeCompact {
			lastResponseUpdateType = string(update.Type)
		}
		if update.Type != agenttypes.EventTypeMessageDone {
			if in.OnUpdate != nil {
				in.OnUpdate(child.Key, clientUpdate)
			}
			return
		}
		finish(false)
		if in.OnUpdate != nil {
			in.OnUpdate(child.Key, clientUpdate)
		}
	})
	return func() { finish(true) }
}

func (s *Service) sendCommandMessage(ctx context.Context, in SendMessageInput, manager *session.Manager, current *session.Session, turnID string, userExchangeSeq int) error {
	if strings.TrimSpace(in.Content) == "" {
		return errors.New("command required")
	}
	root := manager.Root()
	rootAbs, err := root.RootDir()
	if err != nil {
		return err
	}
	callID := "cmd-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	startTool := agenttypes.ToolCall{
		CallID:  callID,
		Title:   in.Content,
		Status:  "running",
		Kind:    agenttypes.ToolKindExecute,
		RawType: "commandExecution",
		Meta: map[string]any{
			"source":       "userShell",
			"phase":        "start",
			"command":      in.Content,
			"cwd":          ".",
			"terminalCols": in.TerminalCols,
		},
	}
	if in.OnUpdate != nil {
		in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeToolCall, Data: startTool})
	}

	proc, err := commandexec.StartInSession(ctx, commandexec.Options{
		Command:      in.Content,
		Cwd:          rootAbs,
		Shells:       configuredShells(s.Registry),
		Shell:        in.Shell,
		RootID:       in.RootID,
		Session:      current.Key,
		TerminalCols: in.TerminalCols,
	})
	if err != nil {
		log.Printf("[command] start.error root=%s session=%s command=%q err=%v", in.RootID, current.Key, in.Content, err)
		final := startTool
		final.Status = "failed"
		final.Meta = cloneMeta(final.Meta)
		final.Meta["phase"] = "final"
		final.Meta["exitCode"] = -1
		final.Meta["error"] = err.Error()
		final.Content = []agenttypes.ToolCallContentItem{{Type: "text", Text: err.Error()}}
		if in.OnUpdate != nil {
			in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeToolUpdate, Data: final})
			in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeMessageDone, Data: agenttypes.MessageDone{}})
		}
		if persistErr := persistCommandTurn(ctx, manager, current, final, turnID, userExchangeSeq); persistErr != nil {
			return persistErr
		}
		return err
	}

	limiter := commandexec.NewOutputLimiter()
	ticker := time.NewTicker(limiter.FlushEvery())
	defer ticker.Stop()
	done := make(chan commandexec.Result, 1)
	go func() {
		done <- proc.Wait()
	}()

	var result commandexec.Result
	var haveResult bool
	cancelStarted := false
	outputCh := proc.Output()
	for !haveResult {
		select {
		case chunk, ok := <-outputCh:
			if !ok {
				outputCh = nil
				continue
			}
			limiter.Write(chunk)
		case <-ticker.C:
			flushCommandOutput(in.OnUpdate, startTool, limiter)
			ticker.Reset(limiter.FlushEvery())
		case result = <-done:
			haveResult = true
		case <-ctx.Done():
			if !cancelStarted {
				cancelStarted = true
				go stopCommandProcess(proc)
			}
		}
	}
	drainCommandOutput(proc.Output(), limiter, 250*time.Millisecond)
	flushCommandOutput(in.OnUpdate, startTool, limiter)

	final := startTool
	final.Status = "success"
	if cancelStarted || ctx.Err() != nil {
		final.Status = "cancelled"
	} else if result.ExitCode != 0 {
		final.Status = "failed"
	}
	tail := limiter.Tail()
	persistedBytes := limiter.TailBytes()
	outputBytes := limiter.TotalBytes()
	text := string(tail)
	if strings.TrimSpace(result.Shell) != "" {
		if err := manager.UpdateShell(context.Background(), current, result.Shell); err != nil {
			log.Printf("[command] shell.update.error root=%s session=%s shell=%q err=%v", in.RootID, current.Key, result.Shell, err)
		}
	}
	if outputBytes > persistedBytes {
		text = fmt.Sprintf("[output truncated: showing last %d bytes of %d bytes]\n%s", persistedBytes, outputBytes, text)
	}
	final.Meta = map[string]any{
		"source":         "userShell",
		"phase":          "final",
		"command":        in.Content,
		"cwd":            ".",
		"shell":          result.Shell,
		"exitCode":       result.ExitCode,
		"durationMs":     result.Duration.Milliseconds(),
		"outputBytes":    outputBytes,
		"persistedBytes": persistedBytes,
		"truncated":      outputBytes > persistedBytes,
		"truncation":     "tail",
		"terminalCols":   in.TerminalCols,
	}
	if cancelStarted || ctx.Err() != nil {
		final.Meta["cancelled"] = true
	}
	final.Content = []agenttypes.ToolCallContentItem{{Type: "text", Text: text}}
	if in.OnUpdate != nil {
		in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeToolUpdate, Data: final})
		in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeMessageDone, Data: agenttypes.MessageDone{}})
	}
	if err := persistCommandTurn(context.Background(), manager, current, final, turnID, userExchangeSeq); err != nil {
		log.Printf("[command] persist.error root=%s session=%s call=%s err=%v", in.RootID, current.Key, callID, err)
		return err
	}
	if final.Status == "success" || final.Status == "cancelled" {
		if err := UpsertCommandSuggestion(manager, CommandSuggestion{
			Command:        in.Content,
			Cwd:            ".",
			Shell:          result.Shell,
			RootID:         in.RootID,
			LastExitCode:   result.ExitCode,
			LastDurationMs: result.Duration.Milliseconds(),
			LastUsedAt:     result.FinishedAt,
		}); err != nil {
			log.Printf("[command/history] upsert.error root=%s session=%s err=%v", in.RootID, current.Key, err)
		}
	}
	return nil
}

func flushCommandOutput(onUpdate func(agenttypes.Event), base agenttypes.ToolCall, limiter *commandexec.OutputLimiter) {
	if onUpdate == nil || limiter == nil {
		return
	}
	chunk, ok := limiter.Flush()
	if !ok {
		return
	}
	update := base
	update.Status = "running"
	update.Meta = map[string]any{
		"source":       "userShell",
		"phase":        "stream",
		"outputMode":   "ring",
		"skippedBytes": chunk.SkippedBytes,
	}
	update.Content = []agenttypes.ToolCallContentItem{{Type: "text", Text: chunk.Text}}
	onUpdate(agenttypes.Event{Type: agenttypes.EventTypeToolUpdate, Data: update})
}

func drainCommandOutput(output <-chan []byte, limiter *commandexec.OutputLimiter, maxWait time.Duration) {
	if output == nil || limiter == nil {
		return
	}
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	for {
		select {
		case chunk, ok := <-output:
			if !ok {
				return
			}
			if len(chunk) > 0 {
				limiter.Write(chunk)
			}
		case <-timer.C:
			return
		}
	}
}

func stopCommandProcess(proc commandexec.Process) {
	if proc == nil {
		return
	}
	_ = proc.Interrupt()
	time.Sleep(2 * time.Second)
	_ = proc.Terminate()
	time.Sleep(3 * time.Second)
	_ = proc.KillTree()
}

func configuredShells(registry Registry) []commandexec.ShellSpec {
	if registry == nil {
		return nil
	}
	pool := registry.GetAgentPool()
	if pool == nil {
		return nil
	}
	cfg := pool.Config()
	shells := make([]commandexec.ShellSpec, 0, len(cfg.Shells))
	for _, shell := range cfg.Shells {
		shells = append(shells, commandexec.ShellSpec{
			Command:       shell.Command,
			Args:          append([]string(nil), shell.Args...),
			LongShellArgs: append([]string(nil), shell.LongShellArgs...),
			CommandPrefix: shell.CommandPrefix,
		})
	}
	return shells
}

// persistCommandTurn 只补助手行与其 aux：用户行已在 SendMessage 起手、命令真正执行前
// 落盘（与聊天会话同一条规则），这里再写一次会落重复。
//
// 助手行必须复用本轮 user 行 mint 的 mindfs_id（方案 C2）：命令会话是三条服务端发起的
// 轮次路径之一（另两条是 SendMessage 与子会话订阅），少了它命令轮的正文在 S2b 之后
// 挂不回轮次。助手正文为空，块的正文全部来自 aux（tool_call）。
func persistCommandTurn(ctx context.Context, manager *session.Manager, current *session.Session, final agenttypes.ToolCall, turnID string, userExchangeSeq int) error {
	ctx = session.WithExchangeSource(ctx, session.ExchangeSourceLive)
	ctx = session.WithExchangeIdentity(ctx, session.ExchangeIdentity{MindFSID: turnID, TurnSeq: userExchangeSeq})
	agentSeq, err := manager.AddExchangeForAgentAndGetSeq(ctx, current, "agent", "", "", "", "", "")
	if err != nil {
		return err
	}
	if agentSeq <= 0 {
		return errors.New("command agent exchange was not appended")
	}
	aux := session.ExchangeAux{
		Seq:      agentSeq,
		Line:     0,
		ToolCall: &final,
	}
	if err := manager.AddExchangeAux(ctx, current.Key, aux); err != nil {
		return err
	}
	if err := persistTurnBlocks(ctx, manager, current.Key, agentSeq, turnID, "", []session.ExchangeAux{aux}); err != nil {
		log.Printf("[command] persist.blocks.error session=%s seq=%d err=%v", current.Key, agentSeq, err)
	}
	return nil
}

func cloneMeta(meta map[string]any) map[string]any {
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	return out
}

type SendRecoveryInput struct {
	RootID             string
	SessionKey         string
	Manager            *session.Manager
	Current            *session.Session
	AgentName          string
	Model              string
	Mode               string
	Effort             string
	FastService        string
	PlanMode           bool
	RootAbs            string
	CurrentSession     agenttypes.Session
	Prompt             string
	SawAssistantChunk  bool
	SendWithAttachment func(agenttypes.Session, string) error
}

func (s *Service) recoverAgentTurn(ctx context.Context, in SendRecoveryInput) (agenttypes.Session, error) {
	if s == nil {
		return nil, errors.New("services not configured")
	}
	if in.Current == nil {
		return nil, errors.New("session required")
	}
	if in.Manager == nil {
		return nil, errors.New("session manager required")
	}
	if in.SendWithAttachment == nil {
		return nil, errors.New("send function required")
	}
	if in.CurrentSession == nil {
		return nil, errors.New("current session required")
	}

	var lastErr error
	for attempt := 1; attempt <= sessionRecoveryAttempts; attempt++ {
		if attempt > 1 {
			log.Printf("[session/recovery] wait root=%s session=%s agent=%s attempt=%d/%d delay=%s", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, sessionRecoveryDelay)
			if err := waitForRecoveryDelay(ctx, sessionRecoveryDelay); err != nil {
				return nil, err
			}
		}

		sess := in.CurrentSession
		recoveryMessage := in.Prompt
		recoveryAction := "resend_prompt"
		if in.SawAssistantChunk {
			recoveryMessage = "continue"
			recoveryAction = "continue"
		}
		log.Printf("[session/recovery] send.start root=%s session=%s agent=%s attempt=%d/%d action=%s", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, recoveryAction)
		if err := in.SendWithAttachment(sess, recoveryMessage); err != nil {
			if isCanceledTurnError(err) || ctx.Err() != nil {
				return nil, err
			}
			if isNonRecoverableAgentError(err) {
				log.Printf("[session/recovery] send.non_recoverable root=%s session=%s agent=%s attempt=%d/%d action=%s err=%v", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, recoveryAction, err)
				return nil, err
			}
			lastErr = err
			log.Printf("[session/recovery] send.failed root=%s session=%s agent=%s attempt=%d/%d action=%s err=%v", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, recoveryAction, err)
			continue
		}
		log.Printf("[session/recovery] send.done root=%s session=%s agent=%s attempt=%d/%d action=%s", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, recoveryAction)
		return sess, nil
	}
	if lastErr == nil {
		lastErr = errors.New("agent recovery failed")
	}
	return nil, lastErr
}

func waitForRecoveryDelay(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) AnswerQuestion(ctx context.Context, in AnswerQuestionInput) error {
	if err := s.ensureRegistry(); err != nil {
		return err
	}
	sessionKey := strings.TrimSpace(in.SessionKey)
	if sessionKey == "" {
		return errors.New("session key required")
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return err
	}
	agentName := strings.TrimSpace(in.Agent)
	if agentName == "" {
		current, err := manager.Get(ctx, sessionKey, 0)
		if err != nil {
			return err
		}
		agentName = strings.TrimSpace(session.InferAgentFromSession(current))
	}
	if agentName == "" {
		return errors.New("agent required")
	}
	pool := s.Registry.GetAgentPool()
	if pool == nil {
		return errors.New("agent pool unavailable")
	}
	sess, ok := pool.Get(agentPoolSessionKey(sessionKey, agentName))
	if !ok {
		return errors.New("agent session not found")
	}
	answer := agenttypes.AskUserAnswer{
		ToolUseID: strings.TrimSpace(in.ToolUseID),
		Answers:   in.Answers,
	}
	if err := manager.MarkPendingAskUserAnswered(ctx, sessionKey, answer.ToolUseID, answer.Answers, time.Now()); err != nil {
		return err
	}
	if err := sess.AnswerQuestion(ctx, answer); err != nil {
		return err
	}
	return nil
}

func currentAssistantLine(responseText string) int {
	// Anchor auxiliary events after the last text line, before trailing empty
	// lines that may be filled by subsequent chunks. Leave the response intact.
	responseText = strings.TrimRight(responseText, "\n")
	if responseText == "" {
		return 0
	}
	return strings.Count(responseText, "\n") + 1
}

func hydratePendingToolCallAux(ctx context.Context, manager *session.Manager, sessionKey string, aux session.ExchangeAux) session.ExchangeAux {
	if manager == nil || aux.ToolCall == nil || strings.TrimSpace(aux.ToolCall.CallID) == "" {
		return aux
	}
	if aux.ToolCall.Kind != agenttypes.ToolKindAskUser {
		return aux
	}
	latest, err := manager.GetFullToolCall(ctx, sessionKey, aux.ToolCall.CallID)
	if err != nil || latest == nil {
		return aux
	}
	if latest.Kind != agenttypes.ToolKindAskUser {
		return aux
	}
	toolCall := *latest
	aux.ToolCall = &toolCall
	return aux
}

func dedupeExchangeAuxBuffer(items []session.ExchangeAux) []session.ExchangeAux {
	if len(items) == 0 {
		return nil
	}
	seenToolCallIDs := make(map[string]int, len(items))
	seenPlanIDs := make(map[string]struct{}, len(items))
	seenCompactIDs := make(map[string]struct{}, len(items))
	seenTodo := false
	out := make([]session.ExchangeAux, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		callID := ""
		if item.ToolCall != nil {
			callID = strings.TrimSpace(item.ToolCall.CallID)
		}
		if callID != "" {
			if existingIndex, exists := seenToolCallIDs[callID]; exists {
				if out[existingIndex].ToolCall != nil {
					merged := mergeBufferedToolCall(*item.ToolCall, *out[existingIndex].ToolCall)
					out[existingIndex].ToolCall = &merged
				}
				continue
			}
			seenToolCallIDs[callID] = len(out)
		}
		isTodo := item.Todo != nil || (item.ToolCall != nil && item.ToolCall.Kind == agenttypes.ToolKindTodo)
		if isTodo {
			if seenTodo {
				continue
			}
			seenTodo = true
		}
		if item.Plan != nil {
			planID := strings.TrimSpace(item.Plan.ID)
			if planID != "" {
				if _, exists := seenPlanIDs[planID]; exists {
					continue
				}
				seenPlanIDs[planID] = struct{}{}
			}
		}
		if item.Compact != nil {
			compactID := strings.TrimSpace(item.Compact.ID)
			if compactID != "" {
				if _, exists := seenCompactIDs[compactID]; exists {
					continue
				}
				seenCompactIDs[compactID] = struct{}{}
			}
		}
		out = append(out, item)
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}

func shouldPersistToolCallAux(toolCall agenttypes.ToolCall) bool {
	if toolCall.RawType != "claude_task" {
		return true
	}
	return strings.TrimSpace(stringMeta(toolCall.Meta, "subtype")) != "task_progress"
}

func mergeBufferedToolCall(base, next agenttypes.ToolCall) agenttypes.ToolCall {
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
		if stringMeta(base.Meta, "taskTool") == "TaskCreate" && stringMeta(next.Meta, "taskTool") == "TaskUpdate" {
			merged.Meta["taskTool"] = "TaskCreate"
		}
	}
	return merged
}

func appendResponseChunk(responseText, lastResponseUpdateType, chunk string) string {
	if responseText != "" &&
		(lastResponseUpdateType == string(agenttypes.EventTypeThoughtChunk) ||
			lastResponseUpdateType == string(agenttypes.EventTypeToolCall) ||
			lastResponseUpdateType == string(agenttypes.EventTypeToolUpdate) ||
			lastResponseUpdateType == string(agenttypes.EventTypeTodoUpdate) ||
			lastResponseUpdateType == string(agenttypes.EventTypePlanUpdate) ||
			lastResponseUpdateType == string(agenttypes.EventTypeCompact)) &&
		!strings.HasSuffix(responseText, "\n\n") &&
		!strings.HasSuffix(responseText, "\n") {
		responseText += "\n\n"
	}
	return responseText + chunk
}

func randomHex(bytes int) string {
	if bytes <= 0 {
		return ""
	}
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func subagentSessionName(toolCall agenttypes.ToolCall, receiverThreadID string) string {
	if prompt := stringMeta(toolCall.Meta, "prompt"); prompt != "" {
		return truncateRunes(prompt, 48)
	}
	return truncateRunes(receiverThreadID, 16)
}

func stringMeta(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	value, _ := meta[key].(string)
	return strings.TrimSpace(value)
}

func stringSliceMeta(meta map[string]any, key string) []string {
	if meta == nil {
		return nil
	}
	switch value := meta[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if limit <= 0 || len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}

type pathNormalizer interface {
	NormalizePath(string) (string, error)
}

func normalizeAgentUpdatePaths(root pathNormalizer, update agenttypes.Event) agenttypes.Event {
	if root == nil {
		return update
	}
	toolCall, ok := update.Data.(agenttypes.ToolCall)
	if !ok {
		return update
	}

	for i := range toolCall.Locations {
		toolCall.Locations[i].Path = normalizeToolPath(root, toolCall.Locations[i].Path)
	}
	for i := range toolCall.Content {
		toolCall.Content[i].Path = normalizeToolPath(root, toolCall.Content[i].Path)
		if toolCall.Content[i].Type == "text" {
			toolCall.Content[i].Text = normalizeDiffTextPaths(root, toolCall.Content[i].Text)
		}
	}
	if toolCall.Meta != nil {
		if filePath, ok := toolCall.Meta["filePath"].(string); ok {
			toolCall.Meta["filePath"] = normalizeToolPath(root, filePath)
		}
		if path, ok := toolCall.Meta["path"].(string); ok {
			toolCall.Meta["path"] = normalizeToolPath(root, path)
		}
	}
	update.Data = toolCall
	return update
}

func compactAgentUpdate(update agenttypes.Event) agenttypes.Event {
	switch update.Type {
	case agenttypes.EventTypeToolCall, agenttypes.EventTypeToolUpdate:
		if toolCall, ok := update.Data.(agenttypes.ToolCall); ok {
			update.Data = session.CompactToolCall(toolCall)
		}
	}
	return update
}

func normalizeToolPath(root pathNormalizer, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	normalized, err := root.NormalizePath(path)
	if err != nil {
		return path
	}
	return normalized
}

func relatedFileRecordPath(managedRootAbs, runtimeRootAbs, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	slashPath := filepath.ToSlash(path)
	if strings.HasPrefix(slashPath, ".worktree/task-") {
		return filepath.Clean(filepath.Join(managedRootAbs, filepath.FromSlash(slashPath)))
	}
	runtimeRootAbs = strings.TrimSpace(runtimeRootAbs)
	managedRootAbs = strings.TrimSpace(managedRootAbs)
	if runtimeRootAbs != "" && managedRootAbs != "" && !sameManagedDirPath(runtimeRootAbs, managedRootAbs) {
		return filepath.Clean(filepath.Join(runtimeRootAbs, filepath.FromSlash(slashPath)))
	}
	return path
}

func normalizeDiffTextPaths(root pathNormalizer, text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	changed := false
	for i, line := range lines {
		next, ok := normalizeDiffLine(root, line)
		if !ok || next == line {
			continue
		}
		lines[i] = next
		changed = true
	}
	if !changed {
		return text
	}
	return strings.Join(lines, "\n")
}

func normalizeDiffLine(root pathNormalizer, line string) (string, bool) {
	switch {
	case strings.HasPrefix(line, "diff --git "):
		rest := strings.TrimPrefix(line, "diff --git ")
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) != 2 {
			return line, false
		}
		left, leftOK := normalizeDiffRef(root, parts[0])
		right, rightOK := normalizeDiffRef(root, parts[1])
		if !leftOK && !rightOK {
			return line, false
		}
		return "diff --git " + left + " " + right, true
	case strings.HasPrefix(line, "--- "):
		next, ok := normalizeDiffRef(root, strings.TrimPrefix(line, "--- "))
		if !ok {
			return line, false
		}
		return "--- " + next, true
	case strings.HasPrefix(line, "+++ "):
		next, ok := normalizeDiffRef(root, strings.TrimPrefix(line, "+++ "))
		if !ok {
			return line, false
		}
		return "+++ " + next, true
	default:
		return line, false
	}
}

func normalizeDiffRef(root pathNormalizer, ref string) (string, bool) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" || trimmed == "/dev/null" {
		return ref, false
	}

	prefix := ""
	path := trimmed
	switch {
	case strings.HasPrefix(trimmed, "a/"), strings.HasPrefix(trimmed, "b/"):
		prefix = trimmed[:2]
		path = trimmed[2:]
	}

	normalized := normalizeToolPath(root, path)
	if normalized == path || normalized == "" {
		return ref, false
	}
	return prefix + normalized, true
}

func isClaudeAliasModelForValidate(model string) bool {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return false
	}
	has := len(trimmed) >= 4 && strings.EqualFold(trimmed[len(trimmed)-4:], "[1m]")
	base := trimmed
	if has {
		base = strings.TrimSpace(trimmed[:len(trimmed)-4])
	}
	lower := strings.ToLower(strings.TrimSpace(base))
	switch lower {
	case "fable", "opus", "sonnet", "haiku", "of", "op", "os", "ok", "default":
		return true
	default:
		return false
	}
}

func canonicalClaudeModelForValidate(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}
	if strings.EqualFold(trimmed, "default") {
		return trimmed
	}
	if !isClaudeAliasModelForValidate(trimmed) {
		return trimmed
	}
	has := len(trimmed) >= 4 && strings.EqualFold(trimmed[len(trimmed)-4:], "[1m]")
	base := trimmed
	if has {
		base = strings.TrimSpace(trimmed[:len(trimmed)-4])
	}
	lower := strings.ToLower(strings.TrimSpace(base))
	switch lower {
	case "fable":
		lower = "of"
	case "opus":
		lower = "op"
	case "sonnet":
		lower = "os"
	case "haiku":
		lower = "ok"
	}
	if has {
		return lower + "[1m]"
	}
	return lower
}

func claudeModelBaseForValidate(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}
	if strings.EqualFold(trimmed, "default") {
		return "default"
	}
	has := len(trimmed) >= 4 && strings.EqualFold(trimmed[len(trimmed)-4:], "[1m]")
	base := trimmed
	if has {
		base = strings.TrimSpace(trimmed[:len(trimmed)-4])
	}
	if !isClaudeAliasModelForValidate(trimmed) {
		return strings.TrimSpace(base)
	}
	lower := strings.ToLower(strings.TrimSpace(base))
	switch lower {
	case "fable":
		lower = "of"
	case "opus":
		lower = "op"
	case "sonnet":
		lower = "os"
	case "haiku":
		lower = "ok"
	}
	return lower
}

func (s *Service) validateAgentModel(agentName, model string) error {
	agentName = strings.TrimSpace(agentName)
	model = strings.TrimSpace(model)
	if agentName == "" || model == "" || s.Registry == nil {
		return nil
	}
	isClaude := strings.EqualFold(agentName, "claude")
	if isClaude {
		model = canonicalClaudeModelForValidate(model)
	}
	prober := s.Registry.GetProber()
	if prober == nil {
		return nil
	}
	status, ok := prober.GetStatus(agentName)
	if !ok || len(status.Models) == 0 {
		return nil
	}
	for _, item := range status.Models {
		candidate := strings.TrimSpace(item.ID)
		if isClaude {
			candidate = canonicalClaudeModelForValidate(candidate)
		}
		if candidate == model {
			return nil
		}
		// 1M suffix is an explicit user toggle, not a separate model entry.
		// Prober advertises base ids without [1m] (both Anthropic aliases and generic provider ids like glm-*/deepseek-*),
		// so base and base[1m] are the same model.
		if isClaude && claudeModelBaseForValidate(candidate) != "" && claudeModelBaseForValidate(candidate) == claudeModelBaseForValidate(model) {
			return nil
		}
	}
	supported := make([]string, 0, len(status.Models))
	for i, item := range status.Models {
		if i >= 12 {
			break
		}
		if id := strings.TrimSpace(item.ID); id != "" {
			supported = append(supported, id)
		}
	}
	return fmt.Errorf("model %q is not supported by agent %q (supported models: %s)", model, agentName, strings.Join(supported, ", "))
}

// staleCancelTarget 判断一个取消请求是否已经过期：调用方点名要停的那一轮不是此刻
// 在跑的这一轮（含「当时根本没在跑」）。
func staleCancelTarget(in CancelSessionTurnInput, active *activeTurnState) bool {
	if !in.TargetPinned {
		return false
	}
	if active != nil && active.id == in.TargetTurnID && in.TargetTurnID != 0 {
		return false
	}
	log.Printf("[session] turn.cancel.stale target_turn=%d active_turn=%v action=drop",
		in.TargetTurnID, activeTurnIDOrNil(active))
	return true
}

func activeTurnIDOrNil(active *activeTurnState) any {
	if active == nil {
		return "none"
	}
	return active.id
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

// SessionTurnActive lets background orchestration defer while the user is chatting.
// SendMessage's existing per-session lock remains the final serialization guard.
func SessionTurnActive(rootID, sessionKey string) bool {
	return getActiveTurn(rootID, sessionKey) != nil
}
