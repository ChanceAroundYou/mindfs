package controlsync

import (
	"context"
	"errors"
)

// PageSource 拉取「rev >= since」的一页远端变更。*Replicator 实现它（同一套 https
// 客户端与分页大小）；测试用内存实现直接喂分页，不必起 HTTP 服务。
type PageSource interface {
	FetchPage(ctx context.Context, since int64) ([]RemoteChange, error)
}

// FetchPage 让 Replicator 同时充当合并阶段的拉取源。
func (r *Replicator) FetchPage(ctx context.Context, since int64) ([]RemoteChange, error) {
	page, err := r.fetchPage(ctx, since)
	if err != nil {
		return nil, err
	}
	return page.Changes, nil
}

// ReconcileResult 是一次合并的计数，便于状态接口与日志区分「追平」与「卡在冲突」。
type ReconcileResult struct {
	Applied   int // 本地没改过，远端值直接落到权威存储
	Converged int // 两侧改成了同一个值，pending 清掉
	Kept      int // 远端还是祖先值（离线期间主节点没动这条），保留 pending
	Conflicts int // 两侧都改过，留待裁决
	Skipped   int // 服务端重发的旧 rev
}

// Reconciler 在主节点恢复后把远端变更与本地 pending 做记录级三方比对（spec §6.2）。
//
// 它只在 `reconciling` 用：在线追平走 Replicator.SyncOnce（本地没有待推送改动，
// 远端值为准），离线期间不拉取。合并阶段每一条都可能要弹冲突，所以必须看 pending。
type Reconciler struct {
	store    *Store
	registry *Registry
	source   PageSource
	capturer *Capturer
}

func NewReconciler(store *Store, registry *Registry, source PageSource, capturer *Capturer) (*Reconciler, error) {
	if store == nil {
		return nil, errors.New("controlsync: store required")
	}
	if registry == nil {
		return nil, errors.New("controlsync: registry required")
	}
	if source == nil {
		return nil, errors.New("controlsync: page source required")
	}
	return &Reconciler{store: store, registry: registry, source: source, capturer: capturer}, nil
}

// Reconcile 一直拉到没有新页为止。中途出错就停在那儿：已经提交的页连同水位一起生效，
// 下次从 `base_rev` 续传，不会重放或跳过。
func (r *Reconciler) Reconcile(ctx context.Context) (ReconcileResult, error) {
	var res ReconcileResult
	for {
		base, err := r.store.BaseRev(ctx)
		if err != nil {
			return res, err
		}
		changes, err := r.source.FetchPage(ctx, base+1)
		if err != nil {
			return res, err
		}
		if len(changes) == 0 {
			return res, nil
		}
		progressed, err := r.applyPage(ctx, changes, base, &res)
		if err != nil {
			return res, err
		}
		if !progressed {
			// 整页都是已经处理过的旧 rev：服务端没守 since 契约。停在这儿，
			// 别在同一个位置上无限重拉。
			return res, nil
		}
	}
}

// applyPage 的顺序有意如此：**先写权威存储，再提交记录与水位**。
//
// 反过来的话，进程在两步之间死掉会留下「水位已过、JSON 还是旧值」的状态，
// 而且那条变更再也不会被拉到 —— 数据永久丢失。现在这个顺序最坏只是重做一遍，
// 而 Apply 同一份载荷是幂等的。
//
// 返回值表示这一页有没有推进水位；整页都是旧 rev 时为 false。
func (r *Reconciler) applyPage(ctx context.Context, changes []RemoteChange, base int64, res *ReconcileResult) (bool, error) {
	page := ReconcilePage{}
	maxRev := base
	for _, ch := range changes {
		if ch.Rev > maxRev {
			maxRev = ch.Rev
		}
		if ch.Rev <= base {
			res.Skipped++
			continue
		}
		decision, err := r.decide(ctx, ch)
		if err != nil {
			return false, err
		}
		switch decision.action {
		case reconcileApply:
			if err := r.applyToDomain(ctx, ch); err != nil {
				return false, err
			}
			page.Records = append(page.Records, ch)
			res.Applied++
		case reconcileConverged:
			if err := r.applyToDomain(ctx, ch); err != nil {
				return false, err
			}
			page.Records = append(page.Records, ch)
			page.ClearPending = append(page.ClearPending, PendingRef{Domain: ch.Domain, Key: ch.Key})
			res.Converged++
		case reconcileKept:
			res.Kept++
		case reconcileConflict:
			page.Conflicts = append(page.Conflicts, decision.conflict)
			res.Conflicts++
		}
	}
	page.BaseRev = maxRev
	if err := r.store.ApplyReconcilePage(ctx, page); err != nil {
		return false, err
	}
	return maxRev > base, nil
}

