package controlsync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// 角色。
const (
	// RolePrimary 是控制面的唯一写者：本地改动进变更流，供边缘拉取；
	// 同时提供 push / head / changes 三个同步端点。
	RolePrimary = "primary"
	// RoleEdge 是第二台 control：在线时页面照出、控制面写请求转发给主节点，
	// 本地副本追平变更流；主节点不可达时切本地模式，改动先攒 pending。
	RoleEdge = "edge"
)

// 边缘节奏的默认值。全部可由 config.json 覆盖（测试里给 0 = 每拍都跑）。
const (
	DefaultPushInterval    = 10 * time.Second
	DefaultReplicaInterval = 5 * time.Second
	DefaultTickInterval    = time.Second
	DefaultPushBatch       = 100
	// pushDrainRounds 是单拍最多连推几批。被拒的 pending 会一直留在库里，
	// 不设上限就会在同一拍里空转。
	pushDrainRounds = 10
)

// Config 是 control_sync 的启动配置（来自 config.json 的同名段）。
type Config struct {
	Enabled    bool
	Role       string
	InstanceID string
	// BuildVersion 随 head 下发，仅用于诊断：边缘不据此拒绝合并（版本不同但
	// schema 相同是正常升级过程），拒绝只看 instance_id 与 schema 版本。
	BuildVersion string
	PrimaryURL   string
	CAFile       string
	// Token 是同步通道的 bearer token。密钥只走文件/环境变量，不进 config.json。
	Token string

	BatchSize       int
	ProbeInterval   time.Duration
	PushInterval    time.Duration
	ReplicaInterval time.Duration
	TickInterval    time.Duration

	// OnError 接运行期错误（交接失败、推送失败、追平失败）。它只做日志：
	// 模式状态机自己按探测结果决定要不要降级。
	OnError func(error)
	// OnModeChange 在模式真翻转时被调用（前端横幅由 W5 的接线方订阅）。
	OnModeChange func(from, to Mode)
}

// Normalize 校验配置并补默认值。边缘要把整份控制面（含口令 hash）来回搬，
// 少任何一项都不是「配错了」而是不安全，一律拒绝启动。
func (c Config) Normalize() (Config, error) {
	switch c.Role {
	case "":
		c.Role = RolePrimary
	case RolePrimary, RoleEdge:
	default:
		return c, fmt.Errorf("controlsync: unknown role %q", c.Role)
	}
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultPushBatch
	}
	if c.ProbeInterval <= 0 {
		c.ProbeInterval = ProbeInterval
	}
	if c.PushInterval <= 0 {
		c.PushInterval = DefaultPushInterval
	}
	if c.ReplicaInterval <= 0 {
		c.ReplicaInterval = DefaultReplicaInterval
	}
	if c.TickInterval <= 0 {
		c.TickInterval = DefaultTickInterval
	}
	if c.Role != RoleEdge {
		return c, nil
	}
	c.InstanceID = strings.TrimSpace(c.InstanceID)
	c.PrimaryURL = strings.TrimSpace(c.PrimaryURL)
	c.Token = strings.TrimSpace(c.Token)
	if c.InstanceID == "" {
		return c, errors.New("controlsync: instance_id required for edge")
	}
	if c.PrimaryURL == "" {
		return c, errors.New("controlsync: primary_url required for edge")
	}
	if _, err := parsePrimaryURL(c.PrimaryURL); err != nil {
		return c, err
	}
	if c.Token == "" {
		return c, errors.New("controlsync: sync token required for edge")
	}
	return c, nil
}

// ReplicaSource 是边缘既用来追平、又在合并阶段逐页拉取的拉取源。*Replicator 同时满足两者。
type ReplicaSource interface {
	PageSource
	SyncOnce(ctx context.Context) (int, error)
}

// Deps 是宿主装配好的部件。生产里 Store/Registry/Capturer 由 server/app 用真实存储构造；
// Source/Sender/Mode 留空时 Runtime 按 Config 自己建（测试则直接注入内存实现）。
type Deps struct {
	Store    *Store
	Registry *Registry
	Capturer *Capturer
	Source   ReplicaSource
	Sender   PushSender
	Mode     *ModeController
}

