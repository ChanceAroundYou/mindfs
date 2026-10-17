package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
)

func TestManagerGetFullToolCallReadsPendingAuxBeforeDisk(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	callID := "call-pending"
	if err := manager.UpsertPendingExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 1,
		ToolCall: &agenttypes.ToolCall{
			CallID:  callID,
			Title:   "git diff",
			Status:  "running",
			Kind:    agenttypes.ToolKindExecute,
			Content: []agenttypes.ToolCallContentItem{{Type: "text", Text: "running output"}},
		},
	}); err != nil {
		t.Fatalf("upsert pending start: %v", err)
	}
	if err := manager.UpsertPendingExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 1,
		ToolCall: &agenttypes.ToolCall{
			CallID:  callID,
			Status:  "complete",
			Content: []agenttypes.ToolCallContentItem{{Type: "text", Text: "final diff output"}},
			Meta:    map[string]any{"outputBytes": 17},
		},
	}); err != nil {
		t.Fatalf("upsert pending final: %v", err)
	}

	toolCall, err := manager.GetFullToolCall(context.Background(), created.Key, callID)
	if err != nil {
		t.Fatalf("get pending full toolcall: %v", err)
	}
	if toolCall.Status != "complete" {
		t.Fatalf("status = %q, want complete", toolCall.Status)
	}
	if toolCall.Title != "git diff" {
		t.Fatalf("title = %q, want git diff", toolCall.Title)
	}
	if len(toolCall.Content) != 1 || toolCall.Content[0].Text != "final diff output" {
		t.Fatalf("content = %#v, want final diff output", toolCall.Content)
	}

	manager.ClearPendingExchangeAux(context.Background(), created.Key)
	if _, err := manager.GetFullToolCall(context.Background(), created.Key, callID); err == nil {
		t.Fatal("GetFullToolCall after clear returned nil error, want not found")
	}
}

func TestManagerMarkPendingAskUserAnsweredMergesAnswers(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	callID := "ask-1"
	questions := []agenttypes.AskUserQuestionItem{{Question: "Pick one"}}
	if err := manager.UpsertPendingExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 0,
		ToolCall: &agenttypes.ToolCall{
			CallID: callID,
			Title:  "ask user",
			Status: "running",
			Kind:   agenttypes.ToolKindAskUser,
			Meta: map[string]any{
				"toolUseId": callID,
				"questions": questions,
			},
		},
	}); err != nil {
		t.Fatalf("upsert pending ask user: %v", err)
	}

	answeredAt := time.Date(2026, 6, 22, 1, 2, 3, 0, time.UTC)
	if err := manager.MarkPendingAskUserAnswered(context.Background(), created.Key, callID, map[string]string{
		"q_0": "Yes",
	}, answeredAt); err != nil {
		t.Fatalf("mark answered: %v", err)
	}

	toolCall, err := manager.GetFullToolCall(context.Background(), created.Key, callID)
	if err != nil {
		t.Fatalf("get full toolcall: %v", err)
	}
	if toolCall.Status != "complete" {
		t.Fatalf("status = %q, want complete", toolCall.Status)
	}
	if toolCall.Meta["questions"] == nil {
		t.Fatalf("questions were not preserved: %#v", toolCall.Meta)
	}
	answers, ok := toolCall.Meta["answers"].(map[string]string)
	if !ok || answers["q_0"] != "Yes" {
		t.Fatalf("answers = %#v, want q_0=Yes", toolCall.Meta["answers"])
	}
	if toolCall.Meta["answeredAt"] != answeredAt.Format(time.RFC3339Nano) {
		t.Fatalf("answeredAt = %#v, want %s", toolCall.Meta["answeredAt"], answeredAt.Format(time.RFC3339Nano))
	}
}

func TestManagerUnmarkPendingAskUserAnsweredRestoresAnswerable(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type: TypeChat,
		Name: "Chat",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	callID := "ask-unmark"
	questions := []agenttypes.AskUserQuestionItem{{Question: "Pick one"}}
	if err := manager.UpsertPendingExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 0,
		ToolCall: &agenttypes.ToolCall{
			CallID: callID,
			Title:  "ask user",
			Status: "running",
			Kind:   agenttypes.ToolKindAskUser,
			Meta: map[string]any{
				"toolUseId": callID,
				"questions": questions,
			},
		},
	}); err != nil {
		t.Fatalf("upsert pending ask user: %v", err)
	}

	answeredAt := time.Date(2026, 6, 22, 1, 2, 3, 0, time.UTC)
	if err := manager.MarkPendingAskUserAnswered(context.Background(), created.Key, callID, map[string]string{
		"q_0": "Yes",
	}, answeredAt); err != nil {
		t.Fatalf("mark answered: %v", err)
	}
	// 投递失败：答案没送出去，标记必须撤回，卡片回到「可回答」。
	manager.UnmarkPendingAskUserAnswered(context.Background(), created.Key, callID, answeredAt)

	toolCall, err := manager.GetFullToolCall(context.Background(), created.Key, callID)
	if err != nil {
		t.Fatalf("get full toolcall: %v", err)
	}
	if toolCall.Status != "running" {
		t.Fatalf("status = %q, want running（卡片要重新可回答）", toolCall.Status)
	}
	if _, ok := toolCall.Meta["answers"]; ok {
		t.Fatalf("answers 仍留在 meta 里：%#v", toolCall.Meta)
	}
	if _, ok := toolCall.Meta["answeredAt"]; ok {
		t.Fatalf("answeredAt 仍留在 meta 里：%#v", toolCall.Meta)
	}
	if toolCall.Meta["questions"] == nil {
		t.Fatalf("题目被一起抹掉了，卡片渲染不出来：%#v", toolCall.Meta)
	}
}

