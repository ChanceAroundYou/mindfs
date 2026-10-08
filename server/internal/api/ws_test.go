package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/e2ee"
	"mindfs/server/internal/session"

	"github.com/gorilla/websocket"
)

func TestParseClientContext(t *testing.T) {
	payload := map[string]any{
		"context": map[string]any{
			"current_root": "ignored-by-payload",
			"selection": map[string]any{
				"file_path":  "docs/readme.md",
				"start_line": 1,
				"end_line":   3,
				"text":       "abc",
			},
		},
	}

	got := parseClientContext(payload, "mindfs")
	if got.CurrentRoot != "ignored-by-payload" {
		t.Fatalf("unexpected current root: %q", got.CurrentRoot)
	}
	if got.Selection == nil || got.Selection.Text != "abc" {
		t.Fatalf("unexpected selection: %#v", got.Selection)
	}

	got = parseClientContext(map[string]any{}, "fallback-root")
	if got.CurrentRoot != "fallback-root" {
		t.Fatalf("expected fallback root, got %q", got.CurrentRoot)
	}
}

func TestRegisterClientSupersedesPreviousConnection(t *testing.T) {
	hub := NewStreamHub(nil)
	first := &websocket.Conn{}
	second := &websocket.Conn{}

	if previous := hub.RegisterClient("client-1", first); previous != nil {
		t.Fatalf("first registration returned previous connection %p", previous)
	}
	hub.BindSessionClient("session-1", "client-1")
	if previous := hub.RegisterClient("client-1", second); previous != first {
		t.Fatalf("replacement returned %p, want %p", previous, first)
	}

	hub.UnregisterClient("client-1", first)
	if got := hub.clients["client-1"]; got != second {
		t.Fatalf("old connection unregistered replacement: got %p, want %p", got, second)
	}
	if _, ok := hub.connLocks[first]; ok {
		t.Fatal("old connection lock was not released")
	}
	if got := hub.GetSessionClientIDs("session-1", false); len(got) != 1 || got[0] != "client-1" {
		t.Fatalf("old connection removed replacement session binding: %#v", got)
	}

	hub.UnregisterClient("client-1", second)
	if got := hub.clients["client-1"]; got != nil {
		t.Fatalf("replacement connection still registered: %p", got)
	}
}

func TestAppendReplyEventPrefixesTruncatedSummary(t *testing.T) {
	hub := NewStreamHub(nil)

	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: "message_chunk",
		Data: agenttypes.MessageChunk{Content: strings.Repeat("前", 601) + "后"},
	})

	snapshot := hub.PendingSessionSnapshot("sess-1")
	if !strings.HasPrefix(snapshot.Summary, "...") {
		t.Fatalf("summary should start with ellipsis when truncated, got %q", snapshot.Summary)
	}
	if !strings.HasSuffix(snapshot.Summary, "后") {
		t.Fatalf("summary should keep the end of the content, got %q", snapshot.Summary)
	}
}

func TestAppendReplyEventResetsSummaryAfterAuxiliaryEvent(t *testing.T) {
	hub := NewStreamHub(nil)

	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeMessageChunk),
		Data: agenttypes.MessageChunk{Content: "before aux"},
	})
	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypePlanUpdate),
		Data: agenttypes.PlanUpdate{Content: "- inspect"},
	})
	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeMessageChunk),
		Data: agenttypes.MessageChunk{Content: "after aux"},
	})

	snapshot := hub.PendingSessionSnapshot("sess-1")
	if snapshot.Summary != "after aux" {
		t.Fatalf("summary = %q, want aux boundary to discard previous content", snapshot.Summary)
	}
}

func TestAppendReplyEventBuildsCompositeCursor(t *testing.T) {
	hub := NewStreamHub(nil)
	hub.SetPendingUserAt("root", "sess-1", "title", "codex", "", "", "", "", "", false, "prompt", time.Now(), 8)

	first := hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeMessageChunk),
		Data: agenttypes.MessageChunk{Content: "first"},
	})
	second := hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeMessageChunk),
		Data: agenttypes.MessageChunk{Content: "second"},
	})

	if first.EventCursor != "8:1" || second.EventCursor != "8:2" {
		t.Fatalf("event cursors = %q, %q; want 8:1, 8:2", first.EventCursor, second.EventCursor)
	}
}

