package kanban

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

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

type UpdateTaskInput struct {
	RootID             string
	TaskID             string
	Input              string
	CreateWorktree     *bool
	WorktreeBranchMode string
	WorktreeBranch     string
}

// AddStageInput 追加一段 prompt（下一段要执行的内容）。
// 任务处于等待用户时，追加即自动推进；其他状态排入流水尾。
type AddStageInput struct {
	RootID string
	TaskID string
	Stage  StageTemplate
}

// UpdateStageInput 修改任务流水里某一段的定义。
type UpdateStageInput struct {
	RootID string
	TaskID string
	Index  int
	Stage  *StageTemplate // 全量替换该段定义
}

// RemoveStageInput 删除任务流水里尚未执行的一段。
type RemoveStageInput struct {
	RootID string
	TaskID string
	Index  int
}

func (s *Service) ListStageTemplates(ctx context.Context) ([]StageTemplate, error) {
	if s == nil || s.Templates == nil {
		return nil, errors.New("template store not configured")
	}
	return s.Templates.ListStageTemplates()
}

func (s *Service) SaveStageTemplate(ctx context.Context, in StageTemplate) (StageTemplate, error) {
	if s == nil || s.Templates == nil {
		return StageTemplate{}, errors.New("template store not configured")
	}
	return s.Templates.SaveStageTemplate(in)
}

func (s *Service) DeleteStageTemplate(ctx context.Context, id string) error {
	if s == nil || s.Templates == nil {
		return errors.New("template store not configured")
	}
	return s.Templates.DeleteStageTemplate(id)
}

func (s *Service) ListTaskTemplates(ctx context.Context) ([]TaskTemplate, error) {
	if s == nil || s.Templates == nil {
		return nil, errors.New("template store not configured")
	}
	return s.Templates.ListTaskTemplates()
}

// ListTaskTemplatesForRoot 返回「全局 + 指定项目」的模板（见 TemplateStore 同名方法）。
func (s *Service) ListTaskTemplatesForRoot(ctx context.Context, rootID string) ([]TaskTemplate, error) {
	if s == nil || s.Templates == nil {
		return nil, errors.New("template store not configured")
	}
	return s.Templates.ListTaskTemplatesForRoot(rootID)
}

// 模板至此只是「预设」：可随时编辑/删除，任务创建时已拷贝快照，与在途任务完全解耦。
func (s *Service) SaveTaskTemplate(ctx context.Context, in TaskTemplate) (TaskTemplate, error) {
	if s == nil || s.Templates == nil {
		return TaskTemplate{}, errors.New("template store not configured")
	}
	return s.Templates.SaveTaskTemplate(in)
}

func (s *Service) DeleteTaskTemplate(ctx context.Context, id string) error {
	if s == nil || s.Templates == nil {
		return errors.New("template store not configured")
	}
	return s.Templates.DeleteTaskTemplate(id)
}

// normalizeTaskStages 展平并规范化任务流水。
func normalizeTaskStages(in []StageTemplate) []StageTemplate {
	out := make([]StageTemplate, 0, len(in))
	for _, st := range in {
		out = append(out, normalizeStageTemplate(st))
	}
	return out
}

func (s *Service) CreateTask(ctx context.Context, in CreateTaskInput) (TaskDetail, error) {
	rootID := strings.TrimSpace(in.RootID)
	if rootID == "" {
		return TaskDetail{}, errors.New("root_id required")
	}
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	stages := normalizeTaskStages(in.Stages)
	name := strings.TrimSpace(in.Name)
	if len(stages) == 0 && strings.TrimSpace(in.TaskTemplateID) != "" {
		tmpl, err := s.Templates.GetTaskTemplate(in.TaskTemplateID)
		if err != nil {
			return TaskDetail{}, err
		}
		for _, ts := range tmpl.Stages {
			stages = append(stages, normalizeStageTemplate(ts.Snapshot))
		}
		if name == "" {
			name = tmpl.Name
		}
	}
	if len(stages) == 0 || stages[0].Role != RoleUser {
		return TaskDetail{}, errors.New("task first stage must be user")
	}
	now := time.Now().UTC()
	branchMode, branch := normalizeTaskWorktreeBranch(in.WorktreeBranchMode, in.WorktreeBranch)
	taskID := newID("task")
	first := stages[0]
	task := Task{
		ID:                 taskID,
		RootID:             rootID,
		Name:               name,
		TaskTemplateID:     strings.TrimSpace(in.TaskTemplateID),
		TaskTemplateName:   templateNameForTask(s, in.TaskTemplateID, in.TaskTemplateName),
		Stages:             stages,
		CreateWorktree:     in.CreateWorktree,
		WorktreeBranchMode: branchMode,
		WorktreeBranch:     branch,
		CurrentStageIndex:  0,
		Status:             StatusPending,
		Labels:             []string{},
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	run := StageRun{
		ID:         newID("run"),
		TaskID:     taskID,
		StageIndex: 0,
		StageName:  first.Name,
		Role:       RoleUser,
		Status:     StageStatusPending,
		Input:      strings.TrimSpace(in.Input),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	event := TaskEvent{
		ID:         newID("event"),
		TaskID:     taskID,
		StageRunID: run.ID,
		Type:       "task_created",
		Payload:    eventPayload(map[string]any{"input": run.Input}),
		CreatedAt:  now,
	}
	if _, err := store.CreateTask(ctx, task, run, event); err != nil {
		return TaskDetail{}, err
	}
	// 首段勾了「立即执行」就直接推进到下一个 agent 段跑起来，没勾就安静停在
	//「未开始」（等用户点「立即执行」）。RunNow 会把当前 user 段一并批准掉，
	// 并替我们兜住 worktree 建不起来的情况。
	//
	// 输入为空时不推进：目标段多半引用 {previous_input}，RunNow 会因
	//「current stage input required」失败。服务端不能假设只有前端一个调用方，
	// 那种情况应当停在「未开始」而不是让创建请求报错。
	if first.StartImmediately && len(stages) > 1 && strings.TrimSpace(in.Input) != "" {
		if _, runErr := s.RunNow(ctx, MoveInput{RootID: rootID, TaskID: taskID, Reason: "start_immediately"}); runErr != nil {
			log.Printf("[kanban] task.start_immediately.error root=%s task=%s err=%v", rootID, taskID, runErr)
		}
	}
	return store.GetDetail(ctx, taskID)
}

// templateNameForTask 解析任务来源预设的名字。
//
// 预设库只有主节点一份（见 docs/multi-node-control-plane.md），而任务建在**项目所在的节点**。
// 在 worker 上本机查不到预设 → 回落：调用方带来的名字快照（前端从主节点读到模板后传下来）。
// 都没有就返回空串，模板来源标签留空，不影响建任务。
func templateNameForTask(s *Service, templateID, provided string) string {
	if tmpl, err := s.Templates.GetTaskTemplate(templateID); err == nil {
		return tmpl.Name
	}
	return strings.TrimSpace(provided)
}

func (s *Service) ListTasks(ctx context.Context, rootID string, opts ListTasksOptions) ([]Task, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	return store.ListTasks(ctx, opts)
}

func (s *Service) ListTaskDetails(ctx context.Context, rootID string, opts ListTasksOptions) ([]TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, err
	}
	return store.ListTaskDetails(ctx, opts)
}

// TaskOverviewItem 是跨项目工作台的一行：任务 + 所属项目。
type TaskOverviewItem struct {
	RootID   string `json:"root_id"`
	RootName string `json:"root_name"`
	Task     Task   `json:"task"`
}

// Overview 汇总所有项目的在途任务（未终态 + 最近完成的少量，供归档区查看）。
// 只读，不建 store 之外的缓存；单个项目失败时跳过不阻塞全貌。
func (s *Service) Overview(ctx context.Context) ([]TaskOverviewItem, error) {
	if s == nil || s.Roots == nil {
		return nil, errors.New("root provider not configured")
	}
	items := []TaskOverviewItem{}
	for _, root := range s.Roots.ListRoots() {
		store, err := s.taskStore(root.ID)
		if err != nil {
			continue
		}
		tasks, err := store.ListTasks(ctx, ListTasksOptions{Limit: 50})
		if err != nil {
			continue
		}
		for _, task := range tasks {
			items = append(items, TaskOverviewItem{RootID: root.ID, RootName: root.EffectiveName(), Task: task})
		}
	}
	return items, nil
}

func (s *Service) GetTask(ctx context.Context, rootID, taskID string) (TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	return store.GetDetail(ctx, taskID)
}

// taskSessionNameSeparator 是任务名与 #编号之间的分隔，与后缀一起构成会话名。
const taskSessionNameSeparator = " / "

// TaskSessionName 组出绑定会话该有的名字：<任务名> / #<任务号>。
// 建会话（AppContext.EnsureAgentSession）与「任务改名 → 同步会话名」都走它，
// 后缀才不会只在建会话那一刻存在 —— 改名一次就丢，那正是这个函数要收口的原因。
//
// 幂等：base 尾部已带本任务号的后缀时先剥再拼，重复调用不会叠成 "名 / #8 / #8"。
// taskNumber <= 0（legacy 行没有编号）时原样返回，不凭空造后缀。
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

// RenameTask 设置任务名。
func (s *Service) RenameTask(ctx context.Context, rootID, taskID, name string) (TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(taskID))
	if err != nil {
		return TaskDetail{}, err
	}
	// 任务名永不携带本任务自己的后缀：这里剥一次，「任务名 ↔ 会话名」往返怎么走都干净。
	task.Name = TrimTaskSessionNameSuffix(name, task.TaskNumber)
	task.UpdatedAt = time.Now().UTC()
	if err := store.UpdateTask(ctx, task); err != nil {
		return TaskDetail{}, err
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

// TaskNameFromSession：会话改名时同步到绑定的任务（main_session_key 方向；
// 与 HTTP 层「任务→会话」同步共同构成任务名 ↔ 会话名双向绑定）。
// 返回 (detail, true) 表示任务名确实更新；未找到任务或名字未变返回 (_, false)。
func (s *Service) TaskNameFromSession(ctx context.Context, rootID, sessionKey, name string) (TaskDetail, bool) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, false
	}
	taskID, err := store.TaskIDForMainSession(ctx, sessionKey)
	if err != nil || taskID == "" {
		return TaskDetail{}, false
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return TaskDetail{}, false
	}
	// 会话名带着 " / #编号"，比变更前先剥掉，否则「原样重命名一个带后缀的会话」
	// 会被判成变更，白跑一趟 RenameTask + broadcastTaskUpdated。
	base := TrimTaskSessionNameSuffix(name, task.TaskNumber)
	if task.Name == base {
		return TaskDetail{}, false
	}
	detail, err := s.RenameTask(ctx, rootID, taskID, base)
	if err != nil {
		return TaskDetail{}, false
	}
	return detail, true
}

