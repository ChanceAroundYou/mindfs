package api

import (
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

// C（父会话"问完就完成、灯灭；答完又亮"）的针对性回归。
//
// 现象：AskUserQuestion 把一个逻辑任务切成两轮 —— 提问轮 done 时灯灭，用户以为
// 任务结束，其实 agent 在等他回答；答完起新轮，灯又亮，于是"完成/运行"反复横跳。
// 修向 (b)：turn 语义不动，用空心灯表达"在等你回答"。
//
// 空心灯需要一条 per-session 的"有未回答的提问"信号，它必须**活过回合结束**
// （提问轮 done 之后才是要显示的时候），所以不能只挂在"这一轮"上。

func TestAwaitingAnswerSurvivesTurnEnd(t *testing.T) {
	hub := NewStreamHub()
	turnID := hub.SetPendingReply("root", "s1", "title")
	hub.MarkSessionAwaiting("root", "s1", "call-1", true)
	hub.ClearSessionPending("s1", turnID) // 提问轮 done

	if !hub.AwaitingAnswer("s1") {
		t.Fatal("回合结束后 awaiting 丢了 —— 空心灯只会在回合进行中闪现，等于没修")
	}
	if pendingActive(hub, "s1") {
		t.Fatal("等回答 ≠ 在回复：它一旦进了 /api/replying-sessions 就会点亮实心灯")
	}
}

func TestAwaitingAnswerClearedByAnswer(t *testing.T) {
	hub := NewStreamHub()
	hub.SetPendingReply("root", "s1", "title")
	hub.MarkSessionAwaiting("root", "s1", "call-1", true)
	hub.MarkSessionAwaiting("root", "s1", "call-1", false) // 用户答了 / 工具完成

	if hub.AwaitingAnswer("s1") {
		t.Fatal("回答之后 awaiting 没清：空心灯会永久挂在这条会话上")
	}
}

func TestAwaitingAnswerClearedByNewTurn(t *testing.T) {
	hub := NewStreamHub()
	turnID := hub.SetPendingReply("root", "s1", "title")
	hub.MarkSessionAwaiting("root", "s1", "call-1", true)
	hub.ClearSessionPending("s1", turnID)
	hub.SetPendingReply("root", "s1", "title") // 用户直接在输入框回了一句

	if hub.AwaitingAnswer("s1") {
		t.Fatal("新一轮开始后 awaiting 仍在：这一轮结束时会被误判成「在等你回答」")
	}
}

// 空会话键是脏数据，不能造出一条无主条目（ListReplyingSessions 按 key 遍历）。
func TestAwaitingAnswerIgnoresBlankKey(t *testing.T) {
	hub := NewStreamHub()
	hub.MarkSessionAwaiting("root", "  ", "call-1", true)
	if len(hub.ListReplyingSessions()) != 0 {
		t.Fatal("空会话键产生了 pending 条目")
	}
}

func TestSessionListResponseCarriesAwaitingAnswer(t *testing.T) {
	app := &AppContext{}
	app.GetSessionStreamHub().MarkSessionAwaiting("root", "s1", "call-1", true)
	h := &HTTPHandler{AppContext: app}

	item := h.sessionListResponse(&session.Session{Key: "s1"})
	if item["awaiting_answer"] != true {
		t.Fatalf("会话列表项没带上 awaiting_answer: %#v", item["awaiting_answer"])
	}
	other := h.sessionListResponse(&session.Session{Key: "s2"})
	if _, ok := other["awaiting_answer"]; ok {
		t.Fatal("别的会话被误标成 awaiting")
	}
}

// 接线：置位/清位必须由**流事件本身**驱动 —— 否则要么永远不亮，要么永远不灭。
func TestBroadcastSessionUpdateDrivesAwaitingAnswer(t *testing.T) {
	app := &AppContext{}
	hub := app.GetSessionStreamHub()
	hub.SetPendingReply("root", "s1", "title")

	app.BroadcastSessionUpdate("root", "s1", agenttypes.Event{
		Type: agenttypes.EventTypeToolCall,
		Data: agenttypes.ToolCall{CallID: "call-1", Kind: agenttypes.ToolKindAskUser, Status: "running"},
	})
	if !hub.AwaitingAnswer("s1") {
		t.Fatal("ask_user 的 tool_call 没有置位 awaiting：空心灯永远不会亮")
	}

	// 无关工具不能误置位。
	app.BroadcastSessionUpdate("root", "s2", agenttypes.Event{
		Type: agenttypes.EventTypeToolCall,
		Data: agenttypes.ToolCall{CallID: "call-2", Kind: "read", Status: "running"},
	})
	if hub.AwaitingAnswer("s2") {
		t.Fatal("非 ask_user 的工具调用被误判成提问")
	}

	app.BroadcastSessionUpdate("root", "s1", agenttypes.Event{
		Type: agenttypes.EventTypeToolUpdate,
		Data: agenttypes.ToolCall{CallID: "call-1", Kind: agenttypes.ToolKindAskUser, Status: "complete"},
	})
	if hub.AwaitingAnswer("s1") {
		t.Fatal("ask_user 完成后没有清位：空心灯会一直挂着")
	}
}
