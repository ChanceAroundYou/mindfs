package controlsync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Mode 是控制面在本地承担什么角色。四态由服务端持有并持久化，前端只读。
type Mode string

const (
	// ModeOnline：主节点可用，页面照出、控制面写请求转发给主节点。
	ModeOnline Mode = "online"
	// ModeOfflineLocal：主节点不可达，控制面读写都落本地副本。
	ModeOfflineLocal Mode = "offline-local"
	// ModeReconciling：主节点恢复，正在把本地改动并回去（按记录三方合并）。
	ModeReconciling Mode = "reconciling"
	// ModeConflictReview：两侧都改了同一批记录，该域冻结等人裁决。
	ModeConflictReview Mode = "conflict-review"
)

// 探测节奏：10 秒一次、3 秒超时；3 次失败降级、2 次成功升级。
//
// 这几个数是用户可感知的时延预算（spec §7 的 A2：断开后 ≤30 秒进入 offline-local），
// 不是随手取的：3 × 10s 正好压在上限内。
const (
	ProbeInterval       = 10 * time.Second
	ProbeTimeout        = 3 * time.Second
	DegradeAfterFailure = 3
	UpgradeAfterSuccess = 2
)

// Prober 判断主节点是否可用。测试注入假实现；生产用 HTTPProber。
type Prober interface {
	Probe(ctx context.Context) error
}

// HTTPProber 用 GET <primary>/api/control-sync/head 判活：2xx 即视为可用。
//
// 只判状态码、不解析响应体：探活的意义是「主节点在不在」，把 head 解析也压进来
// 会让一条格式变更（比如多一个字段）表现成「主节点挂了」。
type HTTPProber struct {
	base       *url.URL
	client     *http.Client
	token      string
	instanceID string
}

// NewHTTPProber 构造探活器。token 必须与服务端一致：主节点的 /head 是要鉴权的，
// 不带 token 会得到 401，那会被当成「主节点不可用」而误判降级。
func NewHTTPProber(primaryURL, caFile, token, instanceID string) (*HTTPProber, error) {
	base, err := parsePrimaryURL(primaryURL)
	if err != nil {
		return nil, err
	}
	client, err := newSyncHTTPClient(caFile)
	if err != nil {
		return nil, err
	}
	return &HTTPProber{
		base:       base,
		client:     client,
		token:      strings.TrimSpace(token),
		instanceID: strings.TrimSpace(instanceID),
	}, nil
}

func (p *HTTPProber) Probe(ctx context.Context) error {
	u := *p.base
	u.Path = joinSyncPath(p.base.Path, "/api/control-sync/head")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("controlsync: build probe: %w", err)
	}
	// 探活也走签名：主节点只注册了鉴权后的 /head，裸 bearer 会被 401，
	// 而 401 在状态机里与「主节点不可达」是同一个降级信号。
	if err := SignSyncRequest(req, p.token, p.instanceID, nil, time.Now()); err != nil {
		return fmt.Errorf("controlsync: sign probe: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("controlsync: probe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("controlsync: probe: status %d", resp.StatusCode)
	}
	return nil
}