// DetachFromSession：会话被**删除**时（会话→任务方向，与 TaskNameFromSession 互为镜像）
// 清空任务指向它的所有引用，并在 aux_session_error 上留痕，让任务面板显示
// 「关联会话已删除」而不是跳进一个不存在的 key。
//
// 归档不走这里：归档的会话仍能打开，链接必须保留。
// 返回 (detail, true) 表示确实改动了任务；没有任务绑这些会话时返回 (_, false)。
func (s *Service) DetachFromSession(ctx context.Context, rootID string, sessionKeys []string) (TaskDetail, bool) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, false
	}
	var last TaskDetail
	changed := false
	// 逐个 key 反查：删除是按子树级联的，key 数量不定，
	// TaskIDForMainSession 一次只认一个 key。
	for _, sessionKey := range sessionKeys {
		key := strings.TrimSpace(sessionKey)
		if key == "" {
			continue
		}
		taskID, err := store.TaskIDForMainSession(ctx, key)
		if err != nil || taskID == "" {
			continue
		}
		if err := store.ClearSessionRefs(ctx, taskID, key); err != nil {
			log.Printf("[kanban] detach session refs failed task=%s session=%s: %v", taskID, key, err)
			continue
		}
		msg := deletedSessionNotice(key)
		detail, err := s.UpdateTaskAuxFlags(ctx, rootID, taskID, TaskAuxFlagsPatch{SessionError: &msg}, "session_deleted")
		if err != nil {
			continue
		}
		last = detail
		changed = true
	}
	return last, changed
}

// deletedSessionNotice 生成 aux_session_error 的留痕文案。
// 形状是 {"message":..., "data":[...]}，前端 parseTaskSessionErrorMessage 已按此解析。
func deletedSessionNotice(sessionKey string) string {
	payload := map[string]any{
		"message": fmt.Sprintf("关联会话已删除（%s），任务已解绑。", sessionKey),
		"data":    []string{sessionKey},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("关联会话已删除（%s），任务已解绑。", sessionKey)
	}
	return string(raw)
}