const (
	reconcileApply     = "apply"
	reconcileConverged = "converged"
	reconcileKept      = "kept"
	reconcileConflict  = "conflict"
)

type reconcileDecision struct {
	action   string
	conflict Conflict
}

// decide 是 §6.2 的四种分支，外加「服务端重发旧 rev」与「远端多了一个本地从没见过的
// 记录」两种边界。
func (r *Reconciler) decide(ctx context.Context, ch RemoteChange) (reconcileDecision, error) {
	if err := ValidateRecord(Record{Domain: ch.Domain, Key: ch.Key, Payload: ch.Payload}); err != nil {
		return reconcileDecision{}, err
	}
	pending, ok, err := r.store.PendingChange(ctx, ch.Domain, ch.Key)
	if err != nil {
		return reconcileDecision{}, err
	}
	if !ok {
		return reconcileDecision{action: reconcileApply}, nil
	}
	if pending.Deleted && ch.Deleted {
		return reconcileDecision{action: reconcileConverged}, nil
	}
	localHash, err := pendingHash(pending)
	if err != nil {
		return reconcileDecision{}, err
	}
	if !pending.Deleted && !ch.Deleted && ch.Hash == localHash {
		return reconcileDecision{action: reconcileConverged}, nil
	}
	if ch.Hash == pending.BaseHash {
		// 远端这一版就是边缘离线前看到的那个值：主节点在离线期间没动它，
		// 本地改动仍然是相对祖先的唯一改动，等推送即可。
		return reconcileDecision{action: reconcileKept}, nil
	}
	return reconcileDecision{action: reconcileConflict, conflict: Conflict{
		ID:            PullConflictID(ch.Domain, ch.Key),
		Domain:        ch.Domain,
		Key:           ch.Key,
		LocalPayload:  pending.Payload,
		RemotePayload: ch.Payload,
		LocalHash:     localHash,
		RemoteHash:    ch.Hash,
		LocalDeleted:  pending.Deleted,
		RemoteDeleted: ch.Deleted,
	}}, nil
}

// pendingHash 是待推送载荷的内容 hash。删除的 pending 没有载荷，它的「本地 hash」
// 就是祖先值（与墓碑保留最后一次内容 hash 的口径一致）。
func pendingHash(p PendingChange) (string, error) {
	if p.Deleted {
		return p.BaseHash, nil
	}
	return HashRecord(Record{Domain: p.Domain, Key: p.Key, Payload: p.Payload})
}

// applyToDomain 把远端记录写进权威存储，并压住采集器 —— 否则合并写进去的远端内容
// 会被当成一条新的本地改动回推给主节点。
func (r *Reconciler) applyToDomain(ctx context.Context, ch RemoteChange) error {
	domainImpl, err := r.registry.Get(ch.Domain)
	if err != nil {
		return err
	}
	rec := Record{
		Domain:  ch.Domain,
		Key:     ch.Key,
		Hash:    ch.Hash,
		Payload: ch.Payload,
		Deleted: ch.Deleted,
		Origin:  ch.Origin,
	}
	apply := func() error { return domainImpl.Apply(ctx, rec) }
	if r.capturer == nil {
		return apply()
	}
	return r.capturer.Suppress(ctx, []string{ch.Domain}, apply)
}
