package controlsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// 推送结论。边缘只认「拿到结论」的变更，没拿到就留在 pending 等下一轮。
const (
	PushApplied  = "applied"
	PushConflict = "conflict"
	PushRejected = "rejected"
)

// PushRequest / PushChange 是 POST /api/control-sync/push 的线上形状（spec §6.1）。
type PushRequest struct {
	InstanceID string       `json:"instance_id"`
	BaseRev    int64        `json:"base_rev"`
	Changes    []PushChange `json:"changes"`
}

type PushChange struct {
	ChangeID string          `json:"change_id"`
	Domain   string          `json:"domain"`
	Key      string          `json:"key"`
	BaseHash string          `json:"base_hash"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	Deleted  bool            `json:"deleted"`
}

// PushResult 是单条变更的结论；响应里每条都必须有一条。
type PushResult struct {
	ChangeID   string `json:"change_id"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	ConflictID string `json:"conflict_id,omitempty"`
	Rev        int64  `json:"rev,omitempty"`
	// 以下四项是主节点处理完之后这条记录的当前状态。
	//
	// 边缘靠它们做两件事：conflict 时在本地建一张冲突卡（边缘视角 local = 自己
	// 那一版、remote = 主节点这一版），applied 时给「推送期间又改了一版」的 pending
	// 重定共同祖先。只回 hash 不够 —— 冲突卡要显示两侧内容。
	Exists  bool            `json:"exists"`
	Hash    string          `json:"hash,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Deleted bool            `json:"deleted,omitempty"`
}

// PushBatch 是整批响应：逐条结论 + 处理后的 head_rev。
type PushBatch struct {
	Results []PushResult `json:"results"`
	HeadRev int64        `json:"head_rev"`
}

// Pusher 在主节点侧执行推送（spec §6.1）：幂等判定、三方比对、落库或落冲突。
//
// 它**不**负责认证（token/HMAC 在 HTTP 层）也不管模式；输入是已解析的批次。
type Pusher struct {
	store    *Store
	registry *Registry
	capturer *Capturer
}

func NewPusher(store *Store, registry *Registry, capturer *Capturer) (*Pusher, error) {
	if store == nil {
		return nil, errors.New("controlsync: store required")
	}
	if registry == nil {
		return nil, errors.New("controlsync: registry required")
	}
	return &Pusher{store: store, registry: registry, capturer: capturer}, nil
}

// Push 逐条处理整批。整批不要求原子（记录相互独立），单条落地由 store 的事务保证。
func (p *Pusher) Push(ctx context.Context, req PushRequest) (PushBatch, error) {
	batch := PushBatch{Results: make([]PushResult, 0, len(req.Changes))}
	for _, ch := range req.Changes {
		result := p.pushOne(ctx, req.InstanceID, ch)
		batch.Results = append(batch.Results, result)
		// 这条记录已经在主节点落定，之前因它产生的冲突卡就是旧现场：
		// 不关掉会永远挂在列表里，用户还得手动点一次「已解决」。
		if result.Status == PushApplied {
			if _, err := p.store.SupersedeConflicts(ctx, ch.Domain, ch.Key, "superseded"); err != nil {
				return batch, err
			}
		}
	}
	head, err := p.store.HeadRev(ctx)
	if err != nil {
		return batch, err
	}
	batch.HeadRev = head
	return batch, nil
}

func (p *Pusher) pushOne(ctx context.Context, origin string, ch PushChange) PushResult {
	result := PushResult{ChangeID: ch.ChangeID}
	reject := func(reason string) PushResult {
		result.Status = PushRejected
		result.Reason = reason
		return result
	}
	if ch.ChangeID == "" {
		return reject("change_id required")
	}
	domainImpl, err := p.registry.Get(ch.Domain)
	if err != nil {
		return reject(err.Error())
	}
	if err := ValidateRecordKey(ch.Key); err != nil {
		return reject(err.Error())
	}
	// 先读当前记录再判幂等：重复的 change_id 也要回带主节点现值，
	// 否则边缘拿不到「推上去之后主节点又改过」的那个值去重定祖先。
	cur, exists, err := p.store.GetRecord(ctx, ch.Domain, ch.Key)
	if err != nil {
		return reject(err.Error())
	}
	result.Exists = exists
	if exists {
		result.Hash = cur.Hash
		result.Deleted = cur.Deleted
	}
	applied, err := p.store.ChangeApplied(ctx, ch.ChangeID)
	if err != nil {
		return reject(err.Error())
	}
	if applied {
		result.Status = PushApplied
		result.Reason = "duplicate change_id"
		return result
	}

	// 删除是最简单的一支，先单独处理，免得把「空 payload 的 hash」混进正常比对。
	if ch.Deleted {
		return p.pushDelete(ctx, origin, ch, cur, exists, result, reject)
	}

	canonical, err := CanonicalJSON(ch.Payload)
	if err != nil {
		return reject(fmt.Sprintf("invalid payload: %v", err))
	}
	payloadHash, err := HashRecord(Record{Domain: ch.Domain, Key: ch.Key, Payload: canonical})
	if err != nil {
		return reject(err.Error())
	}

	switch {
	case !exists && ch.BaseHash == "":
		// 边缘新建、主节点也没有 —— 直接落地。
	case exists && cur.Hash == ch.BaseHash:
		// 没人动过这条记录 —— 边缘的改动可以应用。
	case exists && !cur.Deleted && cur.Hash == payloadHash:
		// 已经收敛：主节点当前值恰好等于推送值，不重复写变更流。
		if err := p.store.MarkChangeApplied(ctx, ch.ChangeID, cur.Rev); err != nil {
			return reject(err.Error())
		}
		result.Status = PushApplied
		result.Reason = "converged"
		result.Rev = cur.Rev
		return result
	default:
		return p.pushConflict(ctx, origin, ch, cur, exists, payloadHash, result, reject)
	}

	rec := Record{
		Domain:  ch.Domain,
		Key:     ch.Key,
		Hash:    payloadHash,
		Payload: canonical,
		Origin:  origin,
	}
	if err := p.applyToDomain(ctx, domainImpl, rec); err != nil {
		return reject(fmt.Sprintf("apply: %v", err))
	}
	rev, err := p.store.ApplyPushedChange(ctx, PushedRecord{
		ChangeID: ch.ChangeID,
		Domain:   ch.Domain,
		Key:      ch.Key,
		Hash:     payloadHash,
		Payload:  canonical,
		Origin:   origin,
	})
	if err != nil {
		return reject(err.Error())
	}
	result.Status = PushApplied
	result.Rev = rev
	result.Exists = true
	result.Hash = payloadHash
	result.Deleted = false
	return result
}

func (p *Pusher) pushDelete(ctx context.Context, origin string, ch PushChange,
	cur Record, exists bool, result PushResult, reject func(string) PushResult) PushResult {
	switch {
	case !exists:
		// 主节点本来就没有这条记录：幂等成功，不造墓碑。
		head, err := p.store.HeadRev(ctx)
		if err != nil {
			return reject(err.Error())
		}
		if err := p.store.MarkChangeApplied(ctx, ch.ChangeID, head); err != nil {
			return reject(err.Error())
		}
		result.Status = PushApplied
		result.Reason = "already absent"
		return result
	case cur.Deleted:
		if err := p.store.MarkChangeApplied(ctx, ch.ChangeID, cur.Rev); err != nil {
			return reject(err.Error())
		}
		result.Status = PushApplied
		result.Reason = "already deleted"
		return result
	case cur.Hash == ch.BaseHash:
		// 边缘基于当前值删除：落墓碑，hash 保留最后一次内容（区分「主节点删了」与
		// 「主节点改完又删」靠的就是它）。
		domainImpl, err := p.registry.Get(ch.Domain)
		if err != nil {
			return reject(err.Error())
		}
		if err := p.applyToDomain(ctx, domainImpl, Record{Domain: ch.Domain, Key: ch.Key, Deleted: true}); err != nil {
			return reject(fmt.Sprintf("apply delete: %v", err))
		}
		rev, err := p.store.ApplyPushedChange(ctx, PushedRecord{
			ChangeID: ch.ChangeID,
			Domain:   ch.Domain,
			Key:      ch.Key,
			Hash:     cur.Hash,
			Deleted:  true,
			Origin:   origin,
		})
		if err != nil {
			return reject(err.Error())
		}
		result.Status = PushApplied
		result.Rev = rev
		// 墓碑保留了最后一次内容 hash（cur.Hash 已随当前记录带出），删除标记要改过来：
		// 边缘拿它当「主节点这一侧」渲染，删了还显示旧载荷就错了。
		result.Exists = true
		result.Deleted = true
		return result
	default:
		// 主节点在边缘离线期间改过这条记录，边缘又要删 —— 交给人裁决。
		return p.pushConflict(ctx, origin, ch, cur, exists, "", result, reject)
	}
}

func (p *Pusher) pushConflict(ctx context.Context, _ string, ch PushChange,
	cur Record, exists bool, remoteHash string, result PushResult, reject func(string) PushResult) PushResult {
	local := cur
	if !exists {
		local = Record{Domain: ch.Domain, Key: ch.Key}
	}
	if remoteHash == "" {
		// 删除冲突时远端「载荷」就是空；hash 用墓碑 hash 或空串表达。
		remoteHash = cur.Hash
	}
	id, err := p.store.InsertConflict(ctx, Conflict{
		ID:            ch.ChangeID,
		Domain:        ch.Domain,
		Key:           ch.Key,
		LocalPayload:  local.Payload,
		RemotePayload: ch.Payload,
		LocalHash:     local.Hash,
		RemoteHash:    remoteHash,
		LocalDeleted:  !exists || local.Deleted,
		RemoteDeleted: ch.Deleted,
	})
	if err != nil {
		return reject(err.Error())
	}
	result.Status = PushConflict
	result.ConflictID = id
	result.Reason = "both sides changed"
	// 载荷只在冲突时回：边缘要用它显示「主节点这一侧」，而 users 域里
	// 这份载荷含口令 hash —— 能用 hash 说清的场合就不多传一份内容。
	result.Payload = local.Payload
	return result
}

// applyToDomain 把记录写进权威存储；有采集器时压住采集，避免「自己写自己」进变更流。
func (p *Pusher) applyToDomain(ctx context.Context, domain Domain, rec Record) error {
	if domain == nil {
		return errors.New("controlsync: domain not registered")
	}
	apply := func() error { return domain.Apply(ctx, rec) }
	if p.capturer == nil {
		return apply()
	}
	return p.capturer.Suppress(ctx, []string{rec.Domain}, apply)
}
