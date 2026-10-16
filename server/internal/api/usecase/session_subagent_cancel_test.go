package usecase

import (
	"context"
	"strings"
	"sync"
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

// 卡死 + 停止无效的根因回归。
//
// 子代理的订阅上下文是从**父回合的 turnCtx** 派生的（session_subagents.go:189），
// 而落盘用的正是这个上下文。父回合一结束 turnCtx 就被掐，`defer FinishAll()`
// 此时才跑 close()→done()→落盘 —— 全部撞上 context canceled：
//   - 正文/aux/块表写不进去（日志形态 `[subagent] persist.blocks.error err=context canceled`）；
//   - 用户看到子会话停在中途、灯常亮、点停止没反应（取消命中的是一个已死的会话）。
//
// 这里钉住：父 turnCtx 已取消之后，子代理的落盘与终帧必须仍然成功。
func TestFinishAllAfterParentTurnContextCancelStillPersists(t *testing.T) {
	var doneKeys []string
	router, manager, _ := newClaudeSubagentTestRouter(t, func(sessionKey string, update agenttypes.Event) {
		if update.Type == agenttypes.EventTypeMessageDone {
			doneKeys = append(doneKeys, sessionKey)
		}
	})
	// 换成真实世界里那个可取消的父回合上下文（现有 helper 用的是 Background，
	// 这正是它测不出这个 bug 的原因）。
	parentCtx, cancelParent := context.WithCancel(context.Background())
	router.in.Ctx = parentCtx

	child := openClaudeSubagentChild(t, router)
	defer unregisterActiveTurn(router.in.RootID, child.Key)

	// 父回合结束：真实世界里 session.done / 取消 / 出错都会走到这一步。
	cancelParent()

	router.FinishAll()

	if len(doneKeys) == 0 || doneKeys[0] != child.Key {
		t.Fatalf("父回合上下文取消后子代理没发出终帧：前端清不掉这盏灯（doneKeys=%v）", doneKeys)
	}
	loaded, err := manager.Get(context.Background(), child.Key, 0)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if len(loaded.Exchanges) == 0 {
		t.Fatal("父回合上下文取消后子代理正文没落盘：取消/收尾会把子代理的输出整段丢掉")
	}
	blocks, err := manager.ReadBlocks(context.Background(), child.Key)
	if err != nil {
		t.Fatalf("list blocks: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("父回合上下文取消后子代理块表没落盘：双写块流（S2a）在子会话上整轮回退，" +
			"而 finish 只 log 不返回，这条丢失是静默的（线上形态 persist.blocks.error err=context canceled）")
	}
}

// 子会话灯永久残留（「主会话运行状况没有被结束」）的根因回归。
//
// 完整因果链（证据来自 PC 节点线上，session 1792108831-a090328fbb21）：
//   - 一个 **普通 Bash 工具调用**（claude 转录里的 tool_use:Bash）被上游带上了
//     ParentToolUseID，于是 ensure() 的**认领已有 binding 分支**被走中；
//   - 该分支只 r.attach() + bindKeys()，**从不调 in.OnCreated**（只有 create 分支调）。
//     OnCreated 是置 pending 的唯一入口，于是这条子会话要么根本没灯，要么灯由**别的**
//     回合置下 —— 两种情况都没有任何代码持有它的 turn id；
//   - close()/FinishAll() 只注销活跃轮次并落盘，**从不调终结器**，pending 无人清理；
//   - ws.go / appcontext.go / tasks.go 的 defer drain 只扫自己 map 里登记过的 key，
//     这条从未登记，永久漏在 drain 之外；
//   - 前端 descendantRunning() 沿 parent_session_key 读到这条 Active 的 pending，
//     把父会话解析成 delegating —— 用户手动停了子代理，父会话依旧显示在跑。
//
// 这里钉住：无论子会话走哪条分支进来，attach 时就必须发到 turn id，且 close() 必须
// 自己终结自己那一轮。
func TestAdoptedSubagentTurnIsClosedByClose(t *testing.T) {
	// attached / closed **分开记**：合成一个 map 会让「attach 登记过、但 close 从不
	// 调用终结器」这个正是要抓的缺陷被误判成通过。
	var attachedMu, closedMu sync.Mutex
	attachedIDs := map[string]string{}
	closedIDs := map[string]string{}
	router, manager, _ := newClaudeSubagentTestRouter(t, nil)
	router.in.OnSubAgentAttached = func(child *session.Session, turnID string) {
		if child == nil {
			return
		}
		attachedMu.Lock()
		attachedIDs[child.Key] = turnID
		attachedMu.Unlock()
	}
	router.in.OnSubAgentTurnFinished = func(sessionKey, turnID string) {
		closedMu.Lock()
		closedIDs[sessionKey] = turnID
		closedMu.Unlock()
	}

	// 造一条**已有 binding** 的子会话，再让 router 通过认领分支接管它 —— 这正是
	// 线上那条泄漏子会话的创建路径。
	existing, err := manager.Create(context.Background(), session.CreateInput{
		Type:             session.TypeChat,
		ParentSessionKey: router.in.Parent.Key,
		ParentToolCallID: "call_QmtkKWzGDduNk8p61epcsyzs",
		Agent:            "claude",
		Name:             "Bash",
	})
	if err != nil {
		t.Fatalf("create existing child: %v", err)
	}
	if err := manager.UpsertAgentBinding(context.Background(), session.AgentBinding{
		SessionKey:     existing.Key,
		Agent:          "claude",
		AgentSessionID: "claude-subagent:call_QmtkKWzGDduNk8p61epcsyzs",
	}); err != nil {
		t.Fatalf("upsert binding: %v", err)
	}

	consumed := router.Handle(context.Background(), agenttypes.Event{
		Type: agenttypes.EventTypeMessageChunk,
		Data: agenttypes.MessageChunk{
			Content:         "child response",
			ParentToolUseID: "call_QmtkKWzGDduNk8p61epcsyzs",
		},
	})
	if !consumed {
		t.Fatal("认领分支没有接管这个子会话")
	}
	if len(router.children) != 1 {
		t.Fatalf("children = %#v, want exactly one adopted child", router.children)
	}
	defer unregisterActiveTurn(router.in.RootID, existing.Key)

	// 认领分支也必须是「发到 id 的一轮」。
	attachedMu.Lock()
	attachedID := attachedIDs[existing.Key]
	attachedMu.Unlock()
	if strings.TrimSpace(attachedID) == "" {
		t.Fatal("认领分支 attach 时没有拿到 turn id：这条子会话永远没有可终结的回合，" +
			"pending 会永久残留（线上 1792108831 的症状）")
	}
	// 收尾之前先确认 close 确实没被提前调用过（否则下面的断言是空转）。
	closedMu.Lock()
	premature := closedIDs[existing.Key]
	closedMu.Unlock()
	if premature != "" {
		t.Fatalf("close 之前终结器就被调用了 id=%q：这不是一次由收尾触发的终结", premature)
	}
	// close() 自己终结自己那一轮，不依赖任何调用方的 drain map。
	router.FinishAll()
	closedMu.Lock()
	closedID := closedIDs[existing.Key]
	closedMu.Unlock()
	if closedID == "" {
		t.Fatal("close() 之后终结器从未被调用：pending 无人清理，父会话会被前端 " +
			"descendantRunning() 永久判成 delegating（用户停了子代理，主会话依旧显示在跑）")
	}
	if closedID != attachedID {
		t.Fatalf("close() 终结用的 id = %q, attach 时发的 id = %q：身份不匹配会让清理变成 no-op", closedID, attachedID)
	}
}
