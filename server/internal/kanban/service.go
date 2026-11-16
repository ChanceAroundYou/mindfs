package kanban

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/fs"
)

type RootProvider interface {
	GetRoot(rootID string) (fs.RootInfo, error)
	ListRoots() []fs.RootInfo
}

// workflowFinishLock 是一把带引用计数的 NodeRun 收尾锁。
//
// 锁表不能只增不删（mindfs 是长期进程，每个 NodeRun 留一个 mutex 会无限增长），
// 但也不能在仍有人持有/等待时直接删除条目：后来者会拿到另一把新 mutex，与旧锁
// 的持有者并发进入同一临界区。refs 同时覆盖持有者与等待者，归零才摘除。
type workflowFinishLock struct {
	mu   sync.Mutex
	refs int
}

type Service struct {
	Roots  RootProvider
	Runner Runner
	// WorkflowNodeRunnerPort 是 Workflow Agent 节点的执行端口（Task 2）。与
	// legacy Runner 并存：工作流节点只走这里，绝不合成 StageRun 双写。
	WorkflowNodeRunnerPort WorkflowNodeRunner
	// WorkflowCommentSenderPort sends queued comments to the bound session.
	WorkflowCommentSenderPort WorkflowCommentSender
	// WorkflowNodeCancellerPort cancels in-flight Agent turns with a bounded wait.
	WorkflowNodeCancellerPort WorkflowNodeCanceller
	// Workflows is the control-plane WorkflowStore (WP-05). Nil in legacy tests;
	// HTTP handlers reject with 503 when unavailable.
	Workflows *WorkflowStore
	// WriterGate 是 app 级共享的物理工作树写互斥（D15）。每账户的 Service 各有一个
	// 实例，但必须由 app 层注入**同一个** *WriterGate；为 nil 时写节点 fail closed。
	WriterGate *WriterGate

	mu     sync.Mutex
	stores map[string]*TaskStore
	// taskFinish 标记「正在收尾 worktree」的任务：同一任务同一时刻只允许一个收尾，
	// 否则两条收尾会对着同一棵 worktree 一起 merge / remove。
	taskFinish map[string]*finishToken
	// workflowDrives is the per-task single-flight driver registry. Keyed by
	// rootID+"\x00"+taskID; one goroutine per task, duplicate triggers set
	// pending so the same driver re-reads state once more instead of starting a
	// second concurrent turn.
	workflowDrives        map[string]*workflowDrive
	workflowCancellers    map[string]WorkflowNodeCanceller
	workflowCancelTimeout time.Duration
	// activityTurns 跟踪 ActivityService 派发的异步回合。收尾时先等回合退出、
	// 再等 workflow driver，避免回合在临时库被删除后继续唤醒 driver。
	activityTurns sync.WaitGroup
	// workflowFinishLocks 串行化「同一 NodeRun 的终态收尾」与「操作员取消」。
	// 合并/关闭工作树是不可回滚的 git 副作用：如果取消在 merge 之后、CAS 之前
	// 赢下状态，就会得到「树已合并删除、行记 cancelled」的矛盾。锁只在进程内
	// 有效，跨进程语义本来就未宣称 durable。
	workflowFinishLocks map[string]*workflowFinishLock
	// finishTurnDone 是「收尾回合跑完之后」的回调，由 api 层在装配时挂上。
	// 见 worktree_finish.go 的 notifyFinishTurnOutcome。
	finishTurnDone func(rootID, taskID string, succeeded bool)
	// sessionRunningProbe 报某个会话此刻是不是在回复中，由 api 层在装配时挂上
	// （StreamHub.IsSessionReplying）。
	//
	// 为什么以会话为准而不是 task.Status：任务可以长时间停在 waiting_user（等审核），
	// 那只是「等人」，不代表 agent 在动；反过来 agent 可能刚起、状态还没落库。会话是否
	// 在回复是唯一直接反映「agent 此刻在不在动」的信号 —— 收尾要拆掉 agent 的 cwd，
	// 判据错了就是把人家正在写的目录删了。
	//
	// nil 时（单测、老装配）回落到 task.Status / 段状态那套旧判据，行为与改造前一致。
	sessionRunningProbe func(sessionKey string) bool
	// activityBroadcaster 广播对话式活动流的生命周期事件（Task 7）。由 api 层在
	// 装配时注入（per-account StreamHub 适配器）；nil 时静默，单测与老装配不变。
	// ActivityServiceForRoot 每次新建的 ActivityService 都会带上它。
	activityBroadcaster ActivityBroadcaster
	// workflowLLM 是 workflow 自动选择的语言模型客户端（推荐器）。nil 时推荐器
	// 退化为「第一个可用 workflow」；装配时由 api 层用 Router 的 agent/model 注入。
	workflowLLM LLMClient
	// directExecutor 是「纯对话模式」的直连执行端口，由 api 层注入。
	// ActivityServiceForRoot 每次新建的 ActivityService 都会带上它。
	directExecutor ActivityDirectExecutor
	// routerConfig 是节点本地 Task Router 配置。零值/off 时 Router 完全不存在。
	routerConfig RouterConfig
	// routerAgent 由 api 层装配（AppContext 实现 kanban.RouterAgent）；nil 时
	// Router 不可用，shadow 记录会落 fallback，旧路径照常执行。
	routerAgent RouterAgent
	// routers 按 root 缓存 RouterService。必须挂在长生命周期 Service 上，
	// 不能放在每次新建的 ActivityService 里，否则每回合都丢 lane。
	routers map[string]*RouterService
	// routerBroadcaster 广播决策落库/状态变化；nil 时静默（单测/老装配）。
	routerBroadcaster RouterDecisionBroadcaster
}