// Status 是 GET /api/control-sync/status 的载荷。
type Status struct {
	Mode    Mode  `json:"mode"`
	BaseRev int64 `json:"base_rev"`
	// PrimaryReachable 是最近一次探活的结果。它比 mode 更及时：降级要连续 3 次
	// 失败，而「主节点现在通不通」一次探测就能回答。
	PrimaryReachable bool `json:"primary_reachable"`
	// PendingCount 是本机待推送的本地改动条数；ConflictCount 是未裁决冲突条数。
	// 两个数字都实时查库：缓存值会让用户看到早就不存在的冲突卡。
	PendingCount  int `json:"pending_count"`
	ConflictCount int `json:"conflict_count"`
	// BlockedReason 非空表示追平被锁住（主节点身份或 schema 不符）。此时探测可能
	// 一直是通的、模式却是 offline-local —— 横幅要照这个字段解释原因。
	BlockedReason string `json:"blocked_reason,omitempty"`
	// BlockedCode 是上面那条原因的结构化代码（见 blocked.go）。前端用它决定给不给
	// 「接受新主节点」入口 —— 去猜中文/英文诊断串会在改一个字之后静默失效。
	BlockedCode   string    `json:"blocked_code,omitempty"`
	LastPrimaryAt time.Time `json:"last_primary_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
	// RecentEvents 是最近几次模式迁移（最新在前）。spec §7 要求降级/升级留下可见事件，
	// 前端横幅就用它显示「什么时候变的、为什么」，并用事件 id 做一次性提醒的幂等键。
	RecentEvents []ModeEvent `json:"recent_events,omitempty"`
}

// ModeController 拥有模式状态机：计数、迁移、持久化、对外状态。
//
// 状态落 state 表而不是内存：边缘重启后必须知道自己仍在 offline-local，
// 否则会以 online 身份启动、把控制面写请求全部转给一台仍然不可达的主节点。
type ModeController struct {
	store *Store
	probe Prober
	now   func() time.Time

	// Interval/Timeout 只给测试缩短；生产用上面的常量。
	Interval time.Duration
	Timeout  time.Duration

	// OnModeChange 在模式真翻转时被调用（前端横幅与系统事件由接线方落）。
	OnModeChange func(from, to Mode)

	// applyMu 串行化「判定 + 改内存 + 落库 + 通知」整段迁移。
	//
	// 模式有两个驱动方：探测循环（Observe）与追平收尾（CompleteReconcile）。只靠
	// m.mu 保护单个字段会让两边的落库顺序颠倒（内存 online、库里 offline-local，
	// 重启后回到错误状态），所以迁移必须整段排队。Mode() 不取它，转发中间件
	// 不会被一次库写阻塞。
	applyMu sync.Mutex

	mu        sync.Mutex
	mode      Mode
	reachable bool
	// blocked 是追平锁：主节点身份/schema 不符时由 Runtime 置上，探测成功也不许
	// 自动升级。没有它，降级到 offline-local 后 20 秒就会被探活拉回 reconciling，
	// 然后又撞上同一个不符 —— 模式在两种之间来回跳，等于没停下来。
	blocked   string
	failures  int
	successes int
}

func NewModeController(ctx context.Context, store *Store, probe Prober) (*ModeController, error) {
	if store == nil {
		return nil, errors.New("controlsync: store required")
	}
	state, err := store.LoadState(ctx)
	if err != nil {
		return nil, err
	}
	mode := Mode(state.Mode)
	if mode == "" {
		mode = ModeOnline
	}
	if !validMode(mode) {
		return nil, fmt.Errorf("controlsync: unknown persisted mode %q", state.Mode)
	}
	// 锁着就必须是本地模式：库里写着 online/reconciling 但上次是被拒着停下来的，
	// 直接信库会让重启后的第一拍就去合并一台已经换掉的主节点。
	blocked := strings.TrimSpace(state.BlockedReason)
	if blocked != "" {
		mode = ModeOfflineLocal
	}
	return &ModeController{
		store:    store,
		probe:    probe,
		now:      time.Now,
		Interval: ProbeInterval,
		Timeout:  ProbeTimeout,
		mode:     mode,
		blocked:  blocked,
	}, nil
}

func validMode(m Mode) bool {
	switch m {
	case ModeOnline, ModeOfflineLocal, ModeReconciling, ModeConflictReview:
		return true
	}
	return false
}

// Mode 返回当前模式（读内存，供转发中间件每请求判断）。
func (m *ModeController) Mode() Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

// Status 读库里的水位/时间/错误，拼上内存里的当前模式与最近一次探测结果。
//
// 计数一律实时查库：它们是用户判断「还有多少要处理」的唯一来源，读失败就报错，
// 不能把读不出来的东西显示成 0。
func (m *ModeController) Status(ctx context.Context) (Status, error) {
	state, err := m.store.LoadState(ctx)
	if err != nil {
		return Status{}, err
	}
	pending, err := m.store.CountPending(ctx)
	if err != nil {
		return Status{}, err
	}
	conflicts, err := m.store.CountOpenConflicts(ctx)
	if err != nil {
		return Status{}, err
	}
	// 事件读不出来就报错，不返回空列表：空列表在界面上等于「从没变过模式」，
	// 而真相可能是「刚刚断过又恢复」—— 把读失败伪装成平静比报错更糟。
	events, err := m.store.ListModeEvents(ctx, 5)
	if err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	mode, reachable, blocked := m.mode, m.reachable, m.blocked
	m.mu.Unlock()
	return Status{
		Mode:             mode,
		BaseRev:          state.BaseRev,
		PrimaryReachable: reachable,
		PendingCount:     pending,
		ConflictCount:    conflicts,
		BlockedReason:    blocked,
		BlockedCode:      BlockedCode(blocked),
		LastPrimaryAt:    state.LastPrimaryAt,
		LastError:        state.LastError,
		RecentEvents:     events,
	}, nil
}

// Observe 记录一次探测结果并推进状态机，返回迁移后的模式。
//
// conflict-review 不受探测结果影响：冲突要人裁决，主节点掉线不会让冲突消失。
func (m *ModeController) Observe(success bool) Mode {
	m.applyMu.Lock()
	m.mu.Lock()
	if success {
		m.successes++
		m.failures = 0
	} else {
		m.failures++
		m.successes = 0
	}
	// reachable 是「上一次探测通不通」，与模式不是一回事：降级要连续 3 次失败，
	// 而横幅在第一失败时就该改口说主节点不通了。
	m.reachable = success
	next := m.mode
	switch m.mode {
	case ModeOnline, ModeReconciling:
		if m.failures >= DegradeAfterFailure {
			next = ModeOfflineLocal
		}
	case ModeOfflineLocal:
		// 锁着就不许自动升级：探活只说明「对面有台主节点在应答」，不说明它还是
		// 我们记下的那一台。升级与否由 AdoptPrimary（人工确认）或 Runtime 解锁。
		if m.successes >= UpgradeAfterSuccess && m.blocked == "" {
			next = ModeReconciling
		}
	}
	from := m.mode
	m.mode = next
	m.mu.Unlock()

	now := m.now()
	errText := ""
	if !success {
		errText = "primary unreachable"
	}
	if err := m.store.RecordProbe(context.Background(), now, errText); err != nil {
		// 状态表写失败不阻断状态机：内存模式仍然正确，下一拍会再写一次。
		_ = err
	}
	if from != next {
		_ = m.store.SetMode(context.Background(), next)
	}
	m.applyMu.Unlock()
	m.transition(from, next)
	return next
}

// ProbeOnce 当场探一次并交给状态机。
func (m *ModeController) ProbeOnce(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, m.Timeout)
	defer cancel()
	err := m.probe.Probe(probeCtx)
	m.Observe(err == nil)
	return err
}

// Run 每 Interval 探一次，直到 ctx 结束。
func (m *ModeController) Run(ctx context.Context) {
	ticker := time.NewTicker(m.Interval)
	defer ticker.Stop()
	for {
		if err := m.ProbeOnce(ctx); err != nil && ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// SwitchLocal 是用户显式动作：立刻切本地模式，不等探测计数。
func (m *ModeController) SwitchLocal(ctx context.Context) error {
	m.applyMu.Lock()
	m.mu.Lock()
	from := m.mode
	m.mode = ModeOfflineLocal
	m.failures, m.successes = 0, 0
	m.mu.Unlock()
	err := m.store.SetMode(ctx, ModeOfflineLocal)
	m.applyMu.Unlock()
	if err != nil {
		return err
	}
	m.transition(from, ModeOfflineLocal)
	return nil
}

// Retry 是用户显式动作：当场探主节点，通就进 reconciling，不通留在本地模式。
//
// 这里刻意不走 Observe 的阈值：用户已经明确要求「现在试一次」，
// 再攒两次成功才动会让按钮看起来没反应。它也是**唯一**能绕过追平锁的自动路径以外
// 的入口：锁着时用户点重试，就给一次真正的追平机会（身份核对会在追平开头重做，
// 不符会立刻重新锁上并刷新原因），而不是让按钮看着点了没动静。
func (m *ModeController) Retry(ctx context.Context) (Mode, error) {
	probeCtx, cancel := context.WithTimeout(ctx, m.Timeout)
	defer cancel()
	err := m.probe.Probe(probeCtx)
	next := ModeOfflineLocal
	if err == nil {
		next = ModeReconciling
	}
	m.applyMu.Lock()
	m.mu.Lock()
	from := m.mode
	m.mode = next
	m.failures, m.successes = 0, 0
	m.mu.Unlock()
	saveErr := m.store.SetMode(ctx, next)
	m.applyMu.Unlock()
	if saveErr != nil {
		return next, saveErr
	}
	m.transition(from, next)
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	if recordErr := m.store.RecordProbe(ctx, m.now(), errText); recordErr != nil {
		return next, recordErr
	}
	return next, err
}

// BlockLocal 锁住追平并停在本地模式：主节点身份或 schema 不符时由 Runtime 调用。
//
// 锁会持久化，重启后不得回到 online/reconciling —— 那只会再撞一次同一个不符。
func (m *ModeController) BlockLocal(ctx context.Context, reason string) (Mode, error) {
	m.applyMu.Lock()
	m.mu.Lock()
	from := m.mode
	m.blocked = reason
	m.mode = ModeOfflineLocal
	m.failures, m.successes = 0, 0
	m.mu.Unlock()
	modeErr := m.store.SetMode(ctx, ModeOfflineLocal)
	reasonErr := m.store.SetBlockedReason(ctx, reason)
	m.applyMu.Unlock()
	if modeErr != nil {
		return ModeOfflineLocal, modeErr
	}
	if reasonErr != nil {
		return ModeOfflineLocal, reasonErr
	}
	m.transition(from, ModeOfflineLocal)
	return ModeOfflineLocal, nil
}

// ClearBlock 在身份核对通过后解锁（幂等：本来没锁就不写库）。
func (m *ModeController) ClearBlock(ctx context.Context) error {
	m.mu.Lock()
	blocked := m.blocked
	m.blocked = ""
	m.mu.Unlock()
	if blocked == "" {
		return nil
	}
	return m.store.SetBlockedReason(ctx, "")
}

// AdoptPrimary 是人工确认「对面这台就是新的主节点」：丢掉按旧主节点建的整份副本
// （records / base_rev / 冲突卡，保留 pending 本地改动），清掉记下的身份与锁，
// 然后当场探一次。
//
// 没有它，主节点被重装/换机之后边缘会永久停在本地模式 —— 拒绝合并是对的，
// 但总得有一条有记录的出路，而不是让人去删库。
func (m *ModeController) AdoptPrimary(ctx context.Context) (Mode, error) {
	if err := m.store.ResetReplicaForAdopt(ctx); err != nil {
		return m.Mode(), err
	}
	m.mu.Lock()
	m.blocked = ""
	m.mu.Unlock()
	return m.Retry(ctx)
}

// CompleteReconcile 由合并方在追平结束时调用：有冲突停 conflict-review，否则回 online。
//
// 只在同步回合内生效（reconciling / conflict-review）。追平可能跑在主节点再次断开的
// 同一时刻：探测已经把模式降到 offline-local，这时一拍迟到的收尾不能被当成
// 「追平成功」把整段离线期伪装回 online。判定与写入在同一把锁里，两个写方不会交错。
func (m *ModeController) CompleteReconcile(ctx context.Context, hasConflicts bool) (Mode, error) {
	next := ModeOnline
	if hasConflicts {
		next = ModeConflictReview
	}
	m.applyMu.Lock()
	m.mu.Lock()
	from := m.mode
	if from != ModeReconciling && from != ModeConflictReview {
		m.mu.Unlock()
		m.applyMu.Unlock()
		return from, nil
	}
	m.mode = next
	m.failures, m.successes = 0, 0
	m.mu.Unlock()
	err := m.store.SetMode(ctx, next)
	m.applyMu.Unlock()
	if err != nil {
		return next, err
	}
	m.transition(from, next)
	return next, nil
}

// transition 记录并通知一次模式翻转；持久化由各入口自己做（它们要能返回写失败）。
func (m *ModeController) transition(from, to Mode) {
	if from == to {
		return
	}
	m.recordModeEvent(from, to)
	if m.OnModeChange != nil {
		m.OnModeChange(from, to)
	}
}

// recordModeEvent 落一条可见事件。写失败不阻断状态机 —— 内存里的模式已经正确、
// 库里的模式也已由调用方写过，少一条历史不值得让同步停下来（与 RecordProbe 同一取舍）。
func (m *ModeController) recordModeEvent(from, to Mode) {
	if m.store == nil {
		return
	}
	m.mu.Lock()
	reason := m.blocked
	m.mu.Unlock()
	_, _ = m.store.RecordModeEvent(context.Background(), ModeEvent{
		From: from, To: to, Reason: reason, At: m.now(),
	})
}

// parsePrimaryURL 是复制/转发/探活三处共用的唯一校验：非 https 一律拒绝。
func parsePrimaryURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("controlsync: primary_url required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("controlsync: parse primary_url: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("controlsync: primary_url must be https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("controlsync: primary_url missing host")
	}
	return u, nil
}