func TestReplayPendingStartsAfterCompositeCursor(t *testing.T) {
	hub := NewStreamHub(nil)
	hub.SetPendingUserAt("root", "sess-1", "title", "codex", "", "", "", "", "", false, "prompt", time.Now(), 8)
	hub.AppendReplyEvent("sess-1", StreamEvent{Type: string(agenttypes.EventTypeMessageChunk), Data: agenttypes.MessageChunk{Content: "first"}})
	hub.AppendReplyEvent("sess-1", StreamEvent{Type: string(agenttypes.EventTypeMessageChunk), Data: agenttypes.MessageChunk{Content: "second"}})
	hub.AppendReplyEvent("sess-1", StreamEvent{Type: string(agenttypes.EventTypeMessageChunk), Data: agenttypes.MessageChunk{Content: "third"}})
	hub.replayStates[pendingClientKey("client", "sess-1")] = &ClientReplayState{
		Status:       ClientStreamStatusReplay,
		LastEventSeq: 2,
	}

	step := hub.collectReplayStep("client", "sess-1")
	if len(step.events) != 1 || step.events[0].EventCursor != "8:3" {
		t.Fatalf("replayed events = %#v; want only cursor 8:3", step.events)
	}
}

func TestCoalescedToolStreamAdvancesCursor(t *testing.T) {
	hub := NewStreamHub(nil)
	hub.SetPendingUserAt("root", "sess-1", "title", "", "", "", "", "", "", false, "command", time.Now(), 4)
	toolUpdate := func(text string) StreamEvent {
		return StreamEvent{
			Type: string(agenttypes.EventTypeToolUpdate),
			Data: agenttypes.ToolCall{
				CallID:  "call-1",
				Status:  "running",
				Meta:    map[string]any{"source": "userShell", "phase": "stream"},
				Content: []agenttypes.ToolCallContentItem{{Type: "text", Text: text}},
			},
		}
	}
	hub.AppendReplyEvent("sess-1", toolUpdate("hello "))
	hub.AppendReplyEvent("sess-1", toolUpdate("world"))
	hub.replayStates[pendingClientKey("client", "sess-1")] = &ClientReplayState{
		Status:       ClientStreamStatusReplay,
		LastEventSeq: 1,
	}

	step := hub.collectReplayStep("client", "sess-1")
	if len(step.events) != 1 || step.events[0].EventCursor != "4:2" {
		t.Fatalf("replayed events = %#v; want coalesced cursor 4:2", step.events)
	}
	tool, ok := step.events[0].Data.(agenttypes.ToolCall)
	if !ok || len(tool.Content) != 1 || tool.Content[0].Text != "hello world" {
		t.Fatalf("coalesced tool = %#v; want complete output", step.events[0].Data)
	}
}

func TestSessionMessageContextHasNoDeadlineWithoutAppContext(t *testing.T) {
	handler := &WSHandler{}

	ctx, cancel := handler.sessionMessageContext()
	defer cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("session message context unexpectedly has a deadline")
	}
}

func TestSessionMessageContextUsesAgentPoolLifecycle(t *testing.T) {
	pool := agent.NewPool(agent.Config{})
	defer pool.CloseAll()

	handler := &WSHandler{AppContext: &AppContext{Agents: pool}}
	ctx, cancel := handler.sessionMessageContext()
	defer cancel()

	if _, ok := ctx.Deadline(); ok {
		t.Fatal("session message context unexpectedly has a deadline")
	}
	pool.CloseAll()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("expected session message context to be canceled when agent pool closes")
	}
}