// Runtime 把同步内核接成可运行的一套：主节点只跑采集兜底；边缘额外跑探活、
// 追平、回传与合并。
//
// 单 goroutine 原则：所有「改索引库」的循环（追平、推送、合并）都在 tick 里串行执行，
// 不并发。「采集」另起一个 goroutine，但它通过写 pending 与它们解耦（PutPending 是
// 单行事务，pending 的身份随内容前进）。
type Runtime struct {
	cfg      Config
	store    *Store
	registry *Registry
	capturer *Capturer

	pusher   *Pusher
	feed     *ChangeFeed
	resolver *Resolver

	source     ReplicaSource
	sender     PushSender
	reconciler *Reconciler
	pending    *PendingPusher
	mode       *ModeController

	handed   atomic.Bool
	lastSync time.Time
	lastPush time.Time
}

// NewRuntime 组装运行时。ctx 只用于读取持久化的模式（重启后必须回到上次的状态）。
func NewRuntime(ctx context.Context, cfg Config, deps Deps) (*Runtime, error) {
	cfg, err := cfg.Normalize()
	if err != nil {
		return nil, err
	}
	if deps.Store == nil {
		return nil, errors.New("controlsync: store required")
	}
	if deps.Registry == nil {
		return nil, errors.New("controlsync: registry required")
	}
	if deps.Capturer == nil {
		return nil, errors.New("controlsync: capturer required")
	}
	pusher, err := NewPusher(deps.Store, deps.Registry, deps.Capturer)
	if err != nil {
		return nil, err
	}
	feed, err := NewChangeFeed(deps.Store)
	if err != nil {
		return nil, err
	}
	// 主节点身份随 head 一起下发：边缘靠它判断对面有没有被重建。
	feed.SetIdentity(SyncIdentity{InstanceID: cfg.InstanceID, BuildVersion: cfg.BuildVersion})
	resolver, err := NewResolver(deps.Store, deps.Registry, deps.Capturer)
	if err != nil {
		return nil, err
	}
	rt := &Runtime{
		cfg:      cfg,
		store:    deps.Store,
		registry: deps.Registry,
		capturer: deps.Capturer,
		pusher:   pusher,
		feed:     feed,
		resolver: resolver,
		source:   deps.Source,
		sender:   deps.Sender,
		mode:     deps.Mode,
	}
	if cfg.Role != RoleEdge {
		// 主节点：本地改动进变更流，不做任何「待回传」判断。
		deps.Capturer.PendingWhen = nil
		deps.Capturer.CaptureWhen = nil
		deps.Capturer.SweepWhen = nil
		return rt, nil
	}

	if rt.source == nil {
		replicator, err := NewReplicator(deps.Store, ReplicatorOptions{
			PrimaryURL: cfg.PrimaryURL,
			CAFile:     cfg.CAFile,
			Token:      cfg.Token,
			InstanceID: cfg.InstanceID,
			Limit:      cfg.BatchSize,
			Interval:   cfg.ReplicaInterval,
			OnError:    cfg.OnError,
		})
		if err != nil {
			return nil, err
		}
		rt.source = replicator
	}
	if rt.sender == nil {
		sender, err := NewPushClient(PushClientOptions{
			PrimaryURL: cfg.PrimaryURL,
			Token:      cfg.Token,
			InstanceID: cfg.InstanceID,
			CAFile:     cfg.CAFile,
		})
		if err != nil {
			return nil, err
		}
		rt.sender = sender
	}
	if rt.mode == nil {
		prober, err := NewHTTPProber(cfg.PrimaryURL, cfg.CAFile, cfg.Token, cfg.InstanceID)
		if err != nil {
			return nil, err
		}
		rt.mode, err = NewModeController(ctx, deps.Store, prober)
		if err != nil {
			return nil, err
		}
	}
	rt.mode.Interval = cfg.ProbeInterval
	rt.mode.OnModeChange = rt.modeChanged

	rt.reconciler, err = NewReconciler(deps.Store, deps.Registry, rt.source, deps.Capturer)
	if err != nil {
		return nil, err
	}
	// 裁决「用本地值 / 用户合并值」时要换一个 change_id：同一份载荷换了祖先，
	// 沿用旧身份会被主节点按幂等当成「已应用过」，改动再也推不上去。
	rt.resolver.WithPendingIDs(cfg.InstanceID)
	rt.pending, err = NewPendingPusher(deps.Store, rt.sender, PendingPusherOptions{
		InstanceID: cfg.InstanceID,
		BatchSize:  cfg.BatchSize,
		Interval:   cfg.PushInterval,
		OnError:    cfg.OnError,
	})
	if err != nil {
		return nil, err
	}
	// 边缘的本地改动永远是「待回传」：它没有变更流的读者，写 changes 等于伪造
	// 一段没人追的历史。在线时直接跳过采集 —— 在线写都转发给主节点，本机权威存储
	// 本来就不该变；真变了（后台任务直写）也不是现在能安全回传的东西。
	deps.Capturer.PendingWhen = func() bool { return rt.mode.Mode() != ModeOnline }
	deps.Capturer.CaptureWhen = func() bool { return rt.mode.Mode() != ModeOnline }
	// 刚进 offline-local、这一轮交接还没跑完时先别扫：那时的本地存储里还混着
	// 本机自己生成的默认记录，扫一遍就会把它们当成待回传（见 SweepWhen 注释）。
	deps.Capturer.SweepWhen = func() bool {
		return rt.mode.Mode() != ModeOfflineLocal || rt.handed.Load()
	}
	return rt, nil
}

