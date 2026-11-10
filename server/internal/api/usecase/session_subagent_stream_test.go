package usecase

import (
	"context"
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func TestClaudeSubagentRouterCreatesChildSessionAndRoutesChunks(t *testing.T) {
	ctx := context.Background()
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	parent, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Name: "parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	var created *session.Session
	var sawDone bool
	router := newClaudeSubagentRouter(subagentSessionInput{
		RootID:  root.ID,
		Parent:  parent,
		Agent:   "claude",
		Model:   "sonnet",
		Mode:    "default",
		Effort:  "medium",
		Manager: manager,
		OnCreated: func(child *session.Session) {
			created = child
		},
		OnUpdate: func(sessionKey string, update agenttypes.Event) {
			if created != nil && sessionKey == created.Key && update.Type == agenttypes.EventTypeMessageDone {
				sawDone = true
			}
		},
	})

	parentTask := agenttypes.ToolCall{
		CallID:  "tool-1",
		Title:   "Review changes",
		Status:  "running",
		Kind:    agenttypes.ToolKindTask,
		RawType: "claude_task",
		Meta: map[string]any{
			"parentToolUseId": "tool-1",
			"taskId":          "task-1",
			"taskDescription": "Review changes",
		},
	}
	if consumed := router.Handle(ctx, agenttypes.Event{Type: agenttypes.EventTypeToolCall, Data: parentTask}); consumed {
		t.Fatalf("parent task lifecycle should not be consumed")
	}
	if created != nil {
		t.Fatalf("parent task lifecycle should not create child")
	}

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
	if created == nil {
		t.Fatalf("child session was not created")
	}
	if created.ParentSessionKey != parent.Key || created.ParentToolCallID != "tool-1" {
		t.Fatalf("created child parent fields = %#v", created)
	}
	if consumed := router.Handle(ctx, agenttypes.Event{
		Type: agenttypes.EventTypeMessageDone,
		Data: agenttypes.MessageDone{ParentToolUseID: "tool-1", TaskID: "task-1"},
	}); !consumed {
		t.Fatalf("subagent done was not consumed")
	}

	loaded, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if len(loaded.Exchanges) != 1 || loaded.Exchanges[0].LegacyContent != "child response" {
		t.Fatalf("child exchanges = %#v", loaded.Exchanges)
	}
	if !sawDone {
		t.Fatalf("sub session done update was not emitted")
	}
}

func TestClaudeSubagentRouterDoesNotCreateChildFromTaskIDOnly(t *testing.T) {
	ctx := context.Background()
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	parent, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Name: "parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	var createdCount int
	router := newClaudeSubagentRouter(subagentSessionInput{
		RootID:  root.ID,
		Parent:  parent,
		Agent:   "claude",
		Manager: manager,
		OnCreated: func(*session.Session) {
			createdCount++
		},
	})

	taskOnly := agenttypes.ToolCall{
		CallID:  "task-opaque-id",
		Title:   "Task progress",
		Status:  "running",
		Kind:    agenttypes.ToolKindTask,
		RawType: "claude_task",
		Meta: map[string]any{
			"taskId": "task-opaque-id",
		},
	}
	if consumed := router.Handle(ctx, agenttypes.Event{Type: agenttypes.EventTypeToolUpdate, Data: taskOnly}); consumed {
		t.Fatalf("task lifecycle should stay on parent")
	}
	if createdCount != 0 {
		t.Fatalf("createdCount = %d, want 0", createdCount)
	}

	if consumed := router.Handle(ctx, agenttypes.Event{
		Type: agenttypes.EventTypeMessageChunk,
		Data: agenttypes.MessageChunk{Content: "orphan task text", TaskID: "task-opaque-id"},
	}); consumed {
		t.Fatalf("task-id-only chunk should not create child")
	}
	if createdCount != 0 {
		t.Fatalf("createdCount after task chunk = %d, want 0", createdCount)
	}

	if consumed := router.Handle(ctx, agenttypes.Event{
		Type: agenttypes.EventTypeMessageChunk,
		Data: agenttypes.MessageChunk{
			Content:         "subagent text",
			ParentToolUseID: "call_e365874817b34ca79e665ee9",
			TaskID:          "task-opaque-id",
		},
	}); !consumed {
		t.Fatalf("parent-tool-use chunk should create child")
	}
	if createdCount != 1 {
		t.Fatalf("createdCount after parent chunk = %d, want 1", createdCount)
	}
}

func TestClaudeSubagentRouterRoutesTaskNotificationSummaryAndKeepsParentUpdate(t *testing.T) {
	ctx := context.Background()
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	parent, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Name: "parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	var created *session.Session
	var sawDone bool
	router := newClaudeSubagentRouter(subagentSessionInput{
		RootID:  root.ID,
		Parent:  parent,
		Agent:   "claude",
		Manager: manager,
		OnCreated: func(child *session.Session) {
			created = child
		},
		OnUpdate: func(sessionKey string, update agenttypes.Event) {
			if created != nil && sessionKey == created.Key && update.Type == agenttypes.EventTypeMessageDone {
				sawDone = true
			}
		},
	})

	if consumed := router.Handle(ctx, agenttypes.Event{
		Type: agenttypes.EventTypeMessageChunk,
		Data: agenttypes.MessageChunk{
			Content:         "working",
			ParentToolUseID: "tool-1",
			TaskID:          "task-1",
		},
	}); !consumed {
		t.Fatalf("subagent chunk was not consumed")
	}

	notification := agenttypes.ToolCall{
		CallID:  "tool-1",
		Title:   "done: printed hi",
		Status:  "complete",
		Kind:    agenttypes.ToolKindTask,
		RawType: "claude_task",
		Meta: map[string]any{
			"parentToolUseId": "tool-1",
			"taskId":          "task-1",
			"subtype":         "task_notification",
			"summary":         "done: printed hi",
		},
	}
	if consumed := router.Handle(ctx, agenttypes.Event{Type: agenttypes.EventTypeToolUpdate, Data: notification}); consumed {
		t.Fatalf("terminal task update should still be visible on parent")
	}

	loaded, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if len(loaded.Exchanges) != 1 || loaded.Exchanges[0].LegacyContent != "workingdone: printed hi" {
		t.Fatalf("child exchanges = %#v", loaded.Exchanges)
	}
	if !sawDone {
		t.Fatalf("sub session done update was not emitted")
	}
}

// WP-00 复现：子代理先出工具卡、后出正文时，工具卡不得落进父会话。
//
// 子会话要到第一条带 parentToolUseId 的正文才物化（见 openClaudeSubagentChild）。
// 在它之前到达的 child-owned ToolCall/ToolUpdate 自带 subagentType —— SDK 只在
// 子代理消息上填这个字段 —— 归属已经足够明确，却被 router 的
// 「无 child 时 ToolCall 一律放行」退回父回合 accumulator：用户看到的就是子会话
// 的内部工具卡串进主会话时间线。
func TestClaudeSubagentRouterNeverLeaksChildOwnedEventsIntoParent(t *testing.T) {
	var childKey string
	var childUpdates []agenttypes.Event
	router, manager, _ := newClaudeSubagentTestRouter(t, func(sessionKey string, update agenttypes.Event) {
		childKey = sessionKey
		childUpdates = append(childUpdates, update)
	})

	var parentText strings.Builder
	var parentToolCards []agenttypes.ToolCall
	// 与 session_send.go 的 attachSessionUpdates 同构：router 消费的事件不再进父回合。
	route := func(update agenttypes.Event) bool {
		if router.Handle(context.Background(), update) {
			return true
		}
		switch data := update.Data.(type) {
		case agenttypes.MessageChunk:
			parentText.WriteString(data.Content)
		case agenttypes.ToolCall:
			parentToolCards = append(parentToolCards, data)
		}
		return false
	}

	route(agenttypes.Event{Type: agenttypes.EventTypeMessageChunk, Data: agenttypes.MessageChunk{Content: "parent-before "}})
	// 子代理的第一条事件是它自己的工具卡，此前还没有任何正文增量。
	route(agenttypes.Event{Type: agenttypes.EventTypeToolCall, Data: agenttypes.ToolCall{
		CallID:  "child-read-1",
		Title:   "Read main.go",
		Status:  "running",
		Kind:    agenttypes.ToolKindRead,
		RawType: "tool_use",
		Meta: map[string]any{
			"parentToolUseId": "tool-child-1",
			"taskId":          "task-child-1",
			"subagentType":    "Explore",
			"taskDescription": "Inspect files",
		},
	}})
	route(agenttypes.Event{Type: agenttypes.EventTypeMessageChunk, Data: agenttypes.MessageChunk{
		Content:         "CHILD_ONLY_SECRET",
		ParentToolUseID: "tool-child-1",
		TaskID:          "task-child-1",
		SubagentType:    "Explore",
		TaskDescription: "Inspect files",
	}})
	route(agenttypes.Event{Type: agenttypes.EventTypeToolUpdate, Data: agenttypes.ToolCall{
		CallID:  "child-read-1",
		Title:   "Read main.go",
		Status:  "complete",
		Kind:    agenttypes.ToolKindRead,
		RawType: "tool_use",
		Meta: map[string]any{
			"parentToolUseId": "tool-child-1",
			"taskId":          "task-child-1",
			"subagentType":    "Explore",
		},
	}})
	route(agenttypes.Event{Type: agenttypes.EventTypeMessageChunk, Data: agenttypes.MessageChunk{Content: "parent-after"}})
	route(agenttypes.Event{Type: agenttypes.EventTypeMessageDone, Data: agenttypes.MessageDone{
		ParentToolUseID: "tool-child-1",
		TaskID:          "task-child-1",
	}})

	if got := parentText.String(); got != "parent-before parent-after" {
		t.Fatalf("parent body = %q, want only source parent chunks", got)
	}
	if len(parentToolCards) != 0 {
		t.Fatalf("child-owned tool cards leaked into parent: %#v", parentToolCards)
	}
	if childKey == "" {
		t.Fatal("child session was never attached")
	}
	var sawToolCall bool
	for _, update := range childUpdates {
		if update.Type != agenttypes.EventTypeToolCall {
			continue
		}
		if toolCall, ok := update.Data.(agenttypes.ToolCall); ok && toolCall.CallID == "child-read-1" {
			sawToolCall = true
		}
	}
	if !sawToolCall {
		t.Fatalf("child never received its own tool card; updates=%#v", childUpdates)
	}
	child, err := manager.Get(context.Background(), childKey, 0)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if len(child.Exchanges) != 1 || child.Exchanges[0].LegacyContent != "CHILD_ONLY_SECRET" {
		t.Fatalf("child exchanges = %#v", child.Exchanges)
	}
}

// 无归属信息的事件不得被认领成子代理（否则会凭空建出子会话，并把父正文拆走）。
// 这里同时钉住现状：它仍归父会话 —— 适配层拿不到归属时没有第三条安全 lane，
// 这是已记录的诊断限制，不是修复。
func TestClaudeUnattributedDeltaIsNeverClaimedAsChild(t *testing.T) {
	router, _, _ := newClaudeSubagentTestRouter(t, nil)

	consumed := router.Handle(context.Background(), agenttypes.Event{
		Type: agenttypes.EventTypeMessageChunk,
		Data: agenttypes.MessageChunk{Content: "unattributed text"},
	})
	if consumed {
		t.Fatal("无归属增量被 router 消费：没有稳定 child 身份时不得猜测归属")
	}
	if len(router.children) != 0 {
		t.Fatalf("无归属增量建出了子会话：%#v", router.children)
	}
}

// 已关闭子代理的迟到工具事件也必须被吃掉，不能落回父会话。
// 现有的 TestClosedClaudeSubagentSwallowsFurtherUpdates 只覆盖 MessageChunk。
func TestClaudeLateChildToolEventsNeverFallBackToParent(t *testing.T) {
	router, _, _ := newClaudeSubagentTestRouter(t, nil)
	openClaudeSubagentChild(t, router)
	router.FinishAll()

	consumed := router.Handle(context.Background(), agenttypes.Event{
		Type: agenttypes.EventTypeToolCall,
		Data: agenttypes.ToolCall{
			CallID:  "late-child-tool",
			Title:   "Read late.go",
			Status:  "running",
			Kind:    agenttypes.ToolKindRead,
			RawType: "tool_use",
			Meta: map[string]any{
				"parentToolUseId": "tool-1",
				"taskId":          "task-1",
				"subagentType":    "Explore",
			},
		},
	})
	if !consumed {
		t.Fatal("已关闭子代理的迟到工具事件落回了父会话")
	}
}