func TestSessionRuntimeRootPathUsesRelatedWorktree(t *testing.T) {
	current := &session.Session{
		Source: "worktree",
		RelatedWorktree: &session.RelatedWorktree{
			Path: "  /tmp/project-worktree  ",
		},
	}
	if got := sessionRuntimeRootPath(current); got != "/tmp/project-worktree" {
		t.Fatalf("sessionRuntimeRootPath() = %q, want %q", got, "/tmp/project-worktree")
	}
	if got := sessionRuntimeRootPath(&session.Session{}); got != "" {
		t.Fatalf("sessionRuntimeRootPath() without worktree = %q, want empty", got)
	}
	relatedOnly := &session.Session{
		RelatedWorktree: &session.RelatedWorktree{Path: "/tmp/observed-worktree"},
	}
	if got := sessionRuntimeRootPath(relatedOnly); got != "" {
		t.Fatalf("sessionRuntimeRootPath() for incidental relation = %q, want empty", got)
	}
}

func TestTurnUpdateTrackerWaitIdleWaitsForSettleWindow(t *testing.T) {
	tracker := newTurnUpdateTracker()
	tracker.Begin()
	done := make(chan bool, 1)
	go func() {
		done <- tracker.WaitIdle(context.Background(), 30*time.Millisecond, 500*time.Millisecond)
	}()

	select {
	case <-done:
		t.Fatal("WaitIdle returned while update was in-flight")
	case <-time.After(20 * time.Millisecond):
	}

	tracker.End()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("WaitIdle returned false after update finished")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("WaitIdle did not return after settle window")
	}
}

func TestTurnUpdateTrackerWaitIdleTimesOutWhenUpdateNeverEnds(t *testing.T) {
	tracker := newTurnUpdateTracker()
	tracker.Begin()

	if tracker.WaitIdle(context.Background(), 10*time.Millisecond, 30*time.Millisecond) {
		t.Fatal("expected WaitIdle to time out while update remains in-flight")
	}
}

func TestStreamHubFrozenQueueBlocksAutomaticPopUntilUnfrozen(t *testing.T) {
	hub := NewStreamHub(nil)
	rootID := "root"
	sessionKey := "session"

	hub.EnqueueSessionMessage(rootID, sessionKey, "Session", QueuedUserMessage{
		ID: "first",
		PendingUserMessage: PendingUserMessage{
			Content:   "first message",
			Timestamp: time.Now().UTC(),
		},
	})
	hub.EnqueueSessionMessage(rootID, sessionKey, "Session", QueuedUserMessage{
		ID: "second",
		PendingUserMessage: PendingUserMessage{
			Content:   "second message",
			Timestamp: time.Now().UTC(),
		},
	})

	frozenQueue, frozen := hub.FreezeQueuedSessionMessages(sessionKey)
	if !frozen {
		t.Fatal("expected queue freeze to succeed")
	}
	if len(frozenQueue) != 2 {
		t.Fatalf("expected frozen queue snapshot to contain 2 items, got %d", len(frozenQueue))
	}
	if _, queue, ok := hub.PopQueuedSessionMessage(sessionKey, ""); ok {
		t.Fatal("expected frozen queue to block automatic pop")
	} else if len(queue) != 2 {
		t.Fatalf("expected frozen queue to remain intact, got %d items", len(queue))
	}

	queue, ok := hub.PromoteQueuedSessionMessage(sessionKey, "second")
	if !ok {
		t.Fatal("expected promote to succeed")
	}
	if len(queue) != 2 || queue[0].ID != "second" {
		t.Fatalf("expected promoted item at queue head, got %#v", queue)
	}

	item, queue, ok := hub.PopQueuedSessionMessage(sessionKey, "")
	if !ok {
		t.Fatal("expected promoted queue to be unfrozen")
	}
	if item.ID != "second" {
		t.Fatalf("expected promoted item to pop first, got %q", item.ID)
	}
	if len(queue) != 1 || queue[0].ID != "first" {
		t.Fatalf("expected remaining queue to contain first item, got %#v", queue)
	}
}