// AddStage 追加下一段 prompt。任务等待用户时追加即推进并执行；其他状态排入流水尾。
func (s *Service) AddStage(ctx context.Context, in AddStageInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := s.ensureServiceTask(ctx, in.RootID, store, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	stage := normalizeStageTemplate(in.Stage)
	if task.Status == StatusWaitingUser && strings.TrimSpace(stage.PromptTemplate) != "" {
		// 准入检查放在**最前面**：下面已经要把当前段批成 approved、要把新段推进上去。
		// 收尾期间放进来做完这些再返回，留下的是「段已 approved、没有执行体」——
		// 和「永远 running」是同一种卡死，只是换了张脸。
		release, admErr := s.beginRunAdmission(in.RootID, task.ID)
		if admErr != nil {
			return store.GetDetail(ctx, task.ID)
		}
		defer release()
		stage.Name = defaultStageName(task, len(task.Stages))
		task.Stages = append(task.Stages, stage)
		if latest, runErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); runErr == nil {
			// 同 moveRelative：agent 段没走完时，追加一句评论不能当成「这段已完成」。
			// user 段的 waiting_user 是在等输入，补评论正是答案，照常批准。
			if !canAdvanceFromStage(task.Stages[task.CurrentStageIndex].Role, latest.Status) {
				return TaskDetail{}, fmt.Errorf(
					"current stage is %s: 这一段没走完，追加评论不能替代完成本段，重跑本段或改任务后再试",
					latest.Status,
				)
			}
			if latest.Status != StageStatusSuccess {
				_ = store.UpdateStageRunStatus(ctx, latest.ID, StageStatusApproved)
			}
		}
		detail, err := s.moveTo(ctx, store, task, len(task.Stages)-1, "user_approved", StageStatusApproved, "comment")
		if err != nil {
			return TaskDetail{}, err
		}
		if runErr := s.RunTask(detail.Task.RootID, detail.Task.ID); runErr != nil {
			return TaskDetail{}, runErr
		}
		return detail, nil
	}
	stage.Name = defaultStageName(task, len(task.Stages))
	task.Stages = append(task.Stages, stage)
	task.UpdatedAt = time.Now().UTC()
	if err := store.UpdateTask(ctx, task); err != nil {
		return TaskDetail{}, err
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

// UpdateStage 修改任务某一段的定义（prompt / agent / model / effort 等）。
func (s *Service) UpdateStage(ctx context.Context, in UpdateStageInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := s.ensureServiceTask(ctx, in.RootID, store, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	idx := in.Index
	if idx < 0 || idx >= len(task.Stages) {
		return TaskDetail{}, errors.New("stage_index out of range")
	}
	now := time.Now().UTC()
	if in.Stage != nil {
		updated := normalizeStageTemplate(*in.Stage)
		updated.ID = task.Stages[idx].ID
		updated.CreatedAt = task.Stages[idx].CreatedAt
		task.Stages[idx] = updated
	}
	task.Stages[idx].UpdatedAt = now
	task.UpdatedAt = now
	if err := store.UpdateTask(ctx, task); err != nil {
		return TaskDetail{}, err
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

// RemoveStage 删除任务流水里尚未执行的一段。
// index 0 是任务输入段，不可删；当前指针所在段不可删（执行体正对着它）；
// 产生过 StageRun 的段也不可删（要改走 UpdateStage）。
func (s *Service) RemoveStage(ctx context.Context, in RemoveStageInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := s.ensureServiceTask(ctx, in.RootID, store, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	idx := in.Index
	if idx <= 0 || idx >= len(task.Stages) {
		return TaskDetail{}, errors.New("stage_index out of range")
	}
	if idx == task.CurrentStageIndex {
		return TaskDetail{}, errors.New("current stage cannot be removed")
	}
	runs, err := store.ListStageRuns(ctx, task.ID)
	if err != nil {
		return TaskDetail{}, err
	}
	for _, run := range runs {
		if run.StageIndex == idx && run.Status != StageStatusPending {
			return TaskDetail{}, errors.New("stage already executed")
		}
	}

	task.Stages = append(task.Stages[:idx], task.Stages[idx+1:]...)
	if idx < task.CurrentStageIndex {
		task.CurrentStageIndex--
	}
	if task.CurrentStageIndex >= len(task.Stages) {
		task.CurrentStageIndex = len(task.Stages) - 1
	}
	if task.CurrentStageIndex < 0 {
		task.CurrentStageIndex = 0
	}
	now := time.Now().UTC()
	task.AuxFlags.SessionError = ""
	task.UpdatedAt = now
	if err := store.UpdateTask(ctx, task); err != nil {
		return TaskDetail{}, err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    task.ID,
		Type:      "stage_removed",
		Payload:   eventPayload(map[string]any{"stage_index": idx}),
		CreatedAt: now,
	})
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

// RerunStage 重新执行某段（任务不在运行中时）：把指针移回此段并触发执行。
func (s *Service) RerunStage(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if running, runErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); runErr == nil && running.Status == StageStatusRunning && task.Status == StatusRunning {
		return TaskDetail{}, errors.New("task is running")
	}
	if in.StageIndex < 0 || in.StageIndex >= len(task.Stages) {
		return TaskDetail{}, errors.New("stage_index out of range")
	}
	release, admErr := s.beginRunAdmission(in.RootID, task.ID)
	if admErr != nil {
		return store.GetDetail(ctx, task.ID)
	}
	defer release()
	detail, err := s.moveTo(ctx, store, task, in.StageIndex, "stage_rerun", "", in.Reason)
	if err != nil {
		return TaskDetail{}, err
	}
	if !isTerminalStatus(detail.Task.Status) {
		if runErr := s.RunTask(detail.Task.RootID, detail.Task.ID); runErr != nil {
			return TaskDetail{}, runErr
		}
	}
	return detail, err
}

func (s *Service) UpdateCurrentInput(ctx context.Context, in UpdateTaskInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	run, err := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex)
	if err != nil {
		return TaskDetail{}, err
	}
	input := strings.TrimSpace(in.Input)
	payload := map[string]any{"input": input}
	if in.CreateWorktree != nil {
		if task.CurrentStageIndex != 0 {
			return TaskDetail{}, errors.New("create_worktree can only be changed in first stage")
		}
		// 建过树就不许再改 create_worktree —— 已经跑在那个目录里了。判据用
		// 「路径字段非空」而不是「目录还在」：目录被删是失效状态，该走的是重建入口，
		// 不该让用户借此把任务改成一个和既有历史矛盾的状态。
		if strings.TrimSpace(task.WorktreePath) != "" {
			return TaskDetail{}, errors.New("create_worktree cannot be changed after worktree is created")
		}
		now := time.Now().UTC()
		task.CreateWorktree = *in.CreateWorktree
		task.WorktreeBranchMode, task.WorktreeBranch = normalizeTaskWorktreeBranch(in.WorktreeBranchMode, in.WorktreeBranch)
		task.AuxFlags.SessionError = ""
		task.UpdatedAt = now
		run.Input = input
		event := TaskEvent{
			ID:         newID("event"),
			TaskID:     task.ID,
			StageRunID: run.ID,
			Type:       "stage_input_updated",
			Payload: eventPayload(map[string]any{
				"input":                input,
				"create_worktree":      *in.CreateWorktree,
				"worktree_branch_mode": task.WorktreeBranchMode,
				"worktree_branch":      task.WorktreeBranch,
			}),
			CreatedAt: now,
		}
		if err := store.UpdateTaskAndStageRun(ctx, task, run, event); err != nil {
			return TaskDetail{}, err
		}
		detail, err := store.GetDetail(ctx, task.ID)
		if err == nil && s.Runner != nil {
			s.Runner.TaskUpdated(task.RootID, detail)
		}
		return detail, err
	}
	if err := store.UpdateStageRunInput(ctx, run.ID, input); err != nil {
		return TaskDetail{}, err
	}
	if strings.TrimSpace(task.AuxFlags.SessionError) != "" {
		empty := ""
		_ = store.UpdateTaskAuxFlags(ctx, task.ID, TaskAuxFlagsPatch{SessionError: &empty})
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:         newID("event"),
		TaskID:     task.ID,
		StageRunID: run.ID,
		Type:       "stage_input_updated",
		Payload:    eventPayload(payload),
		CreatedAt:  time.Now().UTC(),
	})
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

func (s *Service) UpdateFirstInput(ctx context.Context, in UpdateTaskInput) (TaskDetail, error) {
	return s.UpdateCurrentInput(ctx, in)
}

func (s *Service) Next(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	if isTerminalStatus(task.Status) {
		return store.GetDetail(ctx, task.ID)
	}
	// 末段等待用户＝收尾，直接完成而不是报 stage out of range。
	if task.Status == StatusWaitingUser && task.CurrentStageIndex >= len(task.Stages)-1 {
		if latest, latestErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); latestErr == nil {
			if latest.Status != StageStatusSuccess {
				_ = store.UpdateStageRunStatus(ctx, latest.ID, StageStatusApproved)
			}
		}
		if err := s.finishTask(ctx, store, task, StatusSuccess, "completed", in.Reason); err != nil {
			return TaskDetail{}, err
		}
		detail, err := store.GetDetail(ctx, task.ID)
		if err == nil && s.Runner != nil {
			s.Runner.TaskUpdated(task.RootID, detail)
		}
		return detail, err
	}
	release, admErr := s.beginRunAdmission(in.RootID, in.TaskID)
	if admErr != nil {
		return store.GetDetail(ctx, in.TaskID)
	}
	defer release()
	detail, err := s.moveRelative(ctx, in, 1, "user_approved", StageStatusApproved)
	if err == nil {
		if runErr := s.RunTask(detail.Task.RootID, detail.Task.ID); runErr != nil {
			return TaskDetail{}, runErr
		}
	}
	return detail, err
}

// RunNow：待开始直接开跑；等待用户视同验收推进到下一段。
func (s *Service) RunNow(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, err := s.taskStore(in.RootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, strings.TrimSpace(in.TaskID))
	if err != nil {
		return TaskDetail{}, err
	}
	switch task.Status {
	case StatusWaitingUser:
		detail, nerr := s.Next(ctx, in)
		// 推进失败时错误已经记在任务上了（moveRelative 里的 recordTaskError）：
		// 「立即执行」不该因此失败，返回当前详情让前端显示那条错误。
		// 典型是 worktree 目录已被删除 —— 用户点按钮要拿到的是卡片上那句人话，
		// 而不是 HTTP 400 加一串底层报错。
		if nerr != nil {
			return store.GetDetail(ctx, task.ID)
		}
		return detail, nil
	case StatusPending:
		// 未开始态点「开始」= 批准当前 user 段并跑起来。已经停在最后一段时
		// 没有下一段可进（moveRelative 会报 out of range），直接执行体推进即可。
		// 曾经这里对 pending 一律只调 RunTask 不推进阶段，多段任务会卡在待审核，
		// 用户得再点一次「执行」。
		if task.CurrentStageIndex >= len(task.Stages)-1 {
			break
		}
		detail, nerr := s.Next(ctx, in)
		// worktree 建不起来时 moveRelative 会报错，但错误已经记在任务上了：
		// 「开始」不该因此失败，返回当前详情让前端显示那条错误。
		// 判据是「此刻有没有可用 worktree」而不是「字段空不空」：目录被删时字段仍非空，
		// 但那不等于有树可用，错误同样已经记在任务上了。
		if nerr != nil && task.CreateWorktree && !worktreeDirUsable(task.WorktreePath) {
			return store.GetDetail(ctx, task.ID)
		}
		return detail, nerr
	case StatusRunning, StatusPaused:
		// 正在跑 / 已暂停：点「立即执行」推进不了任何一段。原先这里静默回 200 +
		// 未变的详情，前端 apply 完看着「什么都没发生」—— 用户无从判断是按钮坏了
		// 还是任务本来就动不了（2026-10-03 任务 26 卡死时就是这个形态）。
		// 明确报错：调用方已经有 reportError 弹窗的通路（App.handleMoveKanbanTask），
		// 重复点击也拿得到同样的反馈。
		return TaskDetail{}, errors.New("task is running or paused: 任务正在执行或已暂停，先暂停/恢复再推进")
	}
	// 准入检查在建事件之前：收尾期间进来的请求不该留下一条「点过立即执行」而
	// 实际什么都没发生的流水（那会让用户以为按钮生效了）。
	release, admErr := s.beginRunAdmission(in.RootID, task.ID)
	if admErr != nil {
		return store.GetDetail(ctx, task.ID)
	}
	defer release()
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    task.ID,
		Type:      "run_now",
		Payload:   eventPayload(map[string]any{"reason": strings.TrimSpace(in.Reason)}),
		CreatedAt: time.Now().UTC(),
	})
	if task.CreateWorktree {
		if _, werr := s.ensureTaskWorktree(ctx, store, task); werr != nil {
			_ = s.recordTaskError(ctx, store, task, "", werr.Error())
			return store.GetDetail(ctx, task.ID)
		}
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil {
		if runErr := s.RunTask(detail.Task.RootID, detail.Task.ID); runErr != nil {
			return TaskDetail{}, runErr
		}
	}
	return detail, err
}

func (s *Service) Pause(ctx context.Context, in MoveInput) (TaskDetail, error) {
	return s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusPaused, "paused", in.Reason, false)
}

func (s *Service) Resume(ctx context.Context, in MoveInput) (TaskDetail, error) {
	// 先问能不能起，再改状态。反过来（先置 running 再 RunTask）会在收尾撞车时
	// 留下一个永远 running、却没有执行体的任务 —— 没有 agent 会再碰它。
	release, admErr := s.beginRunAdmission(in.RootID, in.TaskID)
	if admErr != nil {
		return s.GetTask(ctx, in.RootID, in.TaskID)
	}
	defer release()
	detail, err := s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusRunning, "resumed", in.Reason, false)
	if err == nil {
		if runErr := s.RunTask(detail.Task.RootID, detail.Task.ID); runErr != nil {
			return TaskDetail{}, runErr
		}
	}
	return detail, err
}

