package api

import (
	"strconv"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/notify"
)

func TestAppendReplyEventPrefixesTruncatedSummary(t *testing.T) {
	hub := NewStreamHub()

	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: "message_chunk",
		Data: agenttypes.MessageChunk{Content: strings.Repeat("前", notify.BodyMaxRunes+1) + "后"},
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
	hub := NewStreamHub()

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
	hub := NewStreamHub()
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
	hub := NewStreamHub()
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
	hub := NewStreamHub()
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
	resp := buildSessionDoneResponse("root", "sess-1", "req-1", false)
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
	hub := NewStreamHub()
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
	hub := NewStreamHub()
	_, turnID := hub.SetPendingUserAt("root", "sess-1", "title", "codex", "", "", "", "", "", false, "prompt", time.Now(), 8)
	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeMessageChunk),
		Data: agenttypes.MessageChunk{Content: "answer"},
	})
	// AppContext.EndSessionTurn 在广播 done 之前做的事。
	hub.ClearSessionPending("sess-1", turnID)

	step := hub.collectReplayStep("client", "sess-1")
	if len(step.events) != 0 {
		t.Fatalf("清空后重放应为空快照，得到 %#v", step.events)
	}
	if !step.live {
		t.Fatal("清空后应直接进入 live（无重放内容可排空）")
	}
}

// 连续文本块在写入时合并：重放的历史里，一段长回答只占一条事件。
//
// 单轮跑数小时的会话，message_chunk 会到数万条；不合并的话挂载时要在全局锁内
// 遍历/排序/序列化整份列表 —— 且一条长文本流的每一片都是独立事件。
// 合并只在「无在途重放」时做：见 TestReplayChunksStayUnmergedWhileReplayActive。
func TestContiguousChunksMergeIntoOneReplayEvent(t *testing.T) {
	hub := NewStreamHub()
	hub.SetPendingUserAt("root", "sess-1", "title", "codex", "", "", "", "", "", false, "prompt", time.Now(), 8)

	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeMessageChunk),
		Data: agenttypes.MessageChunk{MessageID: "m1", Content: "a"},
	})
	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeMessageChunk),
		Data: agenttypes.MessageChunk{MessageID: "m1", Content: "b"},
	})
	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeToolCall),
		Data: agenttypes.ToolCall{CallID: "call-1", Title: "Bash"},
	})
	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type: string(agenttypes.EventTypeMessageChunk),
		Data: agenttypes.MessageChunk{MessageID: "m1", Content: "c"},
	})

	// 重放状态在写入之后才出现：写入路径只看「此刻是否有客户端在续投」。
	hub.mu.Lock()
	hub.replayStates[pendingClientKey("client", "sess-1")] = &ClientReplayState{Status: ClientStreamStatusReplay}
	hub.mu.Unlock()
	step := hub.collectReplayStep("client", "sess-1")
	if len(step.events) != 3 {
		t.Fatalf("replay events = %d; want 3（ab 合并、tool、c 各一条）", len(step.events))
	}
	if step.events[0].EventCursor != "8:2" {
		t.Fatalf("merged cursor = %q; want 8:2（取被合并事件的最新 cursor）", step.events[0].EventCursor)
	}
	chunk, ok := step.events[0].Data.(agenttypes.MessageChunk)
	if !ok || chunk.Content != "ab" {
		t.Fatalf("merged chunk = %#v; want content %q", step.events[0].Data, "ab")
	}
	if step.events[1].EventCursor != "8:3" || step.events[2].EventCursor != "8:4" {
		t.Fatalf("cursors = %q, %q; want 8:3, 8:4（顺序与位置不得被打乱）", step.events[1].EventCursor, step.events[2].EventCursor)
	}
}

// 有客户端正在重放时不得原地合并已投递事件：续投是按 cursor 增量下发的，
// 把一条已发过的事件改成更大的累积快照，客户端会当成新内容再拼一遍。
func TestReplayChunksStayUnmergedWhileReplayActive(t *testing.T) {
	hub := NewStreamHub()
	hub.SetPendingUserAt("root", "sess-1", "title", "codex", "", "", "", "", "", false, "prompt", time.Now(), 8)
	hub.mu.Lock()
	hub.replayStates[pendingClientKey("client", "sess-1")] = &ClientReplayState{Status: ClientStreamStatusReplay}
	hub.mu.Unlock()

	for _, text := range []string{"a", "b"} {
		hub.AppendReplyEvent("sess-1", StreamEvent{
			Type: string(agenttypes.EventTypeMessageChunk),
			Data: agenttypes.MessageChunk{MessageID: "m1", Content: text},
		})
	}

	hub.mu.Lock()
	defer hub.mu.Unlock()
	if got := len(hub.pendingSessions["sess-1"].ReplyingList); got != 2 {
		t.Fatalf("replying list = %d events; want 2（在途重放期间不得合并）", got)
	}
}