func TestStreamHubUnfreezeQueueAllowsAutomaticPop(t *testing.T) {
	hub := NewStreamHub(nil)
	sessionKey := "session"
	hub.EnqueueSessionMessage("root", sessionKey, "Session", QueuedUserMessage{
		ID: "first",
		PendingUserMessage: PendingUserMessage{
			Content:   "first message",
			Timestamp: time.Now().UTC(),
		},
	})
	_, frozen := hub.FreezeQueuedSessionMessages(sessionKey)
	if !frozen {
		t.Fatal("expected queue freeze to succeed")
	}

	unfrozenQueue, changed := hub.UnfreezeQueuedSessionMessages(sessionKey)
	if !changed {
		t.Fatal("expected queue unfreeze to report changed")
	}
	if len(unfrozenQueue) != 1 {
		t.Fatalf("expected unfreeze queue snapshot to contain 1 item, got %d", len(unfrozenQueue))
	}
	item, queue, ok := hub.PopQueuedSessionMessage(sessionKey, "")
	if !ok {
		t.Fatal("expected automatic pop after unfreeze")
	}
	if item.ID != "first" {
		t.Fatalf("expected first item, got %q", item.ID)
	}
	if len(queue) != 0 {
		t.Fatalf("expected empty queue, got %#v", queue)
	}
}

