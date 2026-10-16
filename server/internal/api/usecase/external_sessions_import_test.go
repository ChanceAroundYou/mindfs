package usecase

import (
	"context"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func TestImportExternalSessionOnlyPersistsSelectedAuxKinds(t *testing.T) {
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	importer := &syncDeltaTestImporter{
		exchanges: []agenttypes.ImportedExchange{
			{Role: "user", Content: "inspect"},
			{
				Role:    "agent",
				Content: "done",
				Aux: []agenttypes.ImportedExchangeAux{{
					Line: 0,
					ToolCall: &agenttypes.ToolCall{
						CallID:  "read-1",
						Title:   "read README",
						Status:  "complete",
						Kind:    agenttypes.ToolKindRead,
						RawType: "tool_use",
						Meta:    map[string]any{"output": "contents"},
					},
				}, {
					Line: 0,
					ToolCall: &agenttypes.ToolCall{
						CallID: "execute-1",
						Title:  "go test ./...",
						Status: "complete",
						Kind:   agenttypes.ToolKindExecute,
					},
				}, {
					Line: 0,
					ToolCall: &agenttypes.ToolCall{
						CallID: "edit-1",
						Title:  "main.go",
						Status: "complete",
						Kind:   agenttypes.ToolKindEdit,
					},
				}, {
					Line: 0,
					ToolCall: &agenttypes.ToolCall{
						CallID: "plan-1",
						Title:  "update plan",
						Status: "complete",
						Kind:   agenttypes.ToolKindThink,
					},
				}, {
					Line: 0,
					ToolCall: &agenttypes.ToolCall{
						CallID: "ask-1",
						Title:  "ask user",
						Status: "complete",
						Kind:   agenttypes.ToolKindAskUser,
					},
				}, {
					Line: 0,
					ToolCall: &agenttypes.ToolCall{
						CallID: "web-1",
						Title:  "DeepSeek Harness ACP",
						Status: "complete",
						Kind:   agenttypes.ToolKindWebSearch,
					},
				}, {
					Line: 0,
					ToolCall: &agenttypes.ToolCall{
						CallID: "other-1",
						Title:  "view_image",
						Status: "complete",
						Kind:   agenttypes.ToolKindOther,
					},
				}},
			},
		},
	}
	svc := &Service{Registry: &syncDeltaTestRegistry{
		root:     root,
		manager:  manager,
		importer: importer,
	}}

	out, err := svc.ImportExternalSession(context.Background(), ImportExternalSessionInput{
		RootID:         root.ID,
		Agent:          "codex",
		AgentSessionID: "external-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	executeCall, err := manager.GetFullToolCall(context.Background(), out.SessionKey, "execute-1")
	if err != nil {
		t.Fatal(err)
	}
	if executeCall.Kind != agenttypes.ToolKindExecute {
		t.Fatalf("execute tool call = %#v", executeCall)
	}
	editCall, err := manager.GetFullToolCall(context.Background(), out.SessionKey, "edit-1")
	if err != nil {
		t.Fatal(err)
	}
	if editCall.Kind != agenttypes.ToolKindEdit {
		t.Fatalf("edit tool call = %#v", editCall)
	}
	planCall, err := manager.GetFullToolCall(context.Background(), out.SessionKey, "plan-1")
	if err != nil {
		t.Fatal(err)
	}
	if planCall.Kind != agenttypes.ToolKindThink {
		t.Fatalf("plan tool call = %#v", planCall)
	}
	askCall, err := manager.GetFullToolCall(context.Background(), out.SessionKey, "ask-1")
	if err != nil {
		t.Fatal(err)
	}
	if askCall.Kind != agenttypes.ToolKindAskUser {
		t.Fatalf("ask-user tool call = %#v", askCall)
	}
	webCall, err := manager.GetFullToolCall(context.Background(), out.SessionKey, "web-1")
	if err != nil {
		t.Fatal(err)
	}
	if webCall.Kind != agenttypes.ToolKindWebSearch {
		t.Fatalf("web tool call = %#v", webCall)
	}
	otherCall, err := manager.GetFullToolCall(context.Background(), out.SessionKey, "other-1")
	if err != nil {
		t.Fatal(err)
	}
	if otherCall.Kind != agenttypes.ToolKindOther {
		t.Fatalf("other tool call = %#v", otherCall)
	}
	if _, err := manager.GetFullToolCall(context.Background(), out.SessionKey, "read-1"); err == nil {
		t.Fatal("read tool call was persisted, want it filtered")
	}
}

func TestImportExternalSessionCreatesNativeSubagentSession(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	importer := &syncDeltaTestImporter{
		exchanges: []agenttypes.ImportedExchange{{Role: "user", Content: "delegate"}},
		subagents: []agenttypes.ImportedSubagentSession{{
			AgentSessionID:   "child-agent-1",
			ParentToolCallID: "spawn-1",
			Title:            "Investigate tests",
			Exchanges: []agenttypes.ImportedExchange{
				{Role: "user", Content: "inspect tests"},
				{Role: "agent", Content: "done"},
			},
		}},
	}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: root, manager: manager, importer: importer}}
	out, err := svc.ImportExternalSession(ctx, ImportExternalSessionInput{
		RootID: root.ID, Agent: "codex", AgentSessionID: "parent-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := manager.FindAgentBindingByAgentSession(ctx, "codex", "child-agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if binding == nil {
		t.Fatal("subagent binding not created")
	}
	child, err := manager.Get(ctx, binding.SessionKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentSessionKey != out.SessionKey || child.ParentToolCallID != "spawn-1" {
		t.Fatalf("subagent parent = (%q, %q), want (%q, spawn-1)", child.ParentSessionKey, child.ParentToolCallID, out.SessionKey)
	}
	if child.Name != "Investigate tests" || len(child.Exchanges) != 2 {
		t.Fatalf("subagent = %#v", child)
	}
}

func TestImportExternalSessionPersistsPlanAux(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	importer := &syncDeltaTestImporter{
		exchanges: []agenttypes.ImportedExchange{{
			Role: "agent",
			Aux: []agenttypes.ImportedExchangeAux{{
				Plan: &agenttypes.PlanUpdate{Content: "# Plan\n\n- Step"},
			}},
		}},
	}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: root, manager: manager, importer: importer}}
	out, err := svc.ImportExternalSession(ctx, ImportExternalSessionInput{
		RootID: root.ID, Agent: "codex", AgentSessionID: "external-plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	aux, err := manager.GetExchangeAux(ctx, out.SessionKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(aux[1]) != 1 || aux[1][0].Plan == nil || aux[1][0].Plan.Content != "# Plan\n\n- Step" {
		t.Fatalf("persisted aux = %#v", aux)
	}
}