func TestReplyListBoundedByCount(t *testing.T) {
	hub := NewStreamHub()
	hub.SetPendingUserAt("root", "sess-1", "title", "codex", "", "", "", "", "", false, "prompt", time.Now(), 8)
	total := maxReplayEvents + 500
	for i := 0; i < total; i++ {
		hub.AppendReplyEvent("sess-1", StreamEvent{
			Type: string(agenttypes.EventTypeThoughtChunk),
			// 不同 ID → 不合并，逐个计入条数上限。
			Data: agenttypes.ThoughtChunk{ID: "th-" + strconv.Itoa(i), Content: "x"},
		})
	}

	hub.mu.Lock()
	state := hub.pendingSessions["sess-1"]
	size := len(state.ReplyingList)
	last := state.ReplyingList[size-1].EventCursor
	hub.mu.Unlock()

	if size > maxReplayEvents {
		t.Fatalf("replying list = %d; want <= %d", size, maxReplayEvents)
	}
	if size < maxReplayEvents/2 {
		t.Fatalf("replying list = %d; trimmed too aggressively (would lose recent history)", size)
	}
	if want := "8:" + strconv.Itoa(total); last != want {
		t.Fatalf("newest cursor = %q; want %q（裁剪只能丢最旧的）", last, want)
	}
}

func TestReplyListBoundedByBytes(t *testing.T) {
	hub := NewStreamHub()
	hub.SetPendingUserAt("root", "sess-1", "title", "codex", "", "", "", "", "", false, "prompt", time.Now(), 8)
	blob := strings.Repeat("x", 512*1024)
	const events = 40 // ≈ 20 MB，超过 16 MB 预算
	for i := 0; i < events; i++ {
		hub.AppendReplyEvent("sess-1", StreamEvent{
			Type: string(agenttypes.EventTypeToolCall),
			Data: agenttypes.ToolCall{
				CallID:  "call-" + strconv.Itoa(i),
				Title:   "Bash",
				Content: []agenttypes.ToolCallContentItem{{Type: "text", Text: blob}},
			},
		})
	}

	hub.mu.Lock()
	state := hub.pendingSessions["sess-1"]
	size := len(state.ReplyingList)
	bytes := state.replyBytes
	lastCall := state.ReplyingList[size-1].Data.(agenttypes.ToolCall).CallID
	hub.mu.Unlock()

	if bytes > maxReplayBytes {
		t.Fatalf("replyBytes = %d; want <= %d", bytes, maxReplayBytes)
	}
	if size >= events {
		t.Fatalf("replying list = %d events; want < %d（字节超预算时必须裁剪）", size, events)
	}
	if lastCall != "call-"+strconv.Itoa(events-1) {
		t.Fatalf("newest call = %q; want the last appended one（裁剪只能丢最旧的）", lastCall)
	}
}

// 不同回合的相邻文本块不得合并：它们属于两条独立的 assistant 消息，拼在一起
// 客户端会看到上一轮的尾巴并进这一轮。
func TestChunksAcrossTurnsStaySeparate(t *testing.T) {
	hub := NewStreamHub()
	hub.SetPendingUserAt("root", "sess-1", "title", "codex", "", "", "", "", "", false, "prompt", time.Now(), 8)

	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type:     string(agenttypes.EventTypeMessageChunk),
		Data:     agenttypes.MessageChunk{MessageID: "m1", Content: "turn1"},
		MindFSID: "turn-1",
	})
	hub.AppendReplyEvent("sess-1", StreamEvent{
		Type:     string(agenttypes.EventTypeMessageChunk),
		Data:     agenttypes.MessageChunk{MessageID: "m1", Content: "turn2"},
		MindFSID: "turn-2",
	})

	hub.mu.Lock()
	defer hub.mu.Unlock()
	if got := len(hub.pendingSessions["sess-1"].ReplyingList); got != 2 {
		t.Fatalf("replying list = %d events; want 2（跨回合不得合并）", got)
	}
}