// 时间戳对不上说明 agent 的 tool_result 已经把它推进过，撤回去是倒退。
func TestManagerUnmarkPendingAskUserAnsweredIgnoresStaleStamp(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Chat"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	callID := "ask-stale"
	if err := manager.UpsertPendingExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 0,
		ToolCall: &agenttypes.ToolCall{
			CallID: callID,
			Status: "running",
			Kind:   agenttypes.ToolKindAskUser,
		},
	}); err != nil {
		t.Fatalf("upsert pending ask user: %v", err)
	}

	answeredAt := time.Date(2026, 6, 22, 1, 2, 3, 0, time.UTC)
	if err := manager.MarkPendingAskUserAnswered(context.Background(), created.Key, callID, map[string]string{
		"q_0": "Yes",
	}, answeredAt); err != nil {
		t.Fatalf("mark answered: %v", err)
	}
	manager.UnmarkPendingAskUserAnswered(context.Background(), created.Key, callID, answeredAt.Add(time.Second))

	toolCall, err := manager.GetFullToolCall(context.Background(), created.Key, callID)
	if err != nil {
		t.Fatalf("get full toolcall: %v", err)
	}
	if toolCall.Status != "complete" {
		t.Fatalf("status = %q, want complete（时间戳不符就不该撤）", toolCall.Status)
	}
	if _, ok := toolCall.Meta["answers"]; !ok {
		t.Fatalf("answers 被误撤：%#v", toolCall.Meta)
	}
}

// 只有 ask_user 的条目才归这个标记管，别的工具卡不能被它翻回 running。
func TestManagerUnmarkPendingAskUserAnsweredIgnoresOtherKinds(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Chat"})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	callID := "exec-1"
	answeredAt := time.Date(2026, 6, 22, 1, 2, 3, 0, time.UTC)
	if err := manager.UpsertPendingExchangeAux(context.Background(), created.Key, ExchangeAux{
		Seq:  2,
		Line: 0,
		ToolCall: &agenttypes.ToolCall{
			CallID: callID,
			Status: "complete",
			Kind:   agenttypes.ToolKindExecute,
			Meta:   map[string]any{"answeredAt": answeredAt.Format(time.RFC3339Nano)},
		},
	}); err != nil {
		t.Fatalf("upsert pending exec: %v", err)
	}

	manager.UnmarkPendingAskUserAnswered(context.Background(), created.Key, callID, answeredAt)

	toolCall, err := manager.GetFullToolCall(context.Background(), created.Key, callID)
	if err != nil {
		t.Fatalf("get full toolcall: %v", err)
	}
	if toolCall.Status != "complete" {
		t.Fatalf("status = %q, want complete（非 ask_user 不该被撤）", toolCall.Status)
	}
}

func TestExchangeSourceComesFromContext(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Source"})
	if err != nil {
		t.Fatal(err)
	}
	// 无 ctx 标注 = 历史数据，Source 必须为空。
	if err := manager.AddExchangeForAgent(context.Background(), created, "user", "legacy", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	liveCtx := WithExchangeSource(context.Background(), ExchangeSourceLive)
	if err := manager.AddExchangeForAgent(liveCtx, created, "agent", "live", "", "", "", ""); err != nil {
		t.Fatal(err)
	}
	importCtx := WithExchangeSource(context.Background(), ExchangeSourceImport)
	if err := manager.AddExchangeForAgentAt(importCtx, created, "user", "imported", "", "", "", "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	payload, err := os.ReadFile(filepath.Join(root.MetaDir(), "sessions", created.Key+".jsonl"))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(payload)), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}
	wantSources := []string{"", ExchangeSourceLive, ExchangeSourceImport}
	for index, line := range lines {
		var entry Exchange
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("unmarshal line %d: %v", index, err)
		}
		if entry.Source != wantSources[index] {
			t.Fatalf("line %d source = %q, want %q", index, entry.Source, wantSources[index])
		}
	}
}
