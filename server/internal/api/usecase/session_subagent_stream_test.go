package usecase

import (
	"context"
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