// Store / Capturer / Pusher / Feed / Resolver / Mode 供接线方取用：Runtime 不复制这些部件，
// HTTP 端点与运行期必须共用同一份（尤其 store 与 capturer 的抑制计数）。
func (r *Runtime) Store() *Store           { return r.store }
func (r *Runtime) Capturer() *Capturer     { return r.capturer }
func (r *Runtime) Pusher() *Pusher         { return r.pusher }
func (r *Runtime) Feed() *ChangeFeed       { return r.feed }
func (r *Runtime) Resolver() *Resolver     { return r.resolver }
func (r *Runtime) Mode() *ModeController   { return r.mode }
func (r *Runtime) Config() Config          { return r.cfg }
func (r *Runtime) Pending() *PendingPusher { return r.pending }

// Run 跑到 ctx 结束。主节点只有兜底采集；边缘额外跑探活与 tick 循环。
func (r *Runtime) Run(ctx context.Context) {
	if r == nil {
		return
	}
	go r.capturer.Run(ctx, FallbackInterval)
	if r.cfg.Role == RolePrimary {
		// 变更流只在主节点增长；边缘跑清理只会白扫空表。
		go r.pruneLoop(ctx)
	}
	if r.cfg.Role != RoleEdge || r.mode == nil {
		<-ctx.Done()
		return
	}
	go r.mode.Run(ctx)
	ticker := time.NewTicker(r.cfg.TickInterval)
	defer ticker.Stop()
	// 启动即一拍：持久化的模式已经是 offline-local 时要立刻交接，不能等第一个间隔。
	r.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			r.tick(ctx)
		}
	}
}

// tick 是边缘的一次调度。所有改索引库的动作都在这里串行发生。
func (r *Runtime) tick(ctx context.Context) {
	if r == nil || r.mode == nil {
		return
	}
	switch r.mode.Mode() {
	case ModeOnline:
		// 只有这一拍真的拉过新变更，副本才可能变；没拉过就不必再比较一遍账户表。
		if r.syncReplica(ctx) {
			r.applyOnlineUsers(ctx)
		}
		r.settle(ctx)
	case ModeOfflineLocal:
		if err := r.ensureHandover(ctx); err != nil {
			r.report(err)
		}
	case ModeReconciling:
		if _, err := r.reconciler.Reconcile(ctx); err != nil {
			// 身份/schema 不符、保留窗口缺口都不会因为重试而变好：锁住并停在本地模式，
			// 否则探活 20 秒后会把边缘拉回 reconciling，再撞同一个错误，来回跳。
			if errors.Is(err, ErrReplicaOriginMismatch) || errors.Is(err, ErrReplicaSchemaMismatch) ||
				errors.Is(err, ErrRetentionGap) {
				if _, blockErr := r.mode.BlockLocal(ctx, err.Error()); blockErr != nil {
					r.report(blockErr)
				}
			}
			r.report(err)
			return
		}
		// 追平真的跑通了，说明身份核对也过了：上一次锁的原因已经消失。
		if err := r.mode.ClearBlock(ctx); err != nil {
			r.report(err)
		}
		r.settle(ctx)
	case ModeConflictReview:
		// 还有没裁决的卡就原地等；都裁决完了把积压推上去，推干净才回 online。
		r.settle(ctx)
	}
}

