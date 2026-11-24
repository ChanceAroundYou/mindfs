package usecase

import (
	"strings"
	"sync"
	"time"
)

// 会话卫生三档。名字会直接进 JSON，前端按它决定胶囊的颜色与可点的动作。
const (
	// HygieneStateNormal: 既不够久也不够大，胶囊维持原样。
	HygieneStateNormal = "normal"
	// HygieneStateNotice: 上下文已经很大但会话还不算久 —— 只提示，永不自动压缩。
	HygieneStateNotice = "notice"
	// HygieneStateHint: 又久又大 —— 提示，并在连续 OverLineTurns 个用户回合都过线后自动压缩。
	HygieneStateHint = "hint"
	// HygieneStateDisabled: 节点关掉了这个特性。
	HygieneStateDisabled = "disabled"
)

// hygieneDefaultContextWindow 是上下文窗口未知时的判定基准。窗口由 harness 上报，
// 但旧会话、导入会话与部分中转都可能没有这个数；用一个固定基准比「不知道就不判」
// 更安全 —— 后者会让这些会话永远进不了 hint 档。
const hygieneDefaultContextWindow = 200000

// HygieneConfig 是一个节点上的会话卫生策略。零值等于关闭，由配置层显式打开。
type HygieneConfig struct {
	Enabled       bool
	ActiveSeconds int
	// ContextPercent 是「大」的阈值，按上下文窗口的百分比算。相对阈值让同一策略
	// 在 200k 与 272k 窗口上含义一致；绝对 token 数在两种窗口下差出一整个会话。
	ContextPercent int
	// OverLineTurns 是自动压缩前需要的**连续**过线用户回合数。某一轮不满足
	// 「又久又大」就清零重来 —— 否则聊到一半缩回去、过很久再涨回来也会立刻被压。
	OverLineTurns  int
	IdleGapSeconds int
	// Harness 按 harness 覆盖；零值字段沿用全局值。
	Harness map[string]HygieneConfig
}

// HygieneSnapshot 是判定所需的全部输入，来自会话本身的持久状态与进程内累计。
type HygieneSnapshot struct {
	ActiveSeconds int
	ContextTokens int
	// ContextWindow 是 harness 上报的窗口；<=0 时按 hygieneDefaultContextWindow 算。
	ContextWindow int
	// OverLineTurns 是已连续过线的用户回合数（含当前这一轮，如果它过线）。
	OverLineTurns int
	AutoFailed    bool
}

// HygieneView 是给前端与调用方的结论。TurnsUntilAutoCompact 为 -1 表示这一档
// 不会自动压缩（关闭、只大不久、或 OverLineTurns 未配置）。
type HygieneView struct {
	State                 string `json:"state"`
	Enabled               bool   `json:"enabled"`
	ActiveSeconds         int    `json:"active_seconds"`
	ContextTokens         int    `json:"context_tokens"`
	ContextWindow         int    `json:"context_window"`
	ContextPercent        int    `json:"context_percent"`
	ContextLimit          int    `json:"context_limit"`
	OverLineTurns         int    `json:"over_line_turns"`
	TurnsOverLine         int    `json:"turns_over_line"`
	TurnsUntilAutoCompact int    `json:"turns_until_auto_compact"`
	AutoCompactDue        bool   `json:"auto_compact_due"`
	AutoFailed            bool   `json:"auto_failed"`
	// CompactPending 表示已经排了一个 /compact 回合但还没收到 harness 的压缩事件。
	// 手动按钮按它保持禁用，避免真实压缩要几分钟时界面提前恢复成「立即压缩」。
	CompactPending bool   `json:"compact_pending"`
	CompactError   string `json:"compact_error"`
}