// beginRunAdmission 为「要起执行体的动词」开一段准入窗口：查没有收尾 → 放动词改状态
// → RunTask 接手。整段由返回的 release 结束。
//
// 为什么需要一把真的锁而不是「查一下」：查和改之间不是原子的。查通过之后、
// 状态还没改的这一刻，一个收尾请求可以抢进来把 worktree 拆掉；等动词改完状态
// 再去 RunTask 已经被拒 —— 结果是「状态 running、没有执行体」，没有 agent 会再
// 碰它，看起来像卡死。把准入和状态变更绑在一段互斥区间里，收尾要么进不来，
// 要么等这段走完。
func (s *Service) beginRunAdmission(rootID, taskID string) (func(), error) {
	key := rootID + "\x00" + taskID
	token := &admitToken{}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, finishing := s.taskFinish[key]; finishing {
		return nil, errTaskFinishing
	}
	// 已经有一次启动在途（另一个动词正在改状态）：让它先走完，别让两处同时改。
	if _, admitted := s.taskAdmit[key]; admitted {
		return nil, errTaskAlreadyAdmitted
	}
	s.taskAdmit[key] = token
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if held, ok := s.taskAdmit[key]; ok && held == token {
			delete(s.taskAdmit, key)
		}
	}, nil
}

// errTaskAlreadyAdmitted 内部用：同一任务的启动准入已被别人占着。
var errTaskAlreadyAdmitted = errors.New("该任务正在启动中")

func (s *Service) Fail(ctx context.Context, in MoveInput) (TaskDetail, error) {
	return s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusFail, "stage_failed", in.Reason, true)
}

func (s *Service) Cancel(ctx context.Context, in MoveInput) (TaskDetail, error) {
	return s.setTaskStatus(ctx, in.RootID, in.TaskID, StatusCancelled, "cancelled", in.Reason, true)
}

// Complete：任何非终态任务都可一键完成。
// 不看会话/worktree 是否还在——会话被删、worktree 丢了，任务状态照样能人工收尾，
// 否则这些任务会永远卡在 running 没法推进。
func (s *Service) Complete(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if isTerminalStatus(task.Status) {
		return store.GetDetail(ctx, task.ID)
	}
	if latest, runErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); runErr == nil {
		if latest.Status != StageStatusSuccess {
			_ = store.UpdateStageRunStatus(ctx, latest.ID, StageStatusApproved)
		}
	}
	if err := s.finishTask(ctx, store, task, StatusSuccess, "completed", in.Reason); err != nil {
		return TaskDetail{}, err
	}
	return store.GetDetail(ctx, task.ID)
}

func (s *Service) Status(ctx context.Context, rootID, taskID string) (TaskDetail, error) {
	return s.GetTask(ctx, rootID, taskID)
}

// DeleteTask 删掉一张任务卡 —— 只有卡片，worktree / 分支 / 会话一律不碰。
//
// 为什么要有它：看板上的卡只会累积。终态任务留着是为了看历史，看完得能清掉，
// 而「取消」只把状态改成 cancelled，卡片照旧留在板上。
//
// **只在任务没在跑时允许**。执行体还活着的话，它随后对那张行的写入会落空
// （UPDATE 打到不存在的行上，静默 no-op），或者把卡又插回来 —— 与「删掉」相反。
// 判据复用收尾那条 assertNotRunning（会话在不在回复是金标准，见 worktree_finish.go），
// 不另造一套。taskRun 那道锁也要查：已经排进执行队列、还没落 running 状态的窗口里，
// assertNotRunning 看不出东西来。
func (s *Service) DeleteTask(ctx context.Context, in MoveInput) error {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return err
	}
	if err := s.assertNotRunning(ctx, store, task); err != nil {
		return err
	}
	key := strings.TrimSpace(in.RootID) + "\x00" + strings.TrimSpace(in.TaskID)
	s.mu.Lock()
	executing := s.taskRun[key]
	s.mu.Unlock()
	if executing {
		return errors.New("任务正在执行中，先停止再删除")
	}
	return store.DeleteTask(ctx, task.ID)
}

func (s *Service) RunTask(rootID, taskID string) error {
	if s == nil || s.Runner == nil {
		return nil
	}
	rootID = strings.TrimSpace(rootID)
	taskID = strings.TrimSpace(taskID)
	if rootID == "" || taskID == "" {
		return nil
	}
	// 同一任务同时只允许一个执行体。Next/Resume/RunNow 等重复请求不应把同一 agent 阶段跑两次
	// （重复创建 agent 会话、重复消耗 token）。重复请求记为待补跑。
	key := rootID + "\x00" + taskID
	s.mu.Lock()
	// 正在收尾 worktree：不能起 agent。收尾会把 agent 的 cwd 拆掉，而这里起的话
	// agent 会在一个即将消失的目录里跑。宁可不跑，也不跑一个必死的。
	if _, finishing := s.taskFinish[key]; finishing {
		s.mu.Unlock()
		return errTaskFinishing
	}
	if s.taskRun[key] {
		s.taskPend[key] = true
		s.mu.Unlock()
		return nil
	}
	s.taskRun[key] = true
	s.mu.Unlock()
	go func() {
		for {
			if err := s.executeTask(context.Background(), rootID, taskID); err != nil {
				log.Printf("[kanban] task.execute.error root=%s task=%s err=%v", rootID, taskID, err)
			}
			// 收尾段跑完（成功或受阻）就在这里报给外层。**必须在 executeTask 返回之后、
			// 且在 taskRun 标记摘掉之后**：清场要抢 taskFinish 锁，而 acquireTaskFinish
			// 见到 s.taskRun[key] 就拒绝（它就是「还有执行体在跑」）。挂在 executeTask
			// 内部必然被这条锁拒掉 —— agent 收工那一刻执行体确实还占着这个标记。
			// 见 notifyFinishStageOutcome。
			s.notifyFinishStageOutcome(rootID, taskID)
			s.mu.Lock()
			// 执行期间又有请求进来 → 补跑一次（此时阶段多已 waiting_user，补跑不会重复执行 agent）。
			if s.taskPend[key] {
				delete(s.taskPend, key)
				s.mu.Unlock()
				continue
			}
			delete(s.taskRun, key)
			s.mu.Unlock()
			break
		}
	}()
	return nil
}