// Handover 把副本整体写进权威存储，让页面在离线时看到的是主节点最后给的那一版。
//
// 已经攒了 pending 的 key 跳过：副本那一版已经被本地改动取代，写进去等于拿旧值
// 盖掉用户的编辑。apply 包在 Suppress 里，否则采集会把这次「写入」当成本地改动
// 再回传一遍；Suppress 成功后同时采纳基线，随后的采集不会把副本值重发一遍。
//
// 边界：交接前几毫秒内发生、且还没被采集到的本地写入会被副本值盖掉。这类写入只可能
// 来自非 HTTP 的直写（HTTP 写入在响应前就采集完了），且窗口只有一次 tick 的间隔；
// 宁可留这个已知缺口，也不引入「扫描前后两份全量快照做对账」的复杂度。
func (r *Runtime) Handover(ctx context.Context) error {
	if r == nil || r.cfg.Role != RoleEdge {
		return nil
	}
	var errs error
	for _, domain := range r.registry.Names() {
		if err := r.applyReplicaDomain(ctx, domain); err != nil {
			errs = errors.Join(errs, err)
		}
	}
	// 基线跟着副本走：下一次离线改动算出来的 base_hash 才是真正的共同祖先
	// （副本里的值 = 主节点最后给的值），而不是本地过期的那一版。
	if err := r.capturer.RefreshBaseline(ctx); err != nil {
		errs = errors.Join(errs, err)
	}
	return errs
}

// settle 把待回传推上去，并按结果决定停在哪个模式。
//
// 顺序是固定的，三条都不能换：
//  1. 先算冻结域（有未裁决冲突的域），交给推送方跳过 —— 否则冲突记录的 pending
//     会被推上去并拿到回执删掉，用户还没裁决的改动凭空消失。
//  2. 再推。冻结域之外的记录照常回传（Q4 按域冻结，不是整条通道停摆）。
//  3. 最后才看能不能回 online：必须「没有未裁决的卡」且「没有待回传」两条同时成立。
//     只看其中一条会在「已占坑但还没裁决完」或「裁决完了但还没推完」时提前放行。
func (r *Runtime) settle(ctx context.Context) {
	open, err := r.store.ListOpenConflicts(ctx)
	if err != nil {
		r.report(err)
		return
	}
	frozen := make(map[string]bool, len(open))
	for _, c := range open {
		frozen[c.Domain] = true
	}
	r.pending.SetFrozenDomains(frozen)

	stats, err := r.pushAll(ctx)
	if err != nil {
		r.report(err)
		return
	}
	// 推送本身可能刚制造出新的冲突卡（主节点判冲突），要重新数一遍。
	if stats.Conflicts > 0 {
		if open, err = r.store.ListOpenConflicts(ctx); err != nil {
			r.report(err)
			return
		}
	}
	if len(open) > 0 {
		if r.mode.Mode() != ModeConflictReview {
			if _, err := r.mode.CompleteReconcile(ctx, true); err != nil {
				r.report(err)
			}
		}
		return
	}
	pending, err := r.store.CountPending(ctx)
	if err != nil {
		r.report(err)
		return
	}
	if pending > 0 {
		return
	}
	if r.mode.Mode() != ModeOnline {
		if _, err := r.mode.CompleteReconcile(ctx, false); err != nil {
			r.report(err)
		}
	}
}