// EvaluateHygiene 是纯函数：同样的输入永远给出同样的三档结论。
//
// 「久」看活跃时长（空闲间隔已在累计时截断），「大」看会话最后一次上报的上下文
// token。两者同时成立才进入 hint 档；只大是 notice（用户自己决定），只久什么
// 都不做 —— 长时间的低负载对话不值得为压缩付出一次摘要请求。
func EvaluateHygiene(cfg HygieneConfig, snap HygieneSnapshot) HygieneView {
	view := HygieneView{
		Enabled:               cfg.Enabled,
		ActiveSeconds:         snap.ActiveSeconds,
		ContextTokens:         snap.ContextTokens,
		ContextWindow:         hygieneContextWindowFor(snap.ContextWindow),
		ContextPercent:        cfg.ContextPercent,
		ContextLimit:          hygieneContextLimit(cfg, snap.ContextWindow),
		OverLineTurns:         cfg.OverLineTurns,
		TurnsOverLine:         snap.OverLineTurns,
		TurnsUntilAutoCompact: -1,
		AutoFailed:            snap.AutoFailed,
	}
	if !cfg.Enabled {
		view.State = HygieneStateDisabled
		return view
	}
	long := cfg.ActiveSeconds > 0 && snap.ActiveSeconds >= cfg.ActiveSeconds
	limit := hygieneContextLimit(cfg, snap.ContextWindow)
	big := limit > 0 && snap.ContextTokens >= limit
	switch {
	case long && big:
		view.State = HygieneStateHint
	case big:
		view.State = HygieneStateNotice
	default:
		view.State = HygieneStateNormal
	}
	if view.State == HygieneStateHint && cfg.OverLineTurns > 0 {
		remaining := cfg.OverLineTurns - snap.OverLineTurns
		if remaining < 0 {
			remaining = 0
		}
		view.TurnsUntilAutoCompact = remaining
		// 上一次自动压缩失败后不再重试：通道坏了的话，每轮重试只是把失败变成噪声。
		view.AutoCompactDue = remaining == 0 && !snap.AutoFailed
	}
	return view
}

// hygieneContextLimit 把百分比阈值换算成 token 数。百分比未配置时返回 0，
// 调用方据此判定「大」永远不成立 —— 零值配置等于关闭，不做隐式默认。
func hygieneContextLimit(cfg HygieneConfig, window int) int {
	if cfg.ContextPercent <= 0 {
		return 0
	}
	return hygieneContextWindowFor(window) * cfg.ContextPercent / 100
}

// hygieneContextWindowFor 把缺失的窗口换成判定基准；上报过的窗口原样返回。
func hygieneContextWindowFor(window int) int {
	if window <= 0 {
		return hygieneDefaultContextWindow
	}
	return window
}

type hygieneRecord struct {
	lastSeen       time.Time
	activeSeconds  float64
	overLineTurns  int
	autoFailed     bool
	compactPending bool
	compactError   string
	// contextStale 表示压缩事件已到、但新的 token_count 还没到。此时持久化的
	// context_tokens 仍是压缩前的旧值，不能拿它再判一次自动压缩。
	contextStale bool
	// autoAttempted 保证同一段提示期只派发一次自动压缩：派发是异步的，
	// 派发后、压缩完成前的每一轮都会继续算出「已到期」，不拦就会连环开火。
	autoAttempted bool
	// overflowResume 是「溢出压缩成功后重发这条消息」的待办。只在内存里：
	// 压缩期间进程重启就丢掉，用户看到原错误、手动重发即可（与没有本特性时一致）。
	overflowResume *OverflowResumeSpec
}

// HygieneTracker 保存会话卫生状态：进程内是热缓存，活跃时长与最近观测时刻
// 另有一份按会话落盘的快照。上下文大小每次从调用方传入（它来自已持久化的
// last_context_window），所以重启后下一次观测就能按真实时长/上下文重新判定。
type HygieneTracker struct {
	mu      sync.Mutex
	cfg     HygieneConfig
	records map[string]*hygieneRecord
	store   *hygieneStore
}

func NewHygieneTracker(cfg HygieneConfig) *HygieneTracker {
	if cfg.IdleGapSeconds <= 0 {
		cfg.IdleGapSeconds = 30 * 60
	}
	return &HygieneTracker{cfg: cfg, records: map[string]*hygieneRecord{}}
}

// ConfigFor 返回某个 harness 的有效配置；覆盖只替换非零字段。
func (t *HygieneTracker) ConfigFor(harness string) HygieneConfig {
	if t == nil {
		return HygieneConfig{}
	}
	cfg := t.cfg
	cfg.Harness = nil
	override, ok := t.cfg.Harness[strings.ToLower(strings.TrimSpace(harness))]
	if !ok {
		return cfg
	}
	if override.ActiveSeconds > 0 {
		cfg.ActiveSeconds = override.ActiveSeconds
	}
	if override.ContextPercent > 0 {
		cfg.ContextPercent = override.ContextPercent
	}
	if override.OverLineTurns > 0 {
		cfg.OverLineTurns = override.OverLineTurns
	}
	if override.IdleGapSeconds > 0 {
		cfg.IdleGapSeconds = override.IdleGapSeconds
	}
	return cfg
}

