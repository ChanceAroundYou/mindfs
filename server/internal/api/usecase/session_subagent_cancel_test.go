package usecase

import (
	"context"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// B（子代理卡死、停止按钮失灵）的针对性回归。
//
// 根因：Claude 合成子代理的订阅**从不 registerActiveTurn**，而且它的 runtime
// 是 syntheticAgentSession，CancelCurrentTurn 无条件返回 nil。于是
// Service.CancelSessionTurn 走到 `active == nil → return nil`，静默"成功"却什么
// 都没做：订阅不断、子会话不关、pending 不清 → 灯永久常亮、按钮永久失灵。
//
// 这组测试钉住三件事：
//  1. 子代理被 attach 时就把自己登记成"在跑的一轮"，取消请求才可能命中它；
//  2. 取消真的会关掉子会话并发出终帧（终帧是前端清灯的唯一来源）；
//  3. 已关闭的子代理后续更新必须被吃掉，不能落回父会话（否则取消反而把子代理
//     残余输出倒进父会话）。

func newClaudeSubagentTestRouter(t *testing.T, onUpdate func(string, agenttypes.Event)) (*claudeSubagentRouter, *session.Manager, *session.Session) {
	t.Helper()
	ctx := context.Background()
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	parent, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Name: "parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	router := newClaudeSubagentRouter(subagentSessionInput{
		RootID:   root.ID,
		Parent:   parent,
		Agent:    "claude",
		Model:    "sonnet",
		Mode:     "default",
		Effort:   "medium",
		Manager:  manager,
		OnUpdate: onUpdate,
	})
	return router, manager, parent
}

// openClaudeSubagentChild 走一遍真实路径：先来一段带 parentToolUseId 的增量，
// 子会话因此被创建并订阅。
func openClaudeSubagentChild(t *testing.T, router *claudeSubagentRouter) *session.Session {
	t.Helper()
	ctx := context.Background()
	if consumed := router.Handle(ctx, agenttypes.Event{
		Type: agenttypes.EventTypeMessageChunk,
		Data: agenttypes.MessageChunk{
			Content:         "child response",
			ParentToolUseID: "tool-1",
			TaskID:          "task-1",
		},
	}); !consumed {
		t.Fatalf("subagent chunk was not consumed")
	}
	children := router.children
	if len(children) != 1 || children[0] == nil {
		t.Fatalf("children = %#v, want exactly one attached child", children)
	}
	return children[0].child
}

func TestClaudeSubagentAttachRegistersActiveTurn(t *testing.T) {
	router, _, _ := newClaudeSubagentTestRouter(t, nil)
	child := openClaudeSubagentChild(t, router)
	defer unregisterActiveTurn(router.in.RootID, child.Key)

	turnID := ActiveTurnID(router.in.RootID, child.Key)
	if turnID == 0 {
		t.Fatal("ActiveTurnID = 0: 子代理没有登记活跃轮次，取消请求会按「无活跃轮」被丢弃")
	}
	active := getActiveTurn(router.in.RootID, child.Key)
	if active == nil || active.session == nil {
		t.Fatalf("active turn = %#v, want session bound", active)
	}
	if _, ok := active.session.(*syntheticAgentSession); !ok {
		t.Fatalf("active session = %T, want *syntheticAgentSession", active.session)
	}
}

func TestCancelSyntheticSubagentEmitsDoneAndClosesChild(t *testing.T) {
	var doneKeys []string
	router, manager, _ := newClaudeSubagentTestRouter(t, func(sessionKey string, update agenttypes.Event) {
		if update.Type == agenttypes.EventTypeMessageDone {
			doneKeys = append(doneKeys, sessionKey)
		}
	})
	child := openClaudeSubagentChild(t, router)
	turnID := ActiveTurnID(router.in.RootID, child.Key)
	service := Service{Registry: &commandTestRegistry{root: manager.Root(), manager: manager}}

	if err := service.CancelSessionTurn(context.Background(), CancelSessionTurnInput{
		RootID:       router.in.RootID,
		Key:          child.Key,
		TargetTurnID: turnID,
		TargetPinned: true,
	}); err != nil {
		t.Fatalf("CancelSessionTurn returned error: %v", err)
	}
	if len(doneKeys) == 0 {
		t.Fatal("取消后没有发出终帧：前端的灯没有别的来源可以熄灭")
	}
	if doneKeys[0] != child.Key {
		t.Fatalf("done emitted for %q, want %q", doneKeys[0], child.Key)
	}
	if !router.children[0].closed {
		t.Fatal("取消后子代理仍是打开状态")
	}
	if ActiveTurnID(router.in.RootID, child.Key) != 0 {
		t.Fatal("取消后活跃轮次没有注销")
	}
	// 掐断订阅的上下文也是落盘用的那个：先掐后落就会丢正文。灯灭了字不能跟着没。
	loaded, err := manager.Get(context.Background(), child.Key, 0)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if len(loaded.Exchanges) != 1 || loaded.Exchanges[0].LegacyContent != "child response" {
		t.Fatalf("child exchanges = %#v, want the partial response persisted", loaded.Exchanges)
	}
}

func TestClosedClaudeSubagentSwallowsFurtherUpdates(t *testing.T) {
	router, _, _ := newClaudeSubagentTestRouter(t, nil)
	openClaudeSubagentChild(t, router)
	router.FinishAll()

	consumed := router.Handle(context.Background(), agenttypes.Event{
		Type: agenttypes.EventTypeMessageChunk,
		Data: agenttypes.MessageChunk{
			Content:         "late child output",
			ParentToolUseID: "tool-1",
			TaskID:          "task-1",
		},
	})
	if !consumed {
		t.Fatal("已关闭子代理的更新落回了父会话：取消/收尾之后残余输出会串进父会话正文")
	}
}

func TestFinishAllUnregistersActiveTurns(t *testing.T) {
	router, _, _ := newClaudeSubagentTestRouter(t, nil)
	child := openClaudeSubagentChild(t, router)
	router.FinishAll()

	if ActiveTurnID(router.in.RootID, child.Key) != 0 {
		t.Fatal("FinishAll 之后活跃轮次仍在：下一次回合的取消会命中一个已死的子代理")
	}
}
