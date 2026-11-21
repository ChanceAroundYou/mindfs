package controlsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrConflictNotFound 表示这个 id 没有冲突现场（可能已清理或拼错）。
	ErrConflictNotFound = errors.New("controlsync: conflict not found")
	// ErrConflictResolved 表示同一张冲突卡已经被裁决过（重复提交、并发提交）。
	ErrConflictResolved = errors.New("controlsync: conflict already resolved")
	// ErrConflictChoice 表示选择或随附载荷不合法。
	ErrConflictChoice = errors.New("controlsync: invalid conflict choice")
)

// 裁决的三种选择。
//
// 命名按**边缘视角**：`local` 是边缘离线期间改出来的那个值，`primary` 是主节点上
// 现在的值，`payload` 是用户自己合并后的新值。
const (
	ConflictChoiceLocal   = "local"
	ConflictChoicePrimary = "primary"
	ConflictChoicePayload = "payload"
)

// users 域的载荷里含口令 hash，属于敏感数据：查询接口只回 hash 与删除标记，
// 不回载荷明文（spec §9）。
const domainUsers = "users"

// ConflictView 是冲突查询接口的一条。载荷按规范化 JSON 原样带出，前端自己渲染。
type ConflictView struct {
	ID            string `json:"id"`
	Domain        string `json:"domain"`
	Key           string `json:"key"`
	LocalHash     string `json:"local_hash"`
	RemoteHash    string `json:"remote_hash"`
	LocalDeleted  bool   `json:"local_deleted"`
	RemoteDeleted bool   `json:"remote_deleted"`
	// Redacted 为真时 Local / Remote 一律为空，只给 hash 与删除标记。
	Redacted  bool            `json:"redacted"`
	Local     json.RawMessage `json:"local,omitempty"`
	Remote    json.RawMessage `json:"remote,omitempty"`
	CreatedAt string          `json:"created_at"`
}

// Resolver 是冲突卡的查询与裁决入口。它只处理**本机**库里的冲突：
// 边缘读自己的 `conflicts`（拉取侧按 domain,key 派生 id），主节点读推送侧
// 按 change_id 派生 id 的那批。
type Resolver struct {
	store    *Store
	registry *Registry
	capturer *Capturer
	// origin 是本机在 change_id 里的身份前缀。设了它，`payload` 裁决（载荷变了）
	// 会重新领号；主节点自己裁决时可以留空 —— 它没有 applied_changes 去重，
	// 沿用旧 id 也不会丢更新。
	origin string
}

// WithPendingIDs 让 Resolver 在载荷被改写时换一个新的 change_id。
func (r *Resolver) WithPendingIDs(origin string) *Resolver {
	if r != nil {
		r.origin = origin
	}
	return r
}

func NewResolver(store *Store, registry *Registry, capturer *Capturer) (*Resolver, error) {
	if store == nil {
		return nil, errors.New("controlsync: store required")
	}
	if registry == nil {
		return nil, errors.New("controlsync: registry required")
	}
	return &Resolver{store: store, registry: registry, capturer: capturer}, nil
}

// List 返回全部未裁决的冲突，先来的在前。
func (r *Resolver) List(ctx context.Context) ([]ConflictView, error) {
	rows, err := r.store.ListOpenConflicts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ConflictView, 0, len(rows))
	for _, c := range rows {
		out = append(out, conflictView(c))
	}
	return out, nil
}