// finishToken 是一次收尾的锁凭证。见 acquireTaskFinish。
type finishToken struct{}

var errTaskFinishing = errors.New("该任务正在收尾 worktree，稍后再执行")

// ErrTaskSessionRunning 表示任务关联的会话此刻正在回复 —— 收尾必须等它停下。
// 导出是因为 api 层要按它区分「agent 在动，什么都不做只回一句」和「其它拒绝（409）」。
var ErrTaskSessionRunning = errors.New("任务关联的会话正在回复中，等它停下来再收尾")

// ErrFinishStageExists 表示任务已经在收尾流程里（流水上已有收尾段）。
// 导出供 api 层区分「已有收尾段 → 重跑那段继续收尾」和「其它拒绝」。
var ErrFinishStageExists = errors.New("该任务已在收尾流程中")

func NewService(roots RootProvider) *Service {
	return &Service{Roots: roots, stores: map[string]*TaskStore{}, taskFinish: map[string]*finishToken{}, workflowDrives: map[string]*workflowDrive{}, workflowFinishLocks: map[string]*workflowFinishLock{}, workflowCancelTimeout: 10 * time.Second}
}

// lockWorkflowNodeFinish 取得某个 NodeRun 的收尾锁。取消路径与合并收尾路径
// 必须共用它，才能在「agent 已返回、正在 merge」的窗口里不互相踩状态。
func (s *Service) lockWorkflowNodeFinish(nodeRunID string) func() {
	if s == nil {
		return func() {}
	}
	nodeRunID = strings.TrimSpace(nodeRunID)
	if nodeRunID == "" {
		return func() {}
	}
	s.mu.Lock()
	if s.workflowFinishLocks == nil {
		s.workflowFinishLocks = map[string]*workflowFinishLock{}
	}
	lock := s.workflowFinishLocks[nodeRunID]
	if lock == nil {
		lock = &workflowFinishLock{}
		s.workflowFinishLocks[nodeRunID] = lock
	}
	lock.refs++
	s.mu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.mu.Lock()
		lock.refs--
		if lock.refs == 0 && s.workflowFinishLocks[nodeRunID] == lock {
			delete(s.workflowFinishLocks, nodeRunID)
		}
		s.mu.Unlock()
	}
}

func (s *Service) SetRunner(runner Runner) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.Runner = runner
	s.mu.Unlock()
}

// SetWriterGate 注入 app 级共享的物理工作树写互斥。装配期调一次；所有账户的
// Kanban Service 必须拿到同一个实例，否则跨账户命中同一物理树时互斥失效。
func (s *Service) SetWriterGate(gate *WriterGate) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.WriterGate = gate
	s.mu.Unlock()
}

// SetWorkflowNodeRunner 挂上 Workflow Agent 节点执行端口。装配期调一次。
func (s *Service) SetWorkflowNodeRunner(runner WorkflowNodeRunner) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.WorkflowNodeRunnerPort = runner
	s.mu.Unlock()
}

// SetWorkflowCommentSender 挂上排队留言的续聊发送端口。装配期调一次；nil 时
// 到期的留言进入 blocked（needs-user），绝不假装已投递。
func (s *Service) SetWorkflowCommentSender(sender WorkflowCommentSender) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.WorkflowCommentSenderPort = sender
	s.mu.Unlock()
}

// SetRouterConfig 注入节点本地 Task Router 配置。装配期调一次；零值 = off。
func (s *Service) SetRouterConfig(config RouterConfig) {
	if s == nil {
		return
	}
	config = config.Normalized()
	s.mu.Lock()
	s.routerConfig = config
	if config.Mode == RouterModeOff && len(config.RootModes) == 0 {
		// 关闭时清掉已缓存 lane，避免残留的 Router 会话继续被复用。
		s.routers = nil
	}
	s.mu.Unlock()
}

