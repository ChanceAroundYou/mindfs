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
	// HygieneStateHint: 又久又大 —— 提示，并在再经过 HintTurns 个用户回合后自动压缩。
	HygieneStateHint = "hint"
	// HygieneStateDisabled: 节点关掉了这个特性。
	HygieneStateDisabled = "disabled"
)

// HygieneConfig 是一个节点上的会话卫生策略。零值等于关闭，由配置层显式打开。
type HygieneConfig struct {
	Enabled        bool
	ActiveSeconds  int
	ContextTokens  int
	HintTurns      int
	IdleGapSeconds int
	// Harness 按 harness 覆盖；零值字段沿用全局值。
	Harness map[string]HygieneConfig
}

// HygieneSnapshot 是判定所需的全部输入，来自会话本身的持久状态与进程内累计。
type HygieneSnapshot struct {
	ActiveSeconds  int
	ContextTokens  int
	Hinted         bool
	TurnsSinceHint int
	AutoFailed     bool
}

// HygieneView 是给前端与调用方的结论。TurnsUntilAutoCompact 为 -1 表示这一档
// 不会自动压缩（关闭、只大不久、或 HintTurns 未配置）。
type HygieneView struct {
	State                 string `json:"state"`
	Enabled               bool   `json:"enabled"`
	ActiveSeconds         int    `json:"active_seconds"`
	ContextTokens         int    `json:"context_tokens"`
	HintTurns             int    `json:"hint_turns"`
	TurnsSinceHint        int    `json:"turns_since_hint"`
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
		HintTurns:             cfg.HintTurns,
		TurnsSinceHint:        snap.TurnsSinceHint,
		TurnsUntilAutoCompact: -1,
		AutoFailed:            snap.AutoFailed,
	}
	if !cfg.Enabled {
		view.State = HygieneStateDisabled
		return view
	}
	long := snap.ActiveSeconds >= cfg.ActiveSeconds
	big := snap.ContextTokens >= cfg.ContextTokens
	switch {
	case long && big:
		view.State = HygieneStateHint
	case big:
		view.State = HygieneStateNotice
	default:
		view.State = HygieneStateNormal
	}
	if view.State == HygieneStateHint && cfg.HintTurns > 0 {
		remaining := cfg.HintTurns - snap.TurnsSinceHint
		if remaining < 0 {
			remaining = 0
		}
		view.TurnsUntilAutoCompact = remaining
		// 上一次自动压缩失败后不再重试：通道坏了的话，每轮重试只是把失败变成噪声。
		view.AutoCompactDue = remaining == 0 && !snap.AutoFailed
	}
	return view
}

type hygieneRecord struct {
	lastSeen       time.Time
	activeSeconds  float64
	hinted         bool
	turnsSinceHint int
	autoFailed     bool
	compactPending bool
	compactError   string
	// contextStale 表示压缩事件已到、但新的 token_count 还没到。此时持久化的
	// context_tokens 仍是压缩前的旧值，不能拿它再判一次自动压缩。
	contextStale bool
	// autoAttempted 保证同一段提示期只派发一次自动压缩：派发是异步的，
	// 派发后、压缩完成前的每一轮都会继续算出「已到期」，不拦就会连环开火。
	autoAttempted bool
}

// HygieneTracker 保存进程内的会话卫生状态。
//
// 只存「增量」：活跃时长按观测间隔累计，提示轮数按回合结束推进。会话的上下文
// 大小每次从调用方传入（它来自已持久化的 last_context_window），所以重启后即使
// 内存状态清零，下一次观测也会立刻按真实的时长/上下文重新判定。
type HygieneTracker struct {
	mu      sync.Mutex
	cfg     HygieneConfig
	records map[string]*hygieneRecord
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
	if override.ContextTokens > 0 {
		cfg.ContextTokens = override.ContextTokens
	}
	if override.HintTurns > 0 {
		cfg.HintTurns = override.HintTurns
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
}

// Observe 读取当前结论并按需要推进状态。
//
// turnEnded=true 表示这是一个用户回合刚刚结束的时刻：若提示已经出现过，轮数 +1。
// 提示刚出现的那一轮不计数，用户看到提示时还剩完整的 N 轮。
func (t *HygieneTracker) Observe(rootID, sessionKey, harness string, contextTokens int, turnEnded bool) HygieneView {
	if t == nil {
		return EvaluateHygiene(HygieneConfig{}, HygieneSnapshot{ContextTokens: contextTokens})
	}
	key := hygieneScope(rootID, sessionKey)
	cfg := t.ConfigFor(harness)
	t.mu.Lock()
	defer t.mu.Unlock()
	rec := t.recordLocked(key)
	if rec.contextStale {
		// stale 期间不推进轮数、不开新压缩；读口仍报告当前档位，用户看得见。
		snap := HygieneSnapshot{
			ActiveSeconds:  int(rec.activeSeconds),
			ContextTokens:  contextTokens,
			Hinted:         rec.hinted,
			TurnsSinceHint: rec.turnsSinceHint,
			AutoFailed:     rec.autoFailed,
		}
		view := EvaluateHygiene(cfg, snap)
		view.AutoCompactDue = false
		return view
	}
	snap := HygieneSnapshot{
		ActiveSeconds:  int(rec.activeSeconds),
		ContextTokens:  contextTokens,
		Hinted:         rec.hinted,
		TurnsSinceHint: rec.turnsSinceHint,
		AutoFailed:     rec.autoFailed,
	}
	view := EvaluateHygiene(cfg, snap)
	newlyHinted := view.State == HygieneStateHint && !rec.hinted
	switch {
	case view.State != HygieneStateHint:
		rec.hinted = false
		rec.turnsSinceHint = 0
		rec.autoFailed = false
		rec.autoAttempted = false
	case newlyHinted:
		rec.hinted = true
		rec.turnsSinceHint = 0
		rec.autoAttempted = false
	}
	if turnEnded && rec.hinted && !newlyHinted {
		rec.turnsSinceHint++
	}
	snap.Hinted = rec.hinted
	snap.TurnsSinceHint = rec.turnsSinceHint
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
// 读路径必须完全无副作用：前端每次刷新胶囊都会调它，而它一旦把「提示刚到」
// 的那一格记成 hinted，紧接着的回合结束就会被算成「提示之后的第 1 轮」——
// 用户看到的提示窗口整体少一轮，实测会出现「提示和自动压缩同一刻发生」。
// 它也不建记录：没活动过的会话不该因为有人点开看一眼就多一条内存状态。
func (t *HygieneTracker) Snapshot(rootID, sessionKey, harness string, contextTokens int) HygieneView {
	if t == nil {
		return EvaluateHygiene(HygieneConfig{}, HygieneSnapshot{ContextTokens: contextTokens})
	}
	cfg := t.ConfigFor(harness)
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := HygieneSnapshot{ContextTokens: contextTokens}
	if rec := t.records[hygieneScope(rootID, sessionKey)]; rec != nil {
		snap.ActiveSeconds = int(rec.activeSeconds)
		snap.Hinted = rec.hinted
		snap.TurnsSinceHint = rec.turnsSinceHint
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
// 清掉提示与计数，下一次变长要重新积累完整的 N 轮。
func (t *HygieneTracker) MarkCompacted(rootID, sessionKey string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	rec := t.recordLocked(hygieneScope(rootID, sessionKey))
	rec.hinted = false
	rec.turnsSinceHint = 0
	rec.autoFailed = false
	rec.autoAttempted = false
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