// pushAll 连推几批，直到没有可推的或到达上限。被拒的 pending 会留在库里，
// 上限保证一拍之内不会空转。
func (r *Runtime) pushAll(ctx context.Context) (PushStats, error) {
	var total PushStats
	now := time.Now()
	if !r.lastPush.IsZero() && now.Sub(r.lastPush) < r.cfg.PushInterval {
		return total, nil
	}
	r.lastPush = now
	for i := 0; i < pushDrainRounds; i++ {
		stats, err := r.pending.PushOnce(ctx)
		total.Pushed += stats.Pushed
		total.Applied += stats.Applied
		total.Conflicts += stats.Conflicts
		total.Rejected += stats.Rejected
		if err != nil {
			return total, err
		}
		if stats.Pushed == 0 {
			break
		}
	}
	return total, nil
}

// syncReplica 在线时把主节点的变更流追到本地副本。只在 online 跑：合并阶段必须由
// Reconciler 逐页处理，直接追平会绕过三方比对。
//
// 返回值是「这一拍真的去拉过」，不是「拉到条数」：节流跳过时调用方不该再跑一遍
// 跟随动作（账户表采纳），否则每秒一拍都在白比较整份账户表。
func (r *Runtime) syncReplica(ctx context.Context) bool {
	now := time.Now()
	if !r.lastSync.IsZero() && now.Sub(r.lastSync) < r.cfg.ReplicaInterval {
		return false
	}
	r.lastSync = now
	if _, err := r.source.SyncOnce(ctx); err != nil {
		r.report(err)
	}
	return true
}

// applyOnlineUsers 在线时把主控的账户表采纳进本机权威存储。
//
// 为什么在线就要采纳，而不是等掉线交接：经主控 /relay/<name>/ 进来的请求带的是
// **主控那边的账户**（跨机器只能对齐用户名）。这台机器的账户表若只有它自己的
// admin，Workspace("xiaokubao") 会落到 emptyWorkspace —— 页面打得开、内容全空，
// 这正是 relay 用起来的默认状态。
//
// 只做 users 一个域：别的域（偏好/节点表/提示词）在线时由控制面转发直接读写主控，
// 本地那份只给离线用；账户表不一样，它是请求进门时的身份解析依据，在线也绕不开。
func (r *Runtime) applyOnlineUsers(ctx context.Context) {
	if r == nil || r.registry == nil {
		return
	}
	if _, err := r.registry.Get(DomainUsers); err != nil {
		return // 这套部署没注册 users 域，不关本函数的事
	}
	if err := r.applyReplicaDomain(ctx, DomainUsers); err != nil {
		r.report(err)
	}
}

// ensureHandover 每次进入 offline-local 只交接一次；翻到别的模式后清标记，
// 下一次掉线要按当时的副本重新交接。
func (r *Runtime) ensureHandover(ctx context.Context) error {
	if r.handed.Load() {
		return nil
	}
	if err := r.Handover(ctx); err != nil {
		return err
	}
	r.handed.Store(true)
	return nil
}

func (r *Runtime) modeChanged(from, to Mode) {
	r.handed.Store(false)
	if r.cfg.OnModeChange != nil {
		r.cfg.OnModeChange(from, to)
	}
}

func (r *Runtime) report(err error) {
	if err != nil && r.cfg.OnError != nil {
		r.cfg.OnError(err)
	}
}

// PruneInterval 是主节点清理变更流的节奏（spec §4.2：启动一次 + 每小时一次）。
const PruneInterval = time.Hour

// pruneLoop 启动即清一拍，之后每小时一拍。单拍只删一批（PruneBatchSize），
// 积压的多余部分自然由后续每拍接着删 —— 慢一点可以接受，卡住写入不行。
func (r *Runtime) pruneLoop(ctx context.Context) {
	if r == nil || r.store == nil {
		return
	}
	r.pruneOnce(ctx)
	ticker := time.NewTicker(PruneInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.pruneOnce(ctx)
		}
	}
}

func (r *Runtime) pruneOnce(ctx context.Context) {
	if _, err := r.store.PruneChanges(ctx, PruneOptions{}); err != nil {
		if ctx.Err() == nil {
			r.report(err)
		}
	}
}
