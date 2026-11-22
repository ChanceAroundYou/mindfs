package kanban

import (
	"context"
	"log"
	"strings"
	"time"
)

// 会话回合的输出水位（Plan 2026-11-22 W3-1）。
//
// 它回答的问题是「这一轮真的还在吐内容吗」。既有判据只有会话探针（harness 有没有
// 在回包），而挂死的回合探针照样报「在跑」——探针能证明「活着」，不能证明「有进展」。
// 水位记录这个会话最后一次真的落地了内容帧的时刻，静默超阈值才判停滞。
//
// 水位只活在内存里，而且**只有本进程亲眼见过帧才存在**。重启之后「本进程没见过
// 帧」不等于「这一轮一直没产出」：把未知当成停滞，会让每次重启都给每个在跑的回合
// 记一次假的卡死。未知就要老老实实说未知。

const (
	// turnWatermarkFlushInterval 与 turnWatermarkFlushFrames 决定内存水位多久落一次库。
	// 落库只为诊断与跨进程可见；判定本身读内存，所以宁少勿多（回合里每帧都写库会
	// 把 SQLite 打满）。
	turnWatermarkFlushInterval = 30 * time.Second
	turnWatermarkFlushFrames   = 20
)

// turnWatermarkEntry 是一个会话的水位。frames 与 flushedAt 只服务于节流。
type turnWatermarkEntry struct {
	lastOutput time.Time
	frames     int
	flushedAt  time.Time
}

func turnWatermarkKey(rootID, sessionKey string) string {
	return strings.TrimSpace(rootID) + "\x00" + strings.TrimSpace(sessionKey)
}

// NoteSessionTurnOutput 记一帧输出。now 允许测试注入；零值取当前时间。
//
// 调用点只有 StreamHub 的帧合并处（AppContext.BroadcastSessionUpdate），也就是所有
// harness 帧的必经之路：多记一次只是把水位往前推，不会产生副作用。
func (s *Service) NoteSessionTurnOutput(ctx context.Context, rootID, sessionKey string, now time.Time) {
	if s == nil {
		return
	}
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	key := turnWatermarkKey(rootID, sessionKey)
	s.turnWatermarkMu.Lock()
	if s.turnWatermarks == nil {
		s.turnWatermarks = make(map[string]*turnWatermarkEntry)
	}
	entry := s.turnWatermarks[key]
	if entry == nil {
		entry = &turnWatermarkEntry{}
		s.turnWatermarks[key] = entry
	}
	entry.lastOutput = now
	entry.frames++
	flush := entry.flushedAt.IsZero() ||
		entry.frames >= turnWatermarkFlushFrames ||
		now.Sub(entry.flushedAt) >= turnWatermarkFlushInterval
	if flush {
		entry.frames = 0
		entry.flushedAt = now
	}
	s.turnWatermarkMu.Unlock()
	if flush {
		s.flushSessionTurnWatermark(ctx, rootID, sessionKey, now)
	}
}

// SessionTurnWatermark 读内存水位。ok=false 表示本进程不知道（从未见过帧，含重启）。
// 调用方必须把 ok=false 与「水位很旧」当成两件事：前者不可判停滞。
func (s *Service) SessionTurnWatermark(rootID, sessionKey string) (time.Time, bool) {
	if s == nil {
		return time.Time{}, false
	}
	key := turnWatermarkKey(rootID, sessionKey)
	s.turnWatermarkMu.Lock()
	defer s.turnWatermarkMu.Unlock()
	entry := s.turnWatermarks[key]
	if entry == nil || entry.lastOutput.IsZero() {
		return time.Time{}, false
	}
	return entry.lastOutput, true
}

// ClearSessionTurnWatermark 在回合结算时丢掉水位：下一轮从零开始，避免把上一轮的
// 产出时刻当成这一轮的进展。
func (s *Service) ClearSessionTurnWatermark(rootID, sessionKey string) {
	if s == nil {
		return
	}
	key := turnWatermarkKey(rootID, sessionKey)
	s.turnWatermarkMu.Lock()
	defer s.turnWatermarkMu.Unlock()
	delete(s.turnWatermarks, key)
}

// flushSessionTurnWatermark 把水位写进这一轮的回复行与运行中的节点行。写库失败只记
// 日志：水位是诊断与证据，不是状态机的一部分，不能因为它让回合失败。
func (s *Service) flushSessionTurnWatermark(ctx context.Context, rootID, sessionKey string, at time.Time) {
	store, err := s.taskStore(rootID)
	if err != nil || store == nil {
		return
	}
	base := ctx
	if base == nil {
		base = context.Background()
	}
	// 帧路径不能被写库拖住：节流后每个会话每 30 秒最多一次，且带硬超时。
	ctx, cancel := context.WithTimeout(base, 3*time.Second)
	defer cancel()
	stamp := at.UTC().Format(time.RFC3339Nano)
	if _, err := store.db.ExecContext(ctx, `
UPDATE task_activities SET last_output_at = ?
WHERE session_key = ? AND status IN (?, ?)`,
		stamp, sessionKey, ActivityStatusPending, ActivityStatusRunning); err != nil {
		log.Printf("[kanban/watermark] activity flush session=%s: %v", sessionKey, err)
	}
	if _, err := store.db.ExecContext(ctx, `
UPDATE workflow_node_runs SET last_output_at = ?
WHERE session_key = ? AND status = ?`,
		stamp, sessionKey, NodeRunRunning); err != nil {
		log.Printf("[kanban/watermark] node run flush session=%s: %v", sessionKey, err)
	}
}