// acquireTaskFinish 独占这个任务的「收尾权」，收尾期间挡住新的执行。
//
// 为什么必须锁住整个流程而不只是开头查一次：查完「没在跑」到 merge/remove 之间
// 有几秒，任何 Next/RunNow/Rerun 都能在这窗口里把 agent 起来，而它的 cwd 就是
// 那个即将被拆掉的目录。所以锁要一直持到拆完、删完为止。
func (s *Service) acquireTaskFinish(rootID, taskID string) (func(), error) {
	key := rootID + "\x00" + taskID
	// 令牌：release 只认自己那一份。两个并发收尾如果共用一个 bool，先完成的那个
	// release 会把还在跑的另一个的锁一起放开，RunTask 就能对着一个正在被拆的目录
	// 起来。所以锁的「所有权」必须是可辨认的一份，不是一个开关。
	token := &finishToken{}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taskRun[key] {
		return nil, errors.New("任务正在执行中，先停止再收尾")
	}
	// 有人正在为这个任务启动执行体（beginRunAdmission 的窗口里）：那一次会把状态
	// 改成 running 再起 agent。等它走完再收尾，否则收尾插进去就是「状态已改、
	// agent 起不来」。
	if _, admitted := s.taskAdmit[key]; admitted {
		return nil, errors.New("该任务正在启动中，稍后再收尾")
	}
	// 已经在收尾：第二个收尾不能并着跑 —— 它们会对着同一个 worktree 一起
	// merge / remove，其中一个的 release 还会提前放开另一个的锁。
	if _, busy := s.taskFinish[key]; busy {
		return nil, errors.New("该任务正在收尾中，请等它结束")
	}
	s.taskFinish[key] = token
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// 只删自己那份：别人的收尾不该被自己的 release 解锁。
		if held, ok := s.taskFinish[key]; ok && held == token {
			delete(s.taskFinish, key)
		}
	}, nil
}

// KickPending 会启动时进入：仅兜底执行既存任务指针所在段落，不再有排队/槽位语义。
// 只对 pending/running 且未被任何执行体持有的任务触发一次 RunTask。
func (s *Service) KickPending(rootID string) {
	if s == nil || s.Runner == nil {
		return
	}
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return
	}
	go func() {
		store, err := s.taskStore(rootID)
		if err != nil {
			return
		}
		tasks, err := store.ListTasks(context.Background(), ListTasksOptions{})
		if err != nil {
			return
		}
		for _, task := range tasks {
			if isTerminalStatus(task.Status) || task.Status == StatusWaitingUser || task.Status == StatusPending || task.Status == StatusPaused {
				continue
			}
			if strings.TrimSpace(task.AuxFlags.SessionError) != "" {
				continue
			}
			if runErr := s.RunTask(rootID, task.ID); runErr != nil {
				log.Printf("[kanban] task.kick.skipped root=%s task=%s err=%v", rootID, task.ID, runErr)
			}
		}
	}()
}

func (s *Service) UpdateTaskAuxFlags(ctx context.Context, rootID, taskID string, patch TaskAuxFlagsPatch, eventType string) (TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if isTerminalStatus(task.Status) && patch.AskUserWaiting != nil && *patch.AskUserWaiting {
		return store.GetDetail(ctx, task.ID)
	}
	if err := store.UpdateTaskAuxFlags(ctx, task.ID, patch); err != nil {
		return TaskDetail{}, err
	}
	if strings.TrimSpace(eventType) != "" {
		stageRunID := ""
		if run, err := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex); err == nil {
			stageRunID = run.ID
		}
		_ = store.AddEvent(ctx, TaskEvent{
			ID:         newID("event"),
			TaskID:     task.ID,
			StageRunID: stageRunID,
			Type:       strings.TrimSpace(eventType),
			Payload:    eventPayload(map[string]any{"aux_flags": patchPayload(patch)}),
			CreatedAt:  time.Now().UTC(),
		})
	}
	detail, err := store.GetDetail(ctx, task.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(rootID, detail)
	}
	return detail, err
}

func patchPayload(patch TaskAuxFlagsPatch) map[string]any {
	out := map[string]any{}
	if patch.AskUserWaiting != nil {
		out["ask_user_waiting"] = *patch.AskUserWaiting
	}
	if patch.HasPlan != nil {
		out["has_plan"] = *patch.HasPlan
	}
	if patch.HasTodos != nil {
		out["has_todos"] = *patch.HasTodos
	}
	if patch.HasTask != nil {
		out["has_task"] = *patch.HasTask
	}
	if patch.SessionError != nil {
		out["session_error"] = strings.TrimSpace(*patch.SessionError)
	}
	return out
}

func (s *Service) ensureTaskWorktree(ctx context.Context, store *TaskStore, task Task) (Task, error) {
	if !task.CreateWorktree {
		return task, nil
	}
	// 失效时**不自动重建**：重建等于替用户决定那个已删目录里的分支怎么办（分支可能还在、
	// 也可能已经被删），所以报明确错误，由 RebuildTaskWorktree 显式触发。
	if p := strings.TrimSpace(task.WorktreePath); p != "" {
		if worktreeDirUsable(p) {
			return task, nil
		}
		return task, fmt.Errorf("worktree 目录已不存在（%s），点「重建 worktree」恢复后再执行", p)
	}
	if s.Runner == nil {
		return task, errors.New("task runner not configured")
	}
	name := renderWorktreeName("", task)
	branchMode, branch := normalizeTaskWorktreeBranch(task.WorktreeBranchMode, task.WorktreeBranch)
	wt, err := s.Runner.CreateTaskWorktree(ctx, task.RootID, name, branchMode, branch)
	if err != nil {
		return task, err
	}
	// 拿到一个不可用的路径就当没建过：**不写库、不写 worktree_built**。
	//
	// 为什么必须在这里拦：空路径会让下游两处同时说谎 —— agent 拿到空的
	// RuntimeRootPath（service.go 取的就是 task.WorktreePath），于是**在主 checkout
	// 里跑**，改的是主干；界面那边看到「建过 + 路径为空」，把没建过树的活说成
	// 「已收尾」。两个后果都不可逆，所以宁可当失败、让用户重建。
	// 不自动重建的理由与上面「失效时不自动重建」一致：不替用户决定那个目录怎么办。
	if !worktreeDirUsable(wt.Path) {
		return task, fmt.Errorf("worktree 创建没有产出可用目录（路径=%q），请点「重建 worktree」重试", strings.TrimSpace(wt.Path))
	}
	now := time.Now().UTC()
	task.WorktreeRootID = wt.RootID
	task.WorktreePath = wt.Path
	// 记住「建过」：之后清归属（收尾拆目录）会清掉路径，但不该把这条事实一起抹掉，
	// 否则界面分不出「从没建过」和「建过、记录丢了」。
	task.WorktreeBuilt = true
	task.AuxFlags.SessionError = ""
	task.UpdatedAt = now
	// 归属只有一个写入出口（SetWorktreeRefsAndTask），且与状态更新同一事务 ——
	// 理由见 task_store.go 的 updateTaskCore。
	if err := store.SetWorktreeRefsAndTask(ctx, task); err != nil {
		return task, err
	}
	return task, nil
}

// ClearTaskWorktree 清掉任务记录的 worktree 归属（路径 + root id）。
//
// **只给收尾流程用**（它拆掉目录之后清归属）。搬会话不要调它 —— repoint 曾经调过，
// 一次纯搬会话把还在用的任务目录记录清成空，卡片随即显示「已收尾」。见
// usecase/session_repoint.go 里那段刻意不调的注释。
//
// 保留 worktree_built：拆掉目录不等于没建过，界面要靠它显示「已收尾」而不是
// 退回「没建过」。
func (s *Service) ClearTaskWorktree(ctx context.Context, rootID, taskID string) error {
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(task.WorktreePath) == "" && strings.TrimSpace(task.WorktreeRootID) == "" {
		return nil
	}
	// 只清归属两列，不整行写回 —— 理由见 TaskStore.ClearWorktreeRefs。
	if err := store.ClearWorktreeRefs(ctx, task.ID); err != nil {
		return err
	}
	if detail, err := store.GetDetail(ctx, task.ID); err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return nil
}

