package controlsync

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// PendingPusherOptions 是回传循环的参数。
type PendingPusherOptions struct {
	// InstanceID 作为 PushRequest.instance_id 上行，主节点把它记进变更的 origin。
	InstanceID string
	// BatchSize 是每拍最多推多少条（主节点单次请求有 8 MiB 上限）。
	BatchSize int
	// Interval 是两拍之间的间隔。
	Interval time.Duration
	// OnError 收到「单条被拒」这类不该中断整轮的问题（网络失败走返回的 error）。
	OnError func(error)
}

// PendingPusher 把本机离线期间的改动推回主节点（spec §6.1）。
//
// **只允许一个 goroutine 调用 PushOnce**：pending 的「读 → 推送 → 按回执改本地」
// 三步之间没有锁，并发跑会让同一条变更被删两次、或把后一版的内容当成前一版的回执。
// Run 就是那个唯一的 goroutine；需要立即触发时由 Run 所在的那条线调用。
type PendingPusher struct {
	store      *Store
	sender     PushSender
	instanceID string
	batchSize  int
	interval   time.Duration
	onError    func(error)
	// frozen 是「按域冻结」的集合（Q4）：这些域还有未裁决的冲突，pending 一律不推。
	// 与 PushOnce 同一条线读写（Runtime 的 tick 先 SetFrozenDomains 再推），不必加锁。
	frozen map[string]bool
}

// PushStats 是一拍的统计，供日志与状态接口使用。
type PushStats struct {
	// Pushed 是本次送出去的条数。
	Pushed int
	// Applied 是主节点接受（含收敛与已应用过的重放）的条数。
	Applied int
	// Conflicts 是两侧都动过的条数，每条都在本地建了一张冲突卡。
	Conflicts int
	// Rejected 是被主节点拒绝的条数（保留 pending，下拍重试）。
	Rejected int
}