func TestStreamHubSetPendingUserAtUsesProvidedTimestamp(t *testing.T) {
	hub := NewStreamHub(nil)
	want := time.Date(2026, 7, 29, 10, 0, 0, int(456*time.Millisecond), time.UTC)

	pending := hub.SetPendingUserAt("root", "session", "Session", "codex", "gpt-test", "", "", "", "", false, "hello", want)

	if pending == nil {
		t.Fatal("SetPendingUserAt returned nil")
	}
	if !pending.Timestamp.Equal(want) {
		t.Fatalf("pending timestamp = %s, want %s", pending.Timestamp.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

func TestReserveClientRequestKeepsOriginalTimestamp(t *testing.T) {
	handler := &WSHandler{}

	firstTimestamp, firstReserved := handler.reserveClientRequest("request-1")
	time.Sleep(time.Millisecond)
	secondTimestamp, secondReserved := handler.reserveClientRequest("request-1")

	if !firstReserved {
		t.Fatal("first request was not reserved")
	}
	if secondReserved {
		t.Fatal("duplicate request was reserved again")
	}
	if !secondTimestamp.Equal(firstTimestamp) {
		t.Fatalf("duplicate timestamp = %s, want %s", secondTimestamp.Format(time.RFC3339Nano), firstTimestamp.Format(time.RFC3339Nano))
	}
}

func TestRequireWSProofAcceptsValidProof(t *testing.T) {
	clientID := "web-test"
	key := []byte("0123456789abcdef0123456789abcdef")
	manager := e2ee.NewManager(e2ee.Config{
		Enabled:       true,
		NodeID:        "node",
		PairingSecret: "secret",
	})
	if _, err := manager.OpenSessionForClient(clientID, e2ee.DerivedKey{Transport: key}); err != nil {
		t.Fatalf("OpenSessionForClient: %v", err)
	}
	handler := &WSHandler{AppContext: &AppContext{E2EE: manager}}
	ts := time.Now().UTC().Format(time.RFC3339)
	proofPath := "/ws?client_id=" + url.QueryEscape(clientID)
	proof := e2ee.BuildRequestProof(key, http.MethodGet, proofPath, ts, clientID)
	req := httptest.NewRequest(http.MethodGet, proofPath+"&"+wsTSQuery+"="+url.QueryEscape(ts)+"&"+wsProofQuery+"="+url.QueryEscape(proof), nil)

	if err := handler.requireWSProof(req, clientID); err != nil {
		t.Fatalf("requireWSProof() error = %v", err)
	}
}

func TestRequireWSProofRejectsMissingProofWhenE2EEEnabled(t *testing.T) {
	clientID := "web-test"
	manager := e2ee.NewManager(e2ee.Config{
		Enabled:       true,
		NodeID:        "node",
		PairingSecret: "secret",
	})
	handler := &WSHandler{AppContext: &AppContext{E2EE: manager}}
	req := httptest.NewRequest(http.MethodGet, "/ws?client_id="+url.QueryEscape(clientID), nil)

	if err := handler.requireWSProof(req, clientID); err == nil {
		t.Fatal("expected missing proof to be rejected")
	}
}

func TestWSProofPathExcludesProofQueryParams(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws?client_id=web-test&e2ee_ts=now&e2ee_proof=proof", nil)

	if got, want := wsProofPath(req), "/ws?client_id=web-test"; got != want {
		t.Fatalf("wsProofPath() = %q, want %q", got, want)
	}
}

// 重放必须**整批一条消息**发。
//
// 逐条发 = 客户端「收一条 → 渲染一次」，几百条事件就是几百次渲染 —— 用户看到的
// 是「打开/切回会话时逐渐刷一大堆」（2026-10-06 实测确认，这是那条观感的直接成因）。
// 整批放进一条消息，客户端在同一事件处理器内循环应用，React 会批处理成一次渲染。
func TestReplayBatchIsOneMessage(t *testing.T) {
	events := []StreamEvent{
		{Type: string(agenttypes.EventTypeMessageChunk), Data: agenttypes.MessageChunk{Content: "a"}, EventCursor: "8:1"},
		{Type: string(agenttypes.EventTypeMessageChunk), Data: agenttypes.MessageChunk{Content: "b"}, EventCursor: "8:2"},
		{Type: string(agenttypes.EventTypeMessageChunk), Data: agenttypes.MessageChunk{Content: "c"}, EventCursor: "8:3"},
	}
	resp := buildSessionStreamBatchResponse("root", "sess-1", events, true)
	if resp.Type != "session.stream" {
		t.Fatalf("type = %q; want session.stream（客户端 dispatch 按类型分发）", resp.Type)
	}
	got, ok := resp.Payload["events"].([]StreamEvent)
	if !ok || len(got) != 3 {
		t.Fatalf("payload.events = %#v; want all 3 events in ONE message", resp.Payload["events"])
	}
	if _, hasSingle := resp.Payload["event"]; hasSingle {
		t.Fatalf("batch message must not also carry a single `event` key")
	}
	if resp.Payload["root_id"] != "root" || resp.Payload["session_key"] != "sess-1" {
		t.Fatalf("payload must keep root_id/session_key: %#v", resp.Payload)
	}
	if resp.Payload["reset"] != true {
		t.Fatalf("挂载首帧必须带 reset:true（客户端据此清瞬时尾巴）: %#v", resp.Payload)
	}
	if _, empty := resp.Payload["events"].([]StreamEvent); !empty {
		t.Fatalf("events 必须恒为数组（不能是 nil），空批也要能只清尾巴")
	}
}

// 排空期间的**续投**绝不能带 reset。
//
// 挂载首帧是快照（reset → 清尾巴再重建），但排空循环里新到的那些事件是**接在快照之后**
// 的增量：若它们也带 reset，客户端会把刚重建好的整份尾巴清掉，只留下这几条 ——
// 越活跃的会话越容易命中（休眠会话的排空循环一次就走完）。
func TestReplayDrainStepIsNotAReset(t *testing.T) {
	events := []StreamEvent{
		{Type: string(agenttypes.EventTypeMessageChunk), Data: agenttypes.MessageChunk{Content: "late"}, EventCursor: "8:99"},
	}
	resp := buildSessionStreamBatchResponse("root", "sess-1", events, false)
	if _, has := resp.Payload["reset"]; has {
		t.Fatalf("续投帧不得携带 reset（会清掉快照刚重建的尾巴）: %#v", resp.Payload)
	}
	// 但**形状**必须与快照帧一致（都是 events 数组），否则又退回「两条分发路径各认一种形状」。
	got, ok := resp.Payload["events"].([]StreamEvent)
	if !ok || len(got) != 1 {
		t.Fatalf("续投帧必须与快照帧同形状（events 数组）: %#v", resp.Payload["events"])
	}
	// 空 events 时也不能退化成 nil（客户端 Array.isArray 判据会漏掉）。
	empty := buildSessionStreamBatchResponse("root", "sess-1", nil, true)
	if arr, ok := empty.Payload["events"].([]StreamEvent); !ok || arr == nil {
		t.Fatalf("events 归一成空数组，不能是 nil: %#v", empty.Payload["events"])
	}
}

// 回合结束后**重新挂上来的**客户端不得再收到一条 done。
//
// 旧实现用一张 `completed` 表记住「这个会话上一轮什么时候结束的」，在客户端
// session.ready 时补发一条带 replay:true 的 done。客户端据此跳过重锚定 —— 但重锚定
// 本身会再发一次 session.ready，于是 done → ready → done 自持闭环（2026-09-13 实测
// 18 次/秒；2026-10-06 真机重跑复现 80 条 done / 78 次 ?latest=20，≈8 次/秒）。
//
// 补发那条回执本来就是多余的：真实结束的 done 用 liveOnly=false 广播，重放中的客户端
// 也在收件人里；而「挂上来时这一轮早就结束了」的客户端，其「在回复」状态来自 pending
// 列表（该会话已不在其中），不需要一条 done 来纠正。
func TestDoneCarriesNoReplayReceipt(t *testing.T) {
	resp := buildSessionDoneResponse("root", "sess-1", "req-1")
	if _, has := resp.Payload["replay"]; has {
		t.Fatalf("done 载荷不得带 replay 标记（会重启 done→ready 自持环）: %#v", resp.Payload)
	}
	if len(resp.Payload) != 2 {
		t.Fatalf("done 载荷只允许 root_id/session_key: %#v", resp.Payload)
	}
}

// 重放必须保留服务端事件时间戳。
//
// 手机切后台再恢复时客户端走的是**重放**路径（不是 live），而 AppendReplyEvent 存进
// ReplyingList 的是 cloneEvent(event)。cloneEvent 若只搬 Type/Data/EventCursor，
// 时间戳就丢了：重放事件只能回退到「恢复页面的接收时刻」，回复时长把整段后台时间算进去。
// live 路径不经 cloneEvent（BroadcastSessionStream 直接发 AppendReplyEvent 的返回值），
// 所以只有重放会错 —— 这正是「时长只在手机恢复后不对」的形态。
func TestReplayPreservesEventTimestamp(t *testing.T) {
	hub := NewStreamHub(nil)
	hub.SetPendingUserAt("root", "sess-1", "title", "codex", "", "", "", "", "", false, "prompt", time.Now(), 8)
	stored := hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeMessageChunk),
		Data: agenttypes.MessageChunk{Content: "answer"},
	})
	if stored.Timestamp.IsZero() {
		t.Fatal("AppendReplyEvent 必须给事件盖上服务端时间戳")
	}

	hub.mu.Lock()
	hub.replayStates[pendingClientKey("client", "sess-1")] = &ClientReplayState{Status: ClientStreamStatusReplay}
	hub.mu.Unlock()

	step := hub.collectReplayStep("client", "sess-1")
	if len(step.events) != 1 {
		t.Fatalf("replay events = %d; want 1", len(step.events))
	}
	if got := step.events[0].Timestamp; !got.Equal(stored.Timestamp) {
		t.Fatalf("重放事件时间戳 = %v; want %v（cloneEvent 丢了时间戳 → 客户端只能回退到恢复时刻）", got, stored.Timestamp)
	}
}

// 回合清空之后重新挂上来的客户端拿到的是**空快照**（一条 events 为空的 reset 帧），
// 不是上一轮的尾巴 —— 客户端据此清瞬时尾巴，且不会因此再触发一轮。
func TestReplayAfterClearYieldsEmptySnapshot(t *testing.T) {
	hub := NewStreamHub(nil)
	hub.SetPendingUserAt("root", "sess-1", "title", "codex", "", "", "", "", "", false, "prompt", time.Now(), 8)
	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeMessageChunk),
		Data: agenttypes.MessageChunk{Content: "answer"},
	})
	// AppContext.BroadcastSessionDone 在广播 done 之前做的事。
	hub.ClearSessionPending("sess-1")

	step := hub.collectReplayStep("client", "sess-1")
	if len(step.events) != 0 {
		t.Fatalf("清空后重放应为空快照，得到 %#v", step.events)
	}
	if !step.live {
		t.Fatal("清空后应直接进入 live（无重放内容可排空）")
	}
}
