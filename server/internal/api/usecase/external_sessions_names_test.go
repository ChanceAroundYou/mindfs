package usecase

import (
	"context"
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func TestListExternalSessionsUsesPersistentMindFSName(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "claude", Name: "Initial import title"})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(ctx, created, "claude", 0, "external-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Rename(ctx, created.Key, "MindFS display name"); err != nil {
		t.Fatal(err)
	}
	importer := &listExternalSessionsTestImporter{items: []agenttypes.ExternalSessionSummary{{
		Agent:          "claude",
		AgentSessionID: "external-1",
		Cwd:            root.RootPath,
		FirstUserText:  "[REPLY_TIPS]\\nlarge startup prompt",
	}}}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: root, manager: manager, importer: importer}}

	out, err := svc.ListExternalSessions(ctx, ListExternalSessionsInput{RootID: root.ID, Agent: "claude", FilterBound: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 0 {
		t.Fatalf("filter-bound items = %#v, want no currently bound session", out.Items)
	}

	out, err = svc.ListExternalSessions(ctx, ListExternalSessionsInput{RootID: root.ID, Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].Title != "MindFS display name" {
		t.Fatalf("unfiltered item = %#v, want persistent MindFS name", out.Items)
	}

	if err := manager.Delete(ctx, created.Key); err != nil {
		t.Fatal(err)
	}
	out, err = svc.ListExternalSessions(ctx, ListExternalSessionsInput{RootID: root.ID, Agent: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("items = %#v, want one", out.Items)
	}
	if got := out.Items[0].Title; got != "MindFS display name" {
		t.Fatalf("title = %q, want persistent MindFS name", got)
	}
}

// 迁移提示的剥壳是「首行 + 尾行」双锚点，中间整段（含绝对路径）不参与判断。
// 路径改成绝对路径后必须仍能剥掉，否则导入会话的首条 user 气泡会一直挂着这段提示。
func TestStripExternalSessionPrefixRemovesAbsolutePathHint(t *testing.T) {
	hint := buildSwitchReadHint("/abs/root/.mindfs/sessions/1234-abcd.jsonl", 20)
	got := stripExternalSessionPrefix(hint + "真正的问题")
	if got != "真正的问题" {
		t.Fatalf("stripped = %q, want just the user message", got)
	}

	if !strings.Contains(hint, "/abs/root/.mindfs/sessions/1234-abcd.jsonl") {
		t.Fatalf("hint does not carry the absolute path: %q", hint)
	}
	if !strings.HasPrefix(strings.ToLower(hint), "this session was migrated from elsewhere.") {
		t.Fatalf("hint lost its first line, importer would render it as a user bubble: %q", hint)
	}

	// 非迁移提示不得被误剥。
	if plain := stripExternalSessionPrefix("普通问题"); plain != "普通问题" {
		t.Fatalf("plain message = %q, want it untouched", plain)
	}
}