func conflictView(c Conflict) ConflictView {
	v := ConflictView{
		ID:            c.ID,
		Domain:        c.Domain,
		Key:           c.Key,
		LocalHash:     c.LocalHash,
		RemoteHash:    c.RemoteHash,
		LocalDeleted:  c.LocalDeleted,
		RemoteDeleted: c.RemoteDeleted,
		CreatedAt:     c.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if c.Domain == domainUsers {
		v.Redacted = true
		return v
	}
	if !c.LocalDeleted {
		v.Local = c.LocalPayload
	}
	if !c.RemoteDeleted {
		v.Remote = c.RemotePayload
	}
	return v
}

// Resolve 落定一条冲突：把选中的那一侧写回权威存储，并调整 pending 让恢复后
// 的推送能一次成功（spec §6.3）。
//
// 三种选择的后续状态：
//   - `local`：pending 保留，`base_hash` 改成主节点现在的值（`remote_hash`）——
//     下次推送时主节点的当前 hash 正好等于 base_hash，走「祖先匹配 → 应用」。
//   - `primary`：把主节点的值写回来，pending 清掉，这条不再需要推送。
//   - `payload`：写用户合并值，pending 换成这份载荷、base_hash 同样重定到
//     `remote_hash`。
//
// 顺序是「先占坑（CAS）→ 再写副作用 → 失败退坑」。先写副作用的话，两个并发
// 请求会各写一次，后到的那个覆盖先到的选择。
func (r *Resolver) Resolve(ctx context.Context, id, choice string, payload []byte) error {
	c, resolvedAt, _, ok, err := r.store.LoadConflict(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflictNotFound
	}
	if resolvedAt != "" {
		return ErrConflictResolved
	}
	target, keepPending, err := resolveTarget(c, choice, payload)
	if err != nil {
		return err
	}
	if err := r.store.ClaimConflict(ctx, id, choice); err != nil {
		return err
	}
	newChangeID, err := r.pendingIDFor(ctx, c, choice)
	if err != nil {
		r.store.ReleaseConflict(ctx, id)
		return err
	}
	if err := r.applyResolved(ctx, c, target, keepPending, newChangeID); err != nil {
		// 副作用没落成，把坑退回让用户重试；冲突卡本身仍然有效。
		r.store.ReleaseConflict(ctx, id)
		return err
	}
	return nil
}

// resolveTarget 把用户的选择翻译成一条要写进权威存储的记录，
// 以及「本地这一侧是否还需要推送」。
func resolveTarget(c Conflict, choice string, payload []byte) (Record, bool, error) {
	rec := Record{Domain: c.Domain, Key: c.Key}
	switch choice {
	case ConflictChoiceLocal:
		rec.Payload = c.LocalPayload
		rec.Hash = c.LocalHash
		rec.Deleted = c.LocalDeleted
		return rec, true, verifySideHash(rec, c.LocalDeleted)
	case ConflictChoicePrimary:
		rec.Payload = c.RemotePayload
		rec.Hash = c.RemoteHash
		rec.Deleted = c.RemoteDeleted
		return rec, false, verifySideHash(rec, c.RemoteDeleted)
	case ConflictChoicePayload:
		if len(bytes.TrimSpace(payload)) == 0 || bytes.Equal(bytes.TrimSpace(payload), []byte("null")) {
			return Record{}, false, fmt.Errorf("%w: payload required", ErrConflictChoice)
		}
		canonical, err := CanonicalJSON(json.RawMessage(payload))
		if err != nil {
			return Record{}, false, fmt.Errorf("%w: payload is not valid json", ErrConflictChoice)
		}
		rec.Payload = canonical
		hash, err := HashRecord(rec)
		if err != nil {
			return Record{}, false, fmt.Errorf("%w: %v", ErrConflictChoice, err)
		}
		rec.Hash = hash
		return rec, true, nil
	default:
		return Record{}, false, fmt.Errorf("%w: %q", ErrConflictChoice, choice)
	}
}

// verifySideHash 确认落库的载荷与 hash 对得上。删除一侧没有载荷，hash 是墓碑
// 保留的祖先值，无法从载荷重算，跳过。
func verifySideHash(rec Record, deleted bool) error {
	if deleted {
		return nil
	}
	hash, err := HashRecord(rec)
	if err != nil {
		return fmt.Errorf("controlsync: conflict payload unhashable: %w", err)
	}
	if hash != rec.Hash {
		return errors.New("controlsync: conflict payload does not match its hash")
	}
	return nil
}

// applyResolved 先写权威存储，再改 pending。
//
// 反过来的话，pending 已经重定到新 base_hash 而权威存储还是旧值：这时推送会把
// 旧值推上去，冲突现场就此丢失。现在这个顺序最坏是「值对了、pending 没改」，
// 下次推送再撞一次同样的冲突，用户重新裁决即可。
func (r *Resolver) applyResolved(ctx context.Context, c Conflict, target Record, keepPending bool, newChangeID string) error {
	domainImpl, err := r.registry.Get(c.Domain)
	if err != nil {
		return err
	}
	apply := func() error { return domainImpl.Apply(ctx, target) }
	if r.capturer != nil {
		err = r.capturer.Suppress(ctx, []string{c.Domain}, apply)
	} else {
		err = apply()
	}
	if err != nil {
		return fmt.Errorf("controlsync: apply resolved record: %w", err)
	}
	if !keepPending {
		return r.store.DeletePending(ctx, c.Domain, c.Key)
	}
	// 本地这一侧还要推上去：把共同祖先重定到主节点当前值，这样推送方
	// （Pusher）看到的是「基于最新值的改动」，不会再判一次冲突。
	if _, err := r.store.RebasePendingWithID(ctx, c.Domain, c.Key, c.RemoteHash,
		newChangeID, target.Payload, target.Deleted); err != nil {
		return err
	}
	return nil
}

// pendingIDFor 决定重定之后 pending 用哪个身份。
//
// 只有 `payload`（用户填了合并值）会换号：内容变了沿用旧 change_id 会被主节点的
// applied_changes 当成重复而丢掉。`local` 推的就是本地原来那一版，身份不动。
func (r *Resolver) pendingIDFor(ctx context.Context, c Conflict, choice string) (string, error) {
	if r.origin == "" || choice != ConflictChoicePayload {
		return "", nil
	}
	seq, err := r.store.NextPendingSeq(ctx)
	if err != nil {
		return "", err
	}
	return PendingChangeID(r.origin, c.Domain, c.Key, seq), nil
}
