package api

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// SessionCompletion 是一条「这一轮已经跑完」的账本行。
//
// 它存在的唯一理由是**补发**：客户端的完成判定是「上一帧在 replying、这一帧不在了」
// 的边沿检测，一旦手机在后台被系统杀掉 / 进了长时间 Doze，那一轮跑完的时刻它根本没
// 在线，边沿永远不会发生 —— 用户看到的就是「后台时间一长，完成提醒全丢」。
// 有了这份按单调 seq 编号的短账本，客户端只要报出自己消费到哪一条，醒来后第一帧就能
// 把期间漏掉的完成一次性补齐（服务端保证至少一次，客户端按 seq 去重）。
type SessionCompletion struct {
	Seq          uint64    `json:"seq"`
	RootID       string    `json:"rootId"`
	SessionKey   string    `json:"sessionKey"`
	SessionTitle string    `json:"sessionTitle"`
	Summary      string    `json:"summary"`
	CompletedAt  time.Time `json:"completedAt"`
	// SubSession 为真时客户端只推进游标、不弹提醒（子任务完成不该打扰）。
	SubSession bool `json:"subSession,omitempty"`
}

const (
	completionRingLimit = 256
	completionRingTTL   = 24 * time.Hour
)

// completionRing 是一个只追加的定长环，按进程生命周期编号。
//
// epoch 用来识别「服务端重启过」：重启后 seq 从 0 重新开始，而客户端游标可能还停在
// 旧进程的某个大数上。客户端发现 epoch 变了（或 seq 比自己的游标还小）就只重设游标、
// 不补发 —— 旧进程账本已经没了，补不出来。
type completionRing struct {
	mu    sync.Mutex
	epoch string
	seq   uint64
	items []SessionCompletion
}

func newCompletionRing() *completionRing {
	return &completionRing{epoch: strconv.FormatInt(time.Now().UTC().UnixNano(), 36)}
}

// Append 记一条完成，返回它的 seq。超出容量或超过 TTL 的旧行按先进先出丢弃。
func (r *completionRing) Append(item SessionCompletion, now time.Time) uint64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	item.Seq = r.seq
	if item.CompletedAt.IsZero() {
		item.CompletedAt = now.UTC()
	}
	r.items = append(r.items, item)
	r.pruneLocked(now)
	return item.Seq
}

// Since 返回 seq 大于 cursor 的行（可能为空），以及当前最大 seq 与 epoch。
func (r *completionRing) Since(cursor uint64) ([]SessionCompletion, uint64, string) {
	if r == nil {
		return nil, 0, ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []SessionCompletion
	for _, item := range r.items {
		if item.Seq > cursor {
			out = append(out, item)
		}
	}
	return out, r.seq, r.epoch
}

// Head 返回当前最大 seq 与 epoch（不返回行）：首次播种只要这两个数。
func (r *completionRing) Head() (uint64, string) {
	if r == nil {
		return 0, ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq, r.epoch
}

// HasSince 只回答「有没有比 cursor 更新的行」—— 等待循环每 200ms 调一次，必须常数级。
func (r *completionRing) HasSince(cursor uint64) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.items) > 0 && r.items[len(r.items)-1].Seq > cursor
}

func (r *completionRing) pruneLocked(now time.Time) {
	cutoff := 0
	if len(r.items) > completionRingLimit {
		cutoff = len(r.items) - completionRingLimit
	}
	expireBefore := now.UTC().Add(-completionRingTTL)
	for cutoff < len(r.items) && r.items[cutoff].CompletedAt.Before(expireBefore) {
		cutoff++
	}
	if cutoff > 0 {
		r.items = append([]SessionCompletion(nil), r.items[cutoff:]...)
	}
}

// RecordCompletion 把一轮结束记进补发账本。调用点在 EndSessionTurn —— 与通知、广播
// 同一处，保证「会话从 pending 表消失」和「完成入账」是同一个时刻。
func (h *StreamHub) RecordCompletion(snapshot PendingSessionSnapshot, sessionKey string) {
	if h == nil || h.completions == nil {
		return
	}
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		return
	}
	// RootID 为空的条目是「没有置过 pending 的回合」（例如内部续轮直接在别处收尾）。
	// 它不曾在 ListReplyingSessions 里出现过，也就从来不会有边沿检测提醒 —— 记进账本
	// 只会让客户端醒来时弹一条没有项目名、没有标题的空提醒。
	if strings.TrimSpace(snapshot.RootID) == "" {
		return
	}
	h.completions.Append(SessionCompletion{
		RootID:       snapshot.RootID,
		SessionKey:   key,
		SessionTitle: snapshot.SessionTitle,
		Summary:      snapshot.Summary,
		CompletedAt:  time.Now().UTC(),
		SubSession:   snapshot.SubSession,
	}, time.Now())
}

// CompletionsSince 是补发端点的读数口。
func (h *StreamHub) CompletionsSince(cursor uint64) ([]SessionCompletion, uint64, string) {
	if h == nil || h.completions == nil {
		return nil, 0, ""
	}
	return h.completions.Since(cursor)
}

// HasCompletionsSince 供长轮询等待循环判断是否该提前返回。
func (h *StreamHub) HasCompletionsSince(cursor uint64) bool {
	if h == nil || h.completions == nil {
		return false
	}
	return h.completions.HasSince(cursor)
}

// CompletionHead 返回账本当前的最大 seq 与 epoch（播种用，不带历史行）。
func (h *StreamHub) CompletionHead() (uint64, string) {
	if h == nil || h.completions == nil {
		return 0, ""
	}
	return h.completions.Head()
}