// NewPendingPusher 校验依赖并构造回传循环。
func NewPendingPusher(store *Store, sender PushSender, opts PendingPusherOptions) (*PendingPusher, error) {
	if store == nil {
		return nil, errors.New("controlsync: store required")
	}
	if sender == nil {
		return nil, errors.New("controlsync: push sender required")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	if opts.Interval <= 0 {
		opts.Interval = 10 * time.Second
	}
	return &PendingPusher{
		store:      store,
		sender:     sender,
		instanceID: opts.InstanceID,
		batchSize:  opts.BatchSize,
		interval:   opts.Interval,
		onError:    opts.OnError,
	}, nil
}

// SetFrozenDomains 设置「按域冻结」的集合（Q4）。传 nil / 空集合即全部解冻。
//
// 冻结的粒度是域而不是整条通道：一个 pin 的冲突不该把账户偏好、工作流一起卡住。
// 只有 Runtime 的 tick 会调用它 —— 与 PushOnce 同一条 goroutine，顺序是「先定冻结
// 集合、再推」。
func (p *PendingPusher) SetFrozenDomains(frozen map[string]bool) {
	if len(frozen) == 0 {
		p.frozen = nil
		return
	}
	next := make(map[string]bool, len(frozen))
	for domain, on := range frozen {
		if on && domain != "" {
			next[domain] = true
		}
	}
	p.frozen = next
}

// Run 每 interval 推一拍，直到 ctx 结束。第一拍立即执行（恢复后不必再等一个间隔）。
func (p *PendingPusher) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		if _, err := p.PushOnce(ctx); err != nil {
			p.report(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// PushOnce 推一拍：取一批 pending、上行、按逐条回执改本地。
//
// 冻结域（有未裁决冲突）的 pending 连取都不取：它们会一直留在库里，排进批次只会
// 占满整页、把别的域挤出去。过滤放在取页里（ListPendingExcept）而不是取完再筛，
// 正是为了这一点。
//
// 网络或解码失败整拍返回 error 且**不动任何 pending**：下一拍原样重试。
// 单条结果缺失也当整拍失败 —— 猜「大概成功」会删掉没落地的改动。
func (p *PendingPusher) PushOnce(ctx context.Context) (PushStats, error) {
	var stats PushStats
	pending, err := p.store.ListPendingExcept(ctx, p.batchSize, p.frozen)
	if err != nil {
		return stats, err
	}
	if len(pending) == 0 {
		return stats, nil
	}
	baseRev, err := p.store.BaseRev(ctx)
	if err != nil {
		return stats, err
	}
	req := PushRequest{
		InstanceID: p.instanceID,
		BaseRev:    baseRev,
		Changes:    make([]PushChange, 0, len(pending)),
	}
	for _, pc := range pending {
		ch := PushChange{
			ChangeID: pc.ChangeID,
			Domain:   pc.Domain,
			Key:      pc.Key,
			BaseHash: pc.BaseHash,
			Deleted:  pc.Deleted,
		}
		if !pc.Deleted {
			ch.Payload = pc.Payload
		}
		req.Changes = append(req.Changes, ch)
	}
	batch, err := p.sender.Push(ctx, req)
	if err != nil {
		return stats, err
	}
	stats.Pushed = len(req.Changes)
	results := make(map[string]PushResult, len(batch.Results))
	for _, res := range batch.Results {
		results[res.ChangeID] = res
	}
	// 先确认每条都有结论再动本地：缺一条就整拍失败，pending 一条都不改。
	// 边推边改的话，半路发现缺失会留下「前几条删了、后缀没处理」的中间态 ——
	// 重试虽然幂等，但状态更难解释。
	for _, pc := range pending {
		if _, ok := results[pc.ChangeID]; !ok {
			return PushStats{Pushed: stats.Pushed}, fmt.Errorf(
				"controlsync: push result missing for %s (%s/%s)",
				pc.ChangeID, pc.Domain, pc.Key)
		}
	}
	for _, pc := range pending {
		res := results[pc.ChangeID]
		switch res.Status {
		case PushApplied:
			if err := p.applyAck(ctx, pc, res); err != nil {
				return stats, err
			}
			stats.Applied++
		case PushConflict:
			if err := p.recordConflict(ctx, pc, res); err != nil {
				return stats, err
			}
			stats.Conflicts++
		case PushRejected:
			stats.Rejected++
			p.report(fmt.Errorf("controlsync: push rejected %s/%s: %s",
				pc.Domain, pc.Key, res.Reason))
		default:
			return stats, fmt.Errorf("controlsync: unknown push status %q for %s",
				res.Status, pc.ChangeID)
		}
	}
	return stats, nil
}

// applyAck 处理「主节点接受了这一条」。
//
// 只删仍是推上去那一版的那条 pending。删不掉说明推送期间本地又改过 ——
// 那一版是基于刚落地的新值写的，把它的共同祖先挪过去，下拍就不会再判一次冲突。
func (p *PendingPusher) applyAck(ctx context.Context, pc PendingChange, res PushResult) error {
	deleted, err := p.store.DeletePendingIfChange(ctx, pc.Domain, pc.Key, pc.ChangeID)
	if err != nil || deleted {
		return err
	}
	// 主节点本来没有这条记录时回执里 hash 为空：空 base_hash 就是「基于不存在」，
	// 下拍带着它再推，主节点会按「新建」处理。
	_, err = p.store.RebasePendingAfterApplied(ctx, pc.Domain, pc.Key, res.Hash)
	return err
}

// recordConflict 在主节点判冲突时于本地建一张卡。
//
// 命名按边缘视角：local = 本机离线期间那一版（pending），remote = 主节点现值
// （回执带回来的 exists / hash / payload / deleted）。id 与拉取侧共用
// PullConflictID(domain,key) —— 同一条记录只该有**一张**卡，推与拉都指向它。
func (p *PendingPusher) recordConflict(ctx context.Context, pc PendingChange, res PushResult) error {
	localHash := pc.BaseHash
	if !pc.Deleted {
		hash, err := HashRecord(Record{Domain: pc.Domain, Key: pc.Key, Payload: pc.Payload})
		if err != nil {
			return fmt.Errorf("controlsync: hash pending %s/%s: %w", pc.Domain, pc.Key, err)
		}
		localHash = hash
	}
	_, err := p.store.InsertConflict(ctx, Conflict{
		ID:            PullConflictID(pc.Domain, pc.Key),
		Domain:        pc.Domain,
		Key:           pc.Key,
		LocalPayload:  pc.Payload,
		RemotePayload: res.Payload,
		LocalHash:     localHash,
		RemoteHash:    res.Hash,
		LocalDeleted:  pc.Deleted,
		RemoteDeleted: res.Deleted || !res.Exists,
	})
	if err != nil {
		return fmt.Errorf("controlsync: record conflict %s/%s: %w", pc.Domain, pc.Key, err)
	}
	return nil
}

func (p *PendingPusher) report(err error) {
	if p.onError != nil {
		p.onError(err)
	}
}