// RebuildTaskWorktree 重建已删除的任务 worktree。
//
// 为什么不自动重建：目录没了之后，那个分支可能还在（代码还在分支上）也可能已经跟着
// 删了。自动重建等于替用户决定哪一种，而 gitview.AddWorktree 的 -b 在分支仍存在时会
// 报 branch already exists —— 那种错必须让人看见，不该被吞掉。所以这里是显式入口：
// 用户点了才重建，报错了就原样报上去。
//
// 幂等：目录还在时直接返回当前详情，不重复建树。
func (s *Service) RebuildTaskWorktree(ctx context.Context, in MoveInput) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}
	if !task.CreateWorktree {
		return TaskDetail{}, errors.New("task has no worktree")
	}
	if worktreeDirUsable(task.WorktreePath) {
		return store.GetDetail(ctx, task.ID)
	}
	// 清掉陈旧路径，让 ensureTaskWorktree 走建树分支而不是撞上「目录已不存在」那条例外。
	task.WorktreePath = ""
	task.WorktreeRootID = ""
	updated, err := s.ensureTaskWorktree(ctx, store, task)
	if err != nil {
		// 失败原因记到任务上：卡片本来就会渲染 session_error，用户不必去翻日志
		// （典型是 branch already exists，需要他决定怎么处理那个分支）。
		_ = s.recordTaskError(ctx, store, task, "", err.Error())
		return store.GetDetail(ctx, task.ID)
	}
	detail, err := store.GetDetail(ctx, updated.ID)
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(updated.RootID, detail)
	}
	return detail, err
}

func renderWorktreeName(tpl string, task Task) string {
	name := strings.TrimSpace(tpl)
	if name == "" {
		name = "task-{task_number}"
	}
	replacements := map[string]string{
		"task_id":       task.ID,
		"task_number":   strconv.Itoa(task.TaskNumber),
		"root_id":       task.RootID,
		"template_name": task.TaskTemplateName,
		"task_name":     task.Name,
	}
	for key, value := range replacements {
		name = strings.ReplaceAll(name, "{"+key+"}", value)
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "task-0" {
		name = filepath.Base(task.ID)
	}
	return name
}

func normalizeTaskWorktreeBranch(mode, branch string) (string, string) {
	mode = strings.TrimSpace(mode)
	branch = strings.TrimSpace(branch)
	if mode != "existing" {
		return "new", ""
	}
	if branch == "" {
		return "new", ""
	}
	return "existing", branch
}

func (s *Service) executeTask(ctx context.Context, rootID, taskID string) error {
	store, task, err := s.loadForMove(ctx, rootID, taskID)
	if err != nil {
		return err
	}
	for {
		if task.Status == StatusPaused || isTerminalStatus(task.Status) {
			return nil
		}
		if task.CurrentStageIndex < 0 || task.CurrentStageIndex >= len(task.Stages) {
			// 流水被改短（段被删）：停在等待用户，不再静默完成。
			return s.waitForUserSimpl(ctx, store, task, "stage list truncated")
		}
		stage := task.Stages[task.CurrentStageIndex]
		run, err := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex)
		if err != nil {
			return err
		}
		if stage.Role == RoleUser {
			if run.Status == StageStatusApproved || run.Status == StageStatusSuccess {
				if task.CurrentStageIndex == len(task.Stages)-1 {
					return s.finishTask(ctx, store, task, StatusSuccess, "completed", "")
				}
				detail, err := s.moveTo(ctx, store, task, task.CurrentStageIndex+1, "auto_advanced", "", "")
				if err != nil {
					return err
				}
				task = detail.Task
				continue
			}
			return s.waitForUser(ctx, store, task, run, "user_input_required")
		}
		if stage.Role != RoleAgent {
			return s.failTask(ctx, store, task, run.ID, fmt.Errorf("unsupported stage role %q", stage.Role))
		}
		if run.Status == StageStatusSuccess {
			if stage.AutoAdvance {
				if task.CurrentStageIndex == len(task.Stages)-1 {
					return s.finishTask(ctx, store, task, StatusSuccess, "completed", "")
				}
				detail, err := s.moveTo(ctx, store, task, task.CurrentStageIndex+1, "auto_advanced", "", "")
				if err != nil {
					return err
				}
				task = detail.Task
				continue
			}
			return s.waitForUser(ctx, store, task, run, "agent_stage_done")
		}
		if run.Status == StageStatusWaitingUser {
			return nil
		}
		if task.CreateWorktree && !worktreeDirUsable(task.WorktreePath) {
			updated, werr := s.ensureTaskWorktree(ctx, store, task)
			if werr != nil {
				return s.failTask(ctx, store, task, run.ID, werr)
			}
			task = updated
		}
		if err := s.runAgentStage(ctx, store, task, stage, run); err != nil {
			if errors.Is(err, errStopTaskExecution) {
				return nil
			}
			return err
		}
		task, err = store.GetTask(ctx, task.ID)
		if err != nil {
			return err
		}
	}
}

// waitForUserSimpl 在尚未创建 StageRun 时把任务置于等待用户。
func (s *Service) waitForUserSimpl(ctx context.Context, store *TaskStore, task Task, reason string) error {
	now := time.Now().UTC()
	task.Status = StatusWaitingUser
	task.AuxFlags.SessionError = strings.TrimSpace(reason)
	task.UpdatedAt = now
	if err := store.UpdateTask(ctx, task); err != nil {
		return err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    task.ID,
		Type:      "waiting_user",
		Payload:   eventPayload(map[string]any{"reason": strings.TrimSpace(reason)}),
		CreatedAt: now,
	})
	if detail, err := store.GetDetail(ctx, task.ID); err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return nil
}