func hygieneScope(rootID, sessionKey string) string {
	return rootID + "\x00" + sessionKey
}

// ObserveAt 记录一次「这个会话此刻有活动」，并按截断后的间隔累计活跃时长。
// 空闲超过 IdleGap 的那一段整段丢弃：挂着过夜不是「持续久」。
func (t *HygieneTracker) ObserveAt(rootID, sessionKey, harness string, at time.Time) {
	if t == nil {
		return
	}
	key := hygieneScope(rootID, sessionKey)
	cfg := t.ConfigFor(harness)
	t.mu.Lock()
	defer t.mu.Unlock()
	rec := t.recordLocked(key)
	if !rec.lastSeen.IsZero() {
		gap := at.Sub(rec.lastSeen)
		if gap > 0 && gap <= time.Duration(cfg.IdleGapSeconds)*time.Second {
			rec.activeSeconds += gap.Seconds()
		}
	}
	rec.lastSeen = at
	t.persistLocked(key, rec)
}

// Observe 读取当前结论并按需要推进状态。
//
// turnEnded=true 表示这是一个用户回合刚刚结束的时刻。过线（又久又大）的回合
// 把连续计数 +1，**含刚过线的那一轮**——「过线运行 3 轮」就是从第 3 轮结束开始压，
// 而不是提示出现后再白送 3 轮。某一轮不再过线就清零：中断过的话重新攒。
func (t *HygieneTracker) Observe(rootID, sessionKey, harness string, contextTokens, contextWindow int, turnEnded bool) HygieneView {
	snap := HygieneSnapshot{ContextTokens: contextTokens, ContextWindow: contextWindow}
	if t == nil {
		return EvaluateHygiene(HygieneConfig{}, snap)
	}
	key := hygieneScope(rootID, sessionKey)
	cfg := t.ConfigFor(harness)
	t.mu.Lock()
	defer t.mu.Unlock()
	rec := t.recordLocked(key)
	snap.ActiveSeconds = int(rec.activeSeconds)
	snap.OverLineTurns = rec.overLineTurns
	snap.AutoFailed = rec.autoFailed
	view := EvaluateHygiene(cfg, snap)
	if rec.contextStale {
		// stale 期间不推进轮数、不开新压缩；读口仍报告当前档位，用户看得见。
		view.AutoCompactDue = false
		return view
	}
	if view.State != HygieneStateHint {
		rec.overLineTurns = 0
		rec.autoFailed = false
		rec.autoAttempted = false
	} else if turnEnded {
		rec.overLineTurns++
	}
	snap.OverLineTurns = rec.overLineTurns
	snap.AutoFailed = rec.autoFailed
	view = EvaluateHygiene(cfg, snap)
	// 到期只报一次：调用方据此派发一次自动压缩，后续轮次等结果而不是重复开火。
	if view.AutoCompactDue {
		if rec.autoAttempted {
			view.AutoCompactDue = false
		} else {
			rec.autoAttempted = true
		}
	}
	return view
}

// Snapshot 是不推进任何状态的读取，给胶囊这类只读路径用。
//
// 读路径必须完全无副作用：前端每次刷新胶囊都会调它，而它一旦把「刚过线」
// 的那一格记成已计数的回合，紧接着的回合结束就会凭空多算一轮 ——
// 用户看到的窗口整体少一轮，实测会出现「提示和自动压缩同一刻发生」。
// 它也不建记录：没活动过的会话不该因为有人点开看一眼就多一条内存状态。
func (t *HygieneTracker) Snapshot(rootID, sessionKey, harness string, contextTokens, contextWindow int) HygieneView {
	snap := HygieneSnapshot{ContextTokens: contextTokens, ContextWindow: contextWindow}
	if t == nil {
		return EvaluateHygiene(HygieneConfig{}, snap)
	}
	cfg := t.ConfigFor(harness)
	t.mu.Lock()
	defer t.mu.Unlock()
	if rec := t.records[hygieneScope(rootID, sessionKey)]; rec != nil {
		snap.ActiveSeconds = int(rec.activeSeconds)
		snap.OverLineTurns = rec.overLineTurns
		snap.AutoFailed = rec.autoFailed
	}
	view := EvaluateHygiene(cfg, snap)
	if rec := t.records[hygieneScope(rootID, sessionKey)]; rec != nil {
		view.CompactPending = rec.compactPending
		view.CompactError = rec.compactError
	}
	return view
}

