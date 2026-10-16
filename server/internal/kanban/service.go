package kanban

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"mindfs/server/internal/fs"
)

// 阶段完成契约：agent 必须显式回报，本段才算完成。
//
// 以前判据是「没抛错」，于是 agent 撞墙/卡住/只写一半都被记成 success，
// executeTask 紧接着推进并跑下一段，阶段错乱。现在没回报就停在 waiting_user。
//
// 标记带方括号带 MINDFS 前缀，agent 正文里几乎不会自然撞上，误判率远低于裸数字。
// 段号冗余写进标记：上一段的残留标记不会被当成本段完成。
const (
	stageDoneTag    = "MINDFS-STAGE-DONE"
	stageBlockedTag = "MINDFS-STAGE-BLOCKED"
)

// stageDoneRe / stageBlockedRe 只在本段编号吻合时才算数（见 matchStageOutcome）。
var (
	stageDoneRe    = regexp.MustCompile(`\[` + stageDoneTag + `:(\d+)\]`)
	stageBlockedRe = regexp.MustCompile(`\[` + stageBlockedTag + `:(\d+)\s*([^\]]*)\]`)
)

// BuildStageExitContract 拼给 agent 的完成契约。导出是因为真正把它送到 agent
// 眼前的是 api 层（DeveloperInstructions 通道 / 可见 user message 兜底），
// 而匹配方在 api 层也要用 —— 文案与判据必须同源。
func BuildStageExitContract(stageIndex int) string {
	return fmt.Sprintf(
		"\n\n<mindfs-stage-exit stage=%q>\n"+
			"完成本段全部工作后，在回复的最后单独一行输出 [%s:%d]（该行不要加别的内容）。\n"+
			"若未完成或中途受阻，改为输出 [%s:%d 原因]。\n"+
			"未输出以上任一标记时，本段会被判为未完成，任务将停下等你处理。\n"+
			"</mindfs-stage-exit>",
		strconv.Itoa(stageIndex),
		stageDoneTag, stageIndex,
		stageBlockedTag, stageIndex,
	)
}

// MatchStageOutcome 从 agent 本轮输出里判定结论。段号必须吻合本段：
// 上一段的残留标记不算数。优先 Done（agent 先说做完了又补了原因时按做完算）。
func MatchStageOutcome(text string, stageIndex int) StageResult {
	return matchStageOutcome(text, stageIndex)
}

func matchStageOutcome(text string, stageIndex int) StageResult {
	want := strconv.Itoa(stageIndex)
	if m := stageDoneRe.FindStringSubmatch(text); m != nil && m[1] == want {
		return StageResult{Outcome: StageOutcomeDone}
	}
	if m := stageBlockedRe.FindStringSubmatch(text); m != nil && m[1] == want {
		return StageResult{Outcome: StageOutcomeBlocked, Reason: strings.TrimSpace(m[2])}
	}
	return StageResult{Outcome: StageOutcomeSilent}
}

type RootProvider interface {
	GetRoot(rootID string) (fs.RootInfo, error)
	ListRoots() []fs.RootInfo
}

type Service struct {
	Templates *TemplateStore
	Roots     RootProvider
	Runner    Runner

	mu       sync.Mutex
	stores   map[string]*TaskStore
	taskRun  map[string]bool
	taskPend map[string]bool
	// taskFinish 标记「正在收尾 worktree」的任务。收尾会拆掉 agent 正在用的
	// 目录，所以必须和执行互斥：RunTask 拿不到锁就退化成挂起（跟 taskRun 的
	// 处理一样），否则会出现「查完说没在跑，紧接着有人把 agent 起来，收尾把
	// 它的 cwd 删了」。见 acquireTaskFinish。
	taskFinish map[string]*finishToken
	// taskAdmit 标记「正在为这个任务启动执行体」的短暂窗口：动词已经拿到准入、
	// 正在改状态、还没走到 RunTask。这段窗口里 acquireTaskFinish 必须拒绝 ——
	// 否则收尾会抢进来把 agent 的 cwd 拆掉，而动词那边已经把状态改成 running 了。
	//
	// 为什么不能只是「查一下」：查和改之间不是原子的，中间插进来一个收尾就会留下
	// 「状态是 running、却没有执行体」的任务 —— 没有 agent 会再碰它，看起来像卡死。
	taskAdmit map[string]*admitToken
	// finishStageFinished 是「收尾段跑完之后」的回调，由 api 层在装配时挂上。
	// 见 worktree_finish_stage.go 的 notifyFinishStageOutcome —— 它必须挂在
	// executeTask 之外，因为清场要抢 taskFinish 锁而那条锁见到 taskRun 就拒绝。
	finishStageFinished FinishStageFinishedFunc
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
}

// finishToken 是一次收尾的锁凭证。见 acquireTaskFinish。
type finishToken struct{}

// admitToken 是一次启动执行体的准入凭证。见 beginRunAdmission。
type admitToken struct{}

var errStopTaskExecution = errors.New("stop task execution")
var errTaskFinishing = errors.New("该任务正在收尾 worktree，稍后再执行")

// ErrTaskSessionRunning 表示任务关联的会话此刻正在回复 —— 收尾必须等它停下。
// 导出是因为 api 层要按它区分「agent 在动，什么都不做只回一句」和「其它拒绝（409）」。
var ErrTaskSessionRunning = errors.New("任务关联的会话正在回复中，等它停下来再收尾")

// ErrFinishStageExists 表示任务已经在收尾流程里（流水上已有收尾段）。
// 导出供 api 层区分「已有收尾段 → 重跑那段继续收尾」和「其它拒绝」。
var ErrFinishStageExists = errors.New("该任务已在收尾流程中")

func NewService(templates *TemplateStore, roots RootProvider) *Service {
	return &Service{Templates: templates, Roots: roots, stores: map[string]*TaskStore{}, taskRun: map[string]bool{}, taskPend: map[string]bool{}, taskFinish: map[string]*finishToken{}, taskAdmit: map[string]*admitToken{}}
}

func (s *Service) SetRunner(runner Runner) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.Runner = runner
	s.mu.Unlock()
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
	TaskTemplateID     string          // 可选：预设来源，仅记录来自哪个预设；内容在创建时拷进任务
	TaskTemplateName   string          // 可选：预设名快照。库在主节点而任务建在本节点时，本地查不到预设名（见 templateNameForTask），由调用方带上
	Input              string          // 第一段的用户输入
	Name               string          // 可选：任务名；缺省时前端用输入首行回退
	Stages             []StageTemplate // 可选：直接给定流水；否则从预设拷贝
	CreateWorktree     bool
	WorktreeBranchMode string
	WorktreeBranch     string
}

type MoveInput struct {
	RootID     string
	TaskID     string
	Reason     string
	StageIndex int
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