func (s *Service) runAgentStage(ctx context.Context, store *TaskStore, task Task, stage StageTemplate, run StageRun) error {
	if s.Runner == nil {
		return errors.New("task runner not configured")
	}
	if strings.TrimSpace(stage.Agent) == "" {
		return s.failTask(ctx, store, task, run.ID, errors.New("agent stage requires agent"))
	}
	now := time.Now().UTC()
	values := s.promptValues(ctx, store, task, stage, run)
	prompt := BuildAgentPrompt(stage.PromptTemplate, values, TaskControlPromptContext{
		RootID:            task.RootID,
		TaskNumber:        task.TaskNumber,
		CurrentStageIndex: strconv.Itoa(task.CurrentStageIndex),
		CurrentStageName:  stage.Name,
		Enabled:           stage.AgentCanControlStage,
	}) + BuildStageExitContract(task.CurrentStageIndex)
	// 这里是 cwd 真正被取用的地方，所以独立判一次，不依赖上游那几个 gate 有没有跑过
	// （rerun / resume 等路径不一定先过 ensureTaskWorktree）。失效就明确报错，别把
	// 路径流到 agent 启动层再报 chdir 错 —— 那个错看不出是 worktree 被删了。
	if p := strings.TrimSpace(task.WorktreePath); p != "" && !worktreeDirUsable(p) {
		return s.failTask(ctx, store, task, run.ID, fmt.Errorf(
			"worktree 目录已不存在（%s），点「重建 worktree」恢复后再执行", p))
	}
	runtimeRootPath := strings.TrimSpace(task.WorktreePath)
	sessionKey, err := s.Runner.EnsureAgentSession(ctx, AgentStageExecution{
		RootID:          task.RootID,
		RuntimeRootPath: runtimeRootPath,
		Task:            task,
		Stage:           stage,
		Run:             run,
		Prompt:          prompt,
	})
	if err != nil {
		return s.failTask(ctx, store, task, run.ID, err)
	}
	if (strings.TrimSpace(stage.SessionReusePolicy) == "" || stage.SessionReusePolicy == SessionReuseTaskMain) && strings.TrimSpace(task.MainSessionKey) == "" {
		task.MainSessionKey = sessionKey
	}
	run.Status = StageStatusRunning
	run.SessionKey = sessionKey
	run.RenderedPrompt = prompt
	run.StartedAt = now.Format(time.RFC3339Nano)
	task.Status = StatusRunning
	task.AuxFlags = TaskAuxFlags{}
	task.UpdatedAt = now
	if err := store.UpdateTaskAndStageRun(ctx, task, run, TaskEvent{
		ID:         newID("event"),
		TaskID:     task.ID,
		StageRunID: run.ID,
		Type:       "stage_started",
		Payload:    eventPayload(map[string]any{"stage_index": run.StageIndex}),
		CreatedAt:  now,
	}); err != nil {
		return err
	}
	if detail, err := store.GetDetail(ctx, task.ID); err == nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	result, err := s.Runner.RunAgentStage(ctx, AgentStageExecution{
		RootID:          task.RootID,
		RuntimeRootPath: runtimeRootPath,
		Task:            task,
		Stage:           stage,
		Run:             run,
		Prompt:          prompt,
	})
	if err != nil {
		log.Printf("[kanban] agent_stage.session_error root=%s task=%s run=%s err=%v", task.RootID, task.ID, run.ID, err)
		message := strings.TrimSpace(err.Error())
		now = time.Now().UTC()
		task.Status = StatusWaitingUser
		task.AuxFlags.SessionError = message
		task.UpdatedAt = now
		run.Status = StageStatusFail
		run.FinishedAt = now.Format(time.RFC3339Nano)
		if updateErr := store.UpdateTaskAndStageRun(ctx, task, run, TaskEvent{
			ID:         newID("event"),
			TaskID:     task.ID,
			StageRunID: run.ID,
			Type:       "agent_session_error",
			Payload:    eventPayload(map[string]any{"message": message}),
			CreatedAt:  now,
		}); updateErr != nil {
			return updateErr
		}
		if detail, detailErr := store.GetDetail(ctx, task.ID); detailErr == nil {
			s.Runner.TaskUpdated(task.RootID, detail)
		}
		return errStopTaskExecution
	}
	task, err = store.GetTask(ctx, task.ID)
	if err != nil {
		return err
	}
	run, err = store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex)
	if err != nil {
		return err
	}
	if run.Status == StageStatusWaitingUser || task.Status == StatusWaitingUser || task.Status == StatusPaused || isTerminalStatus(task.Status) {
		return nil
	}
	// agent 没显式回报完成就不许算成功：Blocked（它自己说受阻）与 Silent（压根没
	// 回报）都停在 waiting_user，错误摆到卡面上等人处理。AutoAdvance 只看 success，
	// 停在这里就等于下一段不会被自动植进来。
	if result.Outcome != StageOutcomeDone {
		reason := strings.TrimSpace(result.Reason)
		if reason == "" {
			reason = "本段未回报完成：agent 既没输出 [" + stageDoneTag + ":N]，也没说受阻"
		}
		now = time.Now().UTC()
		task.Status = StatusWaitingUser
		task.AuxFlags.SessionError = reason
		task.UpdatedAt = now
		run.Status = StageStatusWaitingUser
		run.FinishedAt = now.Format(time.RFC3339Nano)
		if err := store.UpdateTaskAndStageRun(ctx, task, run, TaskEvent{
			ID:         newID("event"),
			TaskID:     task.ID,
			StageRunID: run.ID,
			Type:       "agent_stage_not_done",
			Payload:    eventPayload(map[string]any{"stage_index": run.StageIndex, "reason": reason}),
			CreatedAt:  now,
		}); err != nil {
			return err
		}
		if detail, err := store.GetDetail(ctx, task.ID); err == nil {
			s.Runner.TaskUpdated(task.RootID, detail)
		}
		return nil
	}
	now = time.Now().UTC()
	run.Status = StageStatusSuccess
	run.FinishedAt = now.Format(time.RFC3339Nano)
	task.UpdatedAt = now
	if err := store.UpdateTaskAndStageRun(ctx, task, run, TaskEvent{
		ID:         newID("event"),
		TaskID:     task.ID,
		StageRunID: run.ID,
		Type:       "stage_succeeded",
		Payload:    eventPayload(map[string]any{"stage_index": run.StageIndex}),
		CreatedAt:  now,
	}); err != nil {
		return err
	}
	if detail, err := store.GetDetail(ctx, task.ID); err == nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return nil
}

func (s *Service) promptValues(ctx context.Context, store *TaskStore, task Task, stage StageTemplate, run StageRun) map[string]string {
	previousInput := strings.TrimSpace(run.Input)
	if previousInput == "" && run.StageIndex > 0 {
		if previous, err := store.LatestStageRun(ctx, task.ID, run.StageIndex-1); err == nil {
			previousInput = previous.Input
		}
	}
	initialInput := previousInput
	if first, err := store.LatestStageRun(ctx, task.ID, 0); err == nil {
		initialInput = first.Input
	}
	return map[string]string{
		"previous_input":     previousInput,
		"task_initial_input": initialInput,
		"task_number":        strconv.Itoa(task.TaskNumber),
	}
}

func (s *Service) waitForUser(ctx context.Context, store *TaskStore, task Task, run StageRun, reason string) error {
	if task.Status == StatusWaitingUser && run.Status == StageStatusWaitingUser {
		return nil
	}
	now := time.Now().UTC()
	task.Status = StatusWaitingUser
	task.UpdatedAt = now
	if run.Status != StageStatusApproved && run.Status != StageStatusSuccess {
		run.Status = StageStatusWaitingUser
	}
	event := TaskEvent{
		ID:         newID("event"),
		TaskID:     task.ID,
		StageRunID: run.ID,
		Type:       "waiting_user",
		Payload:    eventPayload(map[string]any{"reason": reason}),
		CreatedAt:  now,
	}
	if err := store.UpdateTaskAndStageRun(ctx, task, run, event); err != nil {
		return err
	}
	if detail, err := store.GetDetail(ctx, task.ID); err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return nil
}

func (s *Service) finishTask(ctx context.Context, store *TaskStore, task Task, status, eventType, reason string) error {
	now := time.Now().UTC()
	task.Status = status
	task.UpdatedAt = now
	task.CompletedAt = now.Format(time.RFC3339Nano)
	if err := store.UpdateTask(ctx, task); err != nil {
		return err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    task.ID,
		Type:      eventType,
		Payload:   eventPayload(map[string]any{"reason": strings.TrimSpace(reason)}),
		CreatedAt: now,
	})
	if detail, err := store.GetDetail(ctx, task.ID); err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return nil
}

func (s *Service) failTask(ctx context.Context, store *TaskStore, task Task, runID string, err error) error {
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	if updateErr := s.recordTaskError(ctx, store, task, runID, reason); updateErr != nil {
		return updateErr
	}
	return err
}

func (s *Service) recordTaskError(ctx context.Context, store *TaskStore, task Task, runID, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	current, err := store.GetTask(ctx, task.ID)
	if err != nil {
		return err
	}
	current.AuxFlags.SessionError = message
	current.UpdatedAt = time.Now().UTC()
	if err := store.UpdateTask(ctx, current); err != nil {
		return err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:         newID("event"),
		TaskID:     current.ID,
		StageRunID: strings.TrimSpace(runID),
		Type:       "task_error",
		Payload:    eventPayload(map[string]any{"message": message}),
		CreatedAt:  time.Now().UTC(),
	})
	if detail, err := store.GetDetail(ctx, current.ID); err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(current.RootID, detail)
	}
	return nil
}