// MarkCompacted 在任意一次压缩之后调用（harness 自己的或我们触发的）：
// 清掉提示与计数，下一次变长要重新积累完整的 N 轮。活跃时长也从这次压缩
// 重新起算 —— 「久」表示的是距上次压缩的累计活跃时长，不是进程活了多久。
func (t *HygieneTracker) MarkCompacted(rootID, sessionKey string) {
	t.markCompactedAt(rootID, sessionKey, time.Now())
}

func (t *HygieneTracker) markCompactedAt(rootID, sessionKey string, at time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	key := hygieneScope(rootID, sessionKey)
	rec := t.recordLocked(key)
	rec.overLineTurns = 0
	rec.autoFailed = false
	rec.autoAttempted = false
	rec.activeSeconds = 0
	rec.lastSeen = at
	t.persistLocked(key, rec)
}

// MarkContextStale 在压缩事件已到但新 token_count 未到时调用。
// 旧值还挂在 session.LastContextWindow 上，此时任何自动压缩判定都会重复开火。
func (t *HygieneTracker) MarkContextStale(rootID, sessionKey string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordLocked(hygieneScope(rootID, sessionKey)).contextStale = true
}

// ClearContextStale 在新 token_count 写入后调用，恢复正常的轮次判定。
func (t *HygieneTracker) ClearContextStale(rootID, sessionKey string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordLocked(hygieneScope(rootID, sessionKey)).contextStale = false
}

// ContextStale 给发送路径用：只有确认拿到新上下文后才推进 hygiene。
func (t *HygieneTracker) ContextStale(rootID, sessionKey string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.recordLocked(hygieneScope(rootID, sessionKey)).contextStale
}

// MarkAutoFailed 记录一次自动压缩失败；在压缩成功或会话离开提示带之前不再重试。
func (t *HygieneTracker) MarkAutoFailed(rootID, sessionKey string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recordLocked(hygieneScope(rootID, sessionKey)).autoFailed = true
}

// MarkCompactPending 记录一次压缩回合已经排上：读口据此让「立即压缩」按钮
// 一直禁用到收到 harness 的压缩事件，而不是按前端猜的 8 秒就恢复。
func (t *HygieneTracker) MarkCompactPending(rootID, sessionKey string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	rec := t.recordLocked(hygieneScope(rootID, sessionKey))
	rec.compactPending = true
	rec.compactError = ""
}

// FinishCompact 结束这一轮压缩；errText 非空时留一份可读的原因给胶囊。
func (t *HygieneTracker) FinishCompact(rootID, sessionKey, errText string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	rec := t.recordLocked(hygieneScope(rootID, sessionKey))
	rec.compactPending = false
	rec.compactError = compactErrorText(errText)
}

// MarkOverflowResume 登记一次「压缩成功后重发」。后到覆盖先到：同一会话同一时刻
// 只可能有一条被溢出打断的回合（发送锁串行），覆盖即最新的那条。
func (t *HygieneTracker) MarkOverflowResume(rootID, sessionKey string, spec OverflowResumeSpec) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	copied := spec
	t.recordLocked(hygieneScope(rootID, sessionKey)).overflowResume = &copied
}

// TakeOverflowResume 取出并清除待办；调用方负责真正执行重发。
func (t *HygieneTracker) TakeOverflowResume(rootID, sessionKey string) (OverflowResumeSpec, bool) {
	if t == nil {
		return OverflowResumeSpec{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	rec := t.recordLocked(hygieneScope(rootID, sessionKey))
	if rec.overflowResume == nil {
		return OverflowResumeSpec{}, false
	}
	spec := *rec.overflowResume
	rec.overflowResume = nil
	return spec, true
}

// DropOverflowResume 清掉待办并报告「原来确实有一条」，供调用方决定是否记日志。
func (t *HygieneTracker) DropOverflowResume(rootID, sessionKey string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	rec := t.recordLocked(hygieneScope(rootID, sessionKey))
	if rec.overflowResume == nil {
		return false
	}
	rec.overflowResume = nil
	return true
}

// HasOverflowResume 只读查询，测试与日志用。
func (t *HygieneTracker) HasOverflowResume(rootID, sessionKey string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.recordLocked(hygieneScope(rootID, sessionKey)).overflowResume != nil
}

func compactErrorText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	const maxRunes = 240
	if runes := []rune(text); len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return text
}

func (t *HygieneTracker) recordLocked(key string) *hygieneRecord {
	rec := t.records[key]
	if rec == nil {
		rec = &hygieneRecord{}
		t.records[key] = rec
	}
	return rec
}
