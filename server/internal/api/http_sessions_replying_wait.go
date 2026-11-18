package api

import (
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// 长轮询等待端点的计时参数。**故意做成包级 var（不是 const）**：测试要把它压到毫秒级，
// 否则每个超时用例都得真等 5–30 秒。生产值见下方字面量，运行时没有任何代码会改它们。
var (
	// 等待循环的轮询粒度：每轮只调 ListReplyingSessions()（纯内存 map 遍历）。
	replyingWaitPollInterval = 200 * time.Millisecond
	// 摘要刷新间隔：形状没变时，最多这么久才认一次 summary/标题/updatedAt 的变化，
	// 把高频 chunk 更新压成一档，避免变成请求风暴。
	replyingWaitRefreshInterval = 5 * time.Second
	replyingWaitMinTimeout      = 5 * time.Second
	replyingWaitDefaultTimeout  = 20 * time.Second
	replyingWaitMaxTimeout      = 30 * time.Second
	// replyingWaitInFlight 是**全局**在途等待计数（API 层不鉴权、fn 还监听 0.0.0.0，
	// 只能靠这个上限兜底）。测试直接读写它，所以也是包级 var。
	replyingWaitInFlight int32
)

// replyingWaitMaxInFlight 是在途等待的硬上限：超过就 503 + Retry-After。
const replyingWaitMaxInFlight = 32

// handleReplyingSessionsWait 是 `/api/replying-sessions` 的长轮询版本。
//
// 契约（spec §1）：
//   - 不带 If-None-Match → **立即** 200 全量快照 + ETag（客户端播种）。首帧不等待，
//     否则「进程启动后 timeout 内开始又结束的一轮」会既没被看见 replying、也没被看见完成。
//   - 带 If-None-Match → 进入等待，直到其一发生：形状变化（集合/status/awaiting）、
//     距请求开始满刷新间隔且摘要类字段变化、或超时。三者都走 respondJSONConditional，
//     所以载荷与客户端缓存一致时回 304。
//   - 请求方断开 → 立刻返回，不写响应。
//   - 在途 > 32 → 503 + Retry-After: 3。
//
// 循环里**只读** ListReplyingSessions（纯内存）；昂贵的载荷构造（GetRoot + GetMeta）
// 只在最终返回时做一次，详见 replyingSessionsPayload。
func (h *HTTPHandler) handleReplyingSessionsWait(w http.ResponseWriter, r *http.Request) {
	if atomic.AddInt32(&replyingWaitInFlight, 1) > replyingWaitMaxInFlight {
		// 被拒的那次不占名额：回滚后再返回，否则一次拒绝就永久吃掉一份余量。
		atomic.AddInt32(&replyingWaitInFlight, -1)
		w.Header().Set("Retry-After", "3")
		respondError(w, http.StatusServiceUnavailable, errServiceUnavailable("too many concurrent replying-session waiters"))
		return
	}
	defer atomic.AddInt32(&replyingWaitInFlight, -1)

	// 没装 AppContext / hub 时与普通端点同形：立刻回空快照，不进等待。
	if h.AppContext == nil || h.AppContext.GetSessionStreamHub() == nil {
		respondJSONConditional(w, r, map[string]any{"sessions": []map[string]any{}})
		return
	}

	// 首帧不等待：客户端用它播种 ETag。
	if strings.TrimSpace(r.Header.Get("If-None-Match")) == "" {
		respondJSONConditional(w, r, map[string]any{"sessions": h.replyingSessionsPayload(r, h.replyingWaitItems())})
		return
	}

	timeout := parseReplyingWaitTimeout(r.URL.Query().Get("timeout"))
	start := time.Now()
	lastRefresh := start

	// 基线取自**请求到达时**的纯内存快照。两个签名都不碰 GetRoot/GetMeta，
	// 所以每轮比对是常数级开销。
	items := h.replyingWaitItems()
	shapeBase := replyingShapeSignature(items)
	payloadBase := replyingPayloadSignature(items)

	ticker := time.NewTicker(replyingWaitPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			// 请求方断开：立即返回，不写响应。
			return
		case <-ticker.C:
		}
		items = h.replyingWaitItems()
		if replyingShapeSignature(items) != shapeBase {
			break
		}
		now := time.Now()
		if now.Sub(lastRefresh) >= replyingWaitRefreshInterval {
			lastRefresh = now
			if replyingPayloadSignature(items) != payloadBase {
				break
			}
		}
		if now.Sub(start) >= timeout {
			break
		}
	}

	respondJSONConditional(w, r, map[string]any{"sessions": h.replyingSessionsPayload(r, items)})
}

// replyingWaitItems 读一次纯内存快照，并容忍未装配的 AppContext/hub。
func (h *HTTPHandler) replyingWaitItems() []ReplyingSessionState {
	if h.AppContext == nil {
		return nil
	}
	hub := h.AppContext.GetSessionStreamHub()
	if hub == nil {
		return nil
	}
	return hub.ListReplyingSessions()
}

// parseReplyingWaitTimeout 把 `timeout`（秒）夹进 [min, max]；非法值回落默认。
func parseReplyingWaitTimeout(raw string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return replyingWaitDefaultTimeout
	}
	timeout := time.Duration(seconds) * time.Second
	if timeout < replyingWaitMinTimeout {
		return replyingWaitMinTimeout
	}
	if timeout > replyingWaitMaxTimeout {
		return replyingWaitMaxTimeout
	}
	return timeout
}

// replyingShapeSignature 把「形状」——会话集合 + status + awaiting ——按列表顺序拼成指纹。
//
// 只用 ListReplyingSessions 里已有的字段，**不碰** GetRoot/GetMeta：它每 poll 间隔都要算一次，
// 必须廉价。列表顺序由 Task 1 钉成 SessionKey 升序，所以同样的状态永远算出同样的串。
func replyingShapeSignature(items []ReplyingSessionState) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString(item.SessionKey)
		b.WriteByte('|')
		b.WriteString(item.Status)
		b.WriteByte('|')
		b.WriteString(awaitingFlag(item.AwaitingAnswer))
		b.WriteByte('\n')
	}
	return b.String()
}

// replyingPayloadSignature 在形状指纹之上再并入「摘要类」字段：root 身份、会话标题、
// summary、updatedAt。形状不变但摘要刷新时，靠它决定是否到点返回。
//
// 这里用 RootID 而不是对外的 rootTitle：rootTitle 要 GetRoot 才能拿到，放进签名就把
// 「每轮廉价比对」变成「每轮打盘」。root 名在运行时几乎不变、且一旦变化最终响应的
// ETag（由 respondJSONConditional 在真实载荷上算）照样会变，所以用稳定的 RootID 作锚点
// 已经覆盖了实际会变的字段（summary/标题/updatedAt）。
func replyingPayloadSignature(items []ReplyingSessionState) string {
	var b strings.Builder
	for _, item := range items {
		b.WriteString(item.SessionKey)
		b.WriteByte('|')
		b.WriteString(item.Status)
		b.WriteByte('|')
		b.WriteString(awaitingFlag(item.AwaitingAnswer))
		b.WriteByte('|')
		b.WriteString(item.RootID)
		b.WriteByte('|')
		b.WriteString(item.SessionTitle)
		b.WriteByte('|')
		b.WriteString(item.Summary)
		b.WriteByte('|')
		b.WriteString(item.UpdatedAt.UTC().Format(time.RFC3339Nano))
		b.WriteByte('\n')
	}
	return b.String()
}

func awaitingFlag(awaiting bool) string {
	if awaiting {
		return "1"
	}
	return "0"
}
