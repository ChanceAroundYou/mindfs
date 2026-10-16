package usecase

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func TestSendCommandMessagePersistsFinalToolCallAndSuggestion(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	registry := &commandTestRegistry{root: root, manager: manager}
	service := Service{Registry: registry}

	created, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeCommand,
		Name: "Command",
	})
	if err != nil {
		t.Fatalf("create command session: %v", err)
	}

	var sawStart, sawFinal, sawDone bool
	err = service.SendMessage(context.Background(), SendMessageInput{
		RootID:  root.ID,
		Key:     created.Key,
		Content: "printf mindfs-command",
		OnUpdate: func(event agenttypes.Event) {
			if event.Type == agenttypes.EventTypeMessageDone {
				sawDone = true
				return
			}
			toolCall, ok := event.Data.(agenttypes.ToolCall)
			if !ok {
				return
			}
			if toolCall.Meta["source"] != "userShell" {
				return
			}
			switch toolCall.Meta["phase"] {
			case "start":
				sawStart = true
			case "final":
				sawFinal = true
				if toolCall.Status != "success" {
					t.Fatalf("final status = %q meta=%#v content=%#v, want success", toolCall.Status, toolCall.Meta, toolCall.Content)
				}
				if len(toolCall.Content) == 0 || !strings.Contains(toolCall.Content[0].Text, "mindfs-command") {
					t.Fatalf("final content = %#v, want command output", toolCall.Content)
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("SendMessage command returned error: %v", err)
	}
	if !sawStart || !sawFinal || !sawDone {
		t.Fatalf("events sawStart=%v sawFinal=%v sawDone=%v", sawStart, sawFinal, sawDone)
	}

	current, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if len(current.Exchanges) != 2 {
		t.Fatalf("exchange count = %d, want 2", len(current.Exchanges))
	}
	aux, err := manager.GetExchangeAux(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get aux: %v", err)
	}
	if len(aux[2]) != 1 || aux[2][0].ToolCall == nil {
		t.Fatalf("aux[2] = %#v, want final command toolcall", aux[2])
	}

	candidates, err := SearchCommandSuggestions(context.Background(), manager, root.ID, "printf", 10)
	if err != nil {
		t.Fatalf("SearchCommandSuggestions: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Name != "printf mindfs-command" {
		t.Fatalf("candidates = %#v", candidates)
	}
}

func TestSendCommandMessagePersistsCancelledSuggestion(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	registry := &commandTestRegistry{root: root, manager: manager}
	service := Service{Registry: registry}

	created, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeCommand,
		Name: "Command",
	})
	if err != nil {
		t.Fatalf("create command session: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var sawStart, sawCancelled bool
	err = service.SendMessage(ctx, SendMessageInput{
		RootID:  root.ID,
		Key:     created.Key,
		Content: "sleep 10",
		OnUpdate: func(event agenttypes.Event) {
			toolCall, ok := event.Data.(agenttypes.ToolCall)
			if !ok || toolCall.Meta["source"] != "userShell" {
				return
			}
			switch toolCall.Meta["phase"] {
			case "start":
				if !sawStart {
					sawStart = true
					cancel()
				}
			case "final":
				if toolCall.Status == "cancelled" {
					sawCancelled = true
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("SendMessage command returned error: %v", err)
	}
	if !sawStart || !sawCancelled {
		t.Fatalf("events sawStart=%v sawCancelled=%v", sawStart, sawCancelled)
	}

	candidates, err := SearchCommandSuggestions(context.Background(), manager, root.ID, "sleep", 10)
	if err != nil {
		t.Fatalf("SearchCommandSuggestions: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Name != "sleep 10" {
		t.Fatalf("candidates = %#v", candidates)
	}
}

func TestSubSessionSyntheticDonePersistsPartialResponse(t *testing.T) {
	ctx := context.Background()
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	child, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "child"})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	runtime := &fakeUsecaseAgentSession{id: "sub-thread"}
	var sawDone bool
	markDone := attachBackgroundSessionUpdates(ctx, subagentSessionInput{
		RootID:      root.ID,
		Agent:       "codex",
		Mode:        "default",
		Effort:      "medium",
		FastService: "off",
		Manager:     manager,
		OnUpdate: func(sessionKey string, update agenttypes.Event) {
			if sessionKey == child.Key && update.Type == agenttypes.EventTypeMessageDone {
				sawDone = true
			}
		},
	}, child, runtime)

	runtime.emit(agenttypes.Event{Type: agenttypes.EventTypeMessageChunk, Data: agenttypes.MessageChunk{Content: "partial response"}})
	markDone()

	loaded, err := manager.Get(ctx, child.Key, 0)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if len(loaded.Exchanges) != 1 {
		t.Fatalf("exchanges = %d, want 1", len(loaded.Exchanges))
	}
	if loaded.Exchanges[0].LegacyContent != "partial response" {
		t.Fatalf("content = %q", loaded.Exchanges[0].LegacyContent)
	}
	if !sawDone {
		t.Fatalf("synthetic done was not emitted")
	}
}

func TestSendCommandMessageUsesLongShellPerSession(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows long-shell behavior is covered by cross-compile checks")
	}
	rootDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(rootDir, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	registry := &commandTestRegistry{root: root, manager: manager}
	service := Service{Registry: registry}

	first, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeCommand,
		Name: "Command A",
	})
	if err != nil {
		t.Fatalf("create first command session: %v", err)
	}
	second, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeCommand,
		Name: "Command B",
	})
	if err != nil {
		t.Fatalf("create second command session: %v", err)
	}

	if _, err := sendCommandAndFinal(t, service, root.ID, first.Key, "cd nested"); err != nil {
		t.Fatalf("cd nested: %v", err)
	}
	firstPWD, err := sendCommandAndFinal(t, service, root.ID, first.Key, "pwd")
	if err != nil {
		t.Fatalf("first pwd: %v", err)
	}
	if !strings.Contains(firstPWD, "nested") {
		t.Fatalf("first session pwd = %q, want nested", firstPWD)
	}
	secondPWD, err := sendCommandAndFinal(t, service, root.ID, second.Key, "pwd")
	if err != nil {
		t.Fatalf("second pwd: %v", err)
	}
	if strings.Contains(secondPWD, "nested") {
		t.Fatalf("second session pwd = %q, should not inherit first session cwd", secondPWD)
	}
}