func (s *Service) moveRelative(ctx context.Context, in MoveInput, delta int, eventType, previousRunStatus string) (TaskDetail, error) {
	store, task, err := s.loadForMove(ctx, in.RootID, in.TaskID)
	if err != nil {
		return TaskDetail{}, err
	}
	target := task.CurrentStageIndex + delta
	if target < 0 || target >= len(task.Stages) {
		return TaskDetail{}, errors.New("target stage out of range")
	}
	if task.CurrentStageIndex < 0 || task.CurrentStageIndex >= len(task.Stages) {
		return TaskDetail{}, errors.New("current stage out of range")
	}
	latest, latestErr := store.LatestStageRun(ctx, task.ID, task.CurrentStageIndex)
	if latestErr != nil {
		return TaskDetail{}, latestErr
	}
	if delta > 0 && latest.Status == StageStatusRunning {
		return TaskDetail{}, errors.New("current stage is running")
	}
	if delta > 0 && !canLeaveStageOnRequest(task.Stages[task.CurrentStageIndex].Role, latest.Status) {
		// 走 canLeaveStageOnRequest 而不是 canAdvanceFromStage：到这里的一定是人点的
		// （引擎自动推进走 moveTo），而 agent 段的 waiting_user（没输出 [STAGE-DONE:N]）
		// 正该由人来判「就这样，推进」。canAdvanceFromStage 是留给引擎的那条更严的线。
		return TaskDetail{}, fmt.Errorf(
			"current stage is %s: 这一段没走完，重跑本段或改任务后再试",
			latest.Status,
		)
	}
	if delta > 0 && stageRequiresCurrentInput(task.Stages[target], task.CurrentStageIndex) && strings.TrimSpace(latest.Input) == "" {
		return TaskDetail{}, errors.New("current stage input required")
	}
	if delta > 0 && task.CreateWorktree && !worktreeDirUsable(task.WorktreePath) {
		updated, err := s.ensureTaskWorktree(ctx, store, task)
		if err != nil {
			if recordErr := s.recordTaskError(ctx, store, task, latest.ID, err.Error()); recordErr != nil {
				return TaskDetail{}, recordErr
			}
			return TaskDetail{}, err
		}
		task = updated
	}
	if previousRunStatus != "" {
		_ = store.UpdateStageRunStatus(ctx, latest.ID, previousRunStatus)
	}
	return s.moveTo(ctx, store, task, target, eventType, previousRunStatus, in.Reason)
}

func stageRequiresCurrentInput(stage StageTemplate, currentStageIndex int) bool {
	prompt := stage.PromptTemplate
	return strings.Contains(prompt, "{previous_input}") ||
		(currentStageIndex == 0 && strings.Contains(prompt, "{task_initial_input}"))
}

func (s *Service) moveTo(ctx context.Context, store *TaskStore, task Task, target int, eventType, previousRunStatus, reason string) (TaskDetail, error) {
	now := time.Now().UTC()
	stage := task.Stages[target]
	status := StatusWaitingUser
	if stage.Role == RoleAgent {
		status = StatusRunning
	}
	task.CurrentStageIndex = target
	task.Status = status
	task.AuxFlags.SessionError = ""
	task.UpdatedAt = now
	run := StageRun{
		ID:         newID("run"),
		TaskID:     task.ID,
		StageIndex: target,
		StageName:  stage.Name,
		Role:       stage.Role,
		Status:     StageStatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if stage.Role == RoleUser {
		run.Status = StageStatusWaitingUser
	}
	event := TaskEvent{
		ID:         newID("event"),
		TaskID:     task.ID,
		StageRunID: run.ID,
		Type:       eventType,
		Payload:    eventPayload(map[string]any{"reason": strings.TrimSpace(reason), "stage_index": target}),
		CreatedAt:  now,
	}
	if err := store.MoveTask(ctx, task, run, event); err != nil {
		return TaskDetail{}, err
	}
	detail, err := store.GetDetail(ctx, task.ID)
	// 换段指针一定要播：卡片上的「当前在第几段 / 什么状态」就挂在这上面。
	// 以前只靠 HTTP handler 事后补，于是 KickPending、handleKanbanTaskBeginFinish
	// 这类非 HTTP 入口推不出去 —— 界面上就停在旧状态。
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(task.RootID, detail)
	}
	return detail, err
}

// setTaskStatus 改任务状态（Pause/Resume/Cancel/Fail 共用）。
// 终态任务不再接受任何状态改写：已归档的任务不能被 Resume/Pause 复活。
func (s *Service) setTaskStatus(ctx context.Context, rootID, taskID, status, eventType, reason string, terminal bool) (TaskDetail, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskDetail{}, err
	}
	if current, getErr := store.GetTask(ctx, taskID); getErr == nil && isTerminalStatus(current.Status) {
		// 状态没变（已经是终态了），刻意不播：推一条与现状相同的 task.updated
		// 只会让每个客户端白做一次重渲染。
		return store.GetDetail(ctx, current.ID)
	}
	if err := store.UpdateTaskStatus(ctx, taskID, status, terminal); err != nil {
		return TaskDetail{}, err
	}
	_ = store.AddEvent(ctx, TaskEvent{
		ID:        newID("event"),
		TaskID:    strings.TrimSpace(taskID),
		Type:      eventType,
		Payload:   eventPayload(map[string]any{"reason": strings.TrimSpace(reason)}),
		CreatedAt: time.Now().UTC(),
	})
	detail, err := store.GetDetail(ctx, taskID)
	// Pause/Resume/Cancel/Fail 的界面反馈全靠这条推送。以前不发、只由 HTTP handler
	// 补，于是任何非 HTTP 调用方推不出去，别的客户端看到的还是暂停前的状态。
	if err == nil && s.Runner != nil {
		s.Runner.TaskUpdated(rootID, detail)
	}
	return detail, err
}

// ensureServiceTask 读取任务并回填旧任务快照（详见 loadForMove）；当前任务字段仅服务端推动（如追加段落）。
func (s *Service) ensureServiceTask(ctx context.Context, rootID string, store *TaskStore, taskID string) (Task, error) {
	store, task, err := s.loadForMove(ctx, rootID, strings.TrimSpace(taskID))
	if err != nil {
		return Task{}, err
	}
	return task, nil
}

func defaultStageName(task Task, index int) string {
	if index < 0 {
		index = 0
	}
	name := strings.TrimSpace(fmt.Sprintf("阶段 %d", index+1))
	return name
}

func (s *Service) loadForMove(ctx context.Context, rootID, taskID string) (*TaskStore, Task, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return nil, Task{}, err
	}
	task, err := store.GetTask(ctx, taskID)
	if err != nil {
		return nil, Task{}, err
	}
	// 兼容：老任务没存任务流水 → 惰性从预设拷贝一次并落盘；此后与模板解耦。
	if len(task.Stages) == 0 && strings.TrimSpace(task.TaskTemplateID) != "" {
		if tmpl, err := s.Templates.GetTaskTemplate(task.TaskTemplateID); err == nil {
			stages := []StageTemplate{}
			for _, ts := range tmpl.Stages {
				stages = append(stages, normalizeStageTemplate(ts.Snapshot))
			}
			task.Stages = stages
			task.UpdatedAt = time.Now().UTC()
			_ = store.UpdateTask(ctx, task)
		}
	}
	return store, task, nil
}

func (s *Service) taskStore(rootID string) (*TaskStore, error) {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return nil, errors.New("root_id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stores == nil {
		s.stores = map[string]*TaskStore{}
	}
	if store := s.stores[rootID]; store != nil {
		return store, nil
	}
	if s.Roots == nil {
		return nil, errors.New("root provider not configured")
	}
	root, err := s.Roots.GetRoot(rootID)
	if err != nil {
		return nil, err
	}
	store, err := NewTaskStore(root)
	if err != nil {
		return nil, err
	}
	s.stores[rootID] = store
	return store, nil
}

func eventPayload(value map[string]any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

type TaskControlPromptContext struct {
	RootID            string
	TaskNumber        int
	CurrentStageIndex string
	CurrentStageName  string
	Enabled           bool
}

func BuildAgentPrompt(template string, values map[string]string, control TaskControlPromptContext) string {
	out := template
	for key, value := range values {
		out = strings.ReplaceAll(out, "{"+key+"}", value)
	}
	if control.Enabled {
		taskNumber := strconv.Itoa(control.TaskNumber)
		out += fmt.Sprintf("\n\nTask control context:\n- root_id: %s\n- task_number: %s\n- current_stage_index: %s\n- current_stage_name: %s\n\nBefore changing the task stage, inspect the current task state.\n\nmindfs %s -task %s\nmindfs %s -task %s -next\nmindfs %s -task %s -prev",
			control.RootID, taskNumber, control.CurrentStageIndex, control.CurrentStageName,
			control.RootID, taskNumber, control.RootID, taskNumber, control.RootID, taskNumber)
	}
	return out
}