// RouterConfig 返回当前配置快照（已归一化）。
func (s *Service) RouterConfig() RouterConfig {
	if s == nil {
		return RouterConfig{}.Normalized()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.routerConfig
}

// SetRouterAgent 挂上 Router 的 agent 适配器（api.AppContext 实现）。nil 时
// Router 不可用：shadow 会落 fallback，旧路径照常执行。
func (s *Service) SetRouterAgent(agent RouterAgent) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.routerAgent = agent
	s.mu.Unlock()
}

// SetRouterBroadcaster 挂上决策广播端口（每账户 StreamHub）。
func (s *Service) SetRouterBroadcaster(b RouterDecisionBroadcaster) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.routerBroadcaster = b
	s.mu.Unlock()
}

// RouterServiceForRoot 返回该 root 的 Router lane 管理器；mode=off 时返回 nil。
// 缓存保证同一任务的所有回合共用一条 lane（同任务串行、不同任务并行）。
func (s *Service) RouterServiceForRoot(rootID string) *RouterService {
	if s == nil {
		return nil
	}
	config := s.RouterConfig()
	if config.ModeForRoot(rootID) == RouterModeOff {
		return nil
	}
	// taskStore 自己会取 s.mu；必须先取出再拿锁，否则 newRouterService 在锁内
	// 再调 taskStore 会自锁。
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.routers == nil {
		s.routers = make(map[string]*RouterService)
	}
	if existing := s.routers[rootID]; existing != nil {
		return existing
	}
	created := newRouterService(s, rootID, config, store)
	s.routers[rootID] = created
	return created
}

// CloseRouterTask 在删除任务时关掉 Router runtime 并清账。store 删除由
// TaskStore.DeleteTask 完成；这里只处理进程内 runtime。
func (s *Service) CloseRouterTask(ctx context.Context, rootID, taskID string) {
	if s == nil {
		return
	}
	rs := s.RouterServiceForRoot(rootID)
	if rs == nil {
		return
	}
	rs.CloseTask(ctx, taskID)
}

// SetSessionRunningProbe 挂上「会话是否在回复」的探测函数。装配期调一次。
func (s *Service) SetSessionRunningProbe(fn func(sessionKey string) bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.sessionRunningProbe = fn
	s.mu.Unlock()
}

// sessionRunning 报某个会话此刻是否在回复。探针没挂上（单测、老装配）时恒为 false，
// 调用方据此回落到状态判据 —— 见 sessionProbeConfigured。
func (s *Service) sessionRunning(sessionKey string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	fn := s.sessionRunningProbe
	s.mu.Unlock()
	if fn == nil || strings.TrimSpace(sessionKey) == "" {
		return false
	}
	return fn(sessionKey)
}

// sessionProbeConfigured 报探针是否已装配。没装配时收尾回落看 task.Status / 段状态，
// 否则「探针恒 false」会被读成「agent 一定没在跑」，把保护整个架空。
func (s *Service) sessionProbeConfigured() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionRunningProbe != nil
}

type CreateTaskInput struct {
	RootID             string
	Input              string // 首轮用户输入（同时落成第一条 user_message）
	Name               string // 可选：任务名；缺省时前端用输入首行回退
	CreateWorktree     bool
	WorktreeBranchMode string
	WorktreeBranch     string
}

type MoveInput struct {
	RootID string
	TaskID string
	Reason string
}

func TaskSessionName(base string, taskNumber int) string {
	base = TrimTaskSessionNameSuffix(base, taskNumber)
	if taskNumber <= 0 {
		return base
	}
	number := "#" + strconv.Itoa(taskNumber)
	if base == "" {
		return number
	}
	return base + taskSessionNameSeparator + number
}

// TrimTaskSessionNameSuffix 剥掉尾部的 " / #N"，且只在 N 等于该任务自己的编号时才剥。
// 剥是为了让会话名回流成任务名时不带后缀（否则面板上两个 #编号 撞车）；
// 比对编号是为了不误伤「任务名本来就叫 foo / #3」这种真名。
func TrimTaskSessionNameSuffix(name string, taskNumber int) string {
	trimmed := strings.TrimSpace(name)
	if taskNumber <= 0 {
		return trimmed
	}
	marker := taskSessionNameSeparator + "#"
	idx := strings.LastIndex(trimmed, marker)
	if idx < 0 {
		return trimmed
	}
	n, err := strconv.Atoi(trimmed[idx+len(marker):])
	if err != nil || n != taskNumber {
		return trimmed
	}
	return strings.TrimSpace(trimmed[:idx])
}
