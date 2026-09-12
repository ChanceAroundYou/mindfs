package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"mindfs/server/internal/agent"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/session"
)

func TestExternalSessionDeltaAfterCtxSeqSkipsCopiedPrefix(t *testing.T) {
	exchanges := []agenttypes.ImportedExchange{
		{Role: "user", Content: "u1"},
		{Role: "agent", Content: "a1"},
		{Role: "user", Content: "u2"},
		{Role: "agent", Content: "a2"},
	}
	delta := externalSessionDeltaAfterCtxSeq(exchanges, 2)
	if len(delta) != 2 {
		t.Fatalf("len(delta) = %d, want 2", len(delta))
	}
	if delta[0].Content != "u2" || delta[1].Content != "a2" {
		t.Fatalf("delta = %#v", delta)
	}
}

func TestExternalSessionDeltaAfterCtxSeqReturnsEmptyWhenFullySynced(t *testing.T) {
	exchanges := []agenttypes.ImportedExchange{
		{Role: "user", Content: "u1"},
		{Role: "agent", Content: "a1"},
	}
	if delta := externalSessionDeltaAfterCtxSeq(exchanges, 2); len(delta) != 0 {
		t.Fatalf("delta = %#v, want empty", delta)
	}
}

func TestSyncExternalSessionDeltaFastDoesNotApplyCtxSeqToFilteredImport(t *testing.T) {
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(context.Background(), session.CreateInput{
		Type:  session.TypeChat,
		Agent: "codex",
		Name:  "Imported",
	})
	if err != nil {
		t.Fatal(err)
	}
	lastTimestamp := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	if err := manager.AddExchangeForAgentAt(context.Background(), created, "user", "old", "codex", "", "", "", lastTimestamp); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(context.Background(), current, "codex", 10, "external-1"); err != nil {
		t.Fatal(err)
	}
	importer := &syncDeltaTestImporter{
		exchanges: []agenttypes.ImportedExchange{
			{Role: "user", Content: "new user", Timestamp: lastTimestamp.Add(time.Minute)},
			{Role: "agent", Content: "new agent", Timestamp: lastTimestamp.Add(2 * time.Minute)},
		},
	}
	svc := &Service{Registry: &syncDeltaTestRegistry{
		root:     root,
		manager:  manager,
		importer: importer,
	}}
	out, err := svc.SyncExternalSessionDelta(context.Background(), SyncExternalSessionDeltaInput{
		RootID: root.ID,
		Key:    created.Key,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.ImportedCount != 2 {
		t.Fatalf("ImportedCount = %d, want 2", out.ImportedCount)
	}
	if importer.input.AfterTimestamp.IsZero() {
		t.Fatal("AfterTimestamp is zero, want fast sync to pass last timestamp")
	}
	latest, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 3 {
		t.Fatalf("len(exchanges) = %d, want 3", len(latest.Exchanges))
	}
	if got := latest.Exchanges[1].Content; got != "new user" {
		t.Fatalf("exchange[1] = %q, want new user", got)
	}
	if got := latest.Exchanges[2].Content; got != "new agent" {
		t.Fatalf("exchange[2] = %q, want new agent", got)
	}
}

// 复现 2026-09-12 WSL 端「点同步后整段重放」：live 路径写入的轮次已被增量同步以
// ImportedCount=0 跳过（ts 相同被 after 过滤），导致 agent_ctx_seq 停在旧值；
// 随后一次 Full 同步按 ctx_seq 切片，把库内已有的历史尾段整块重放（时间戳是历史
// 原值，还有 aux 缺失签名）。修复方向：入库幂等护栏 + ctx_seq 恒刷新。
func TestSyncExternalSessionDeltaFullDoesNotReplayHistoryInSession(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(context.Background(), session.CreateInput{
		Type:  session.TypeChat,
		Agent: "codex",
		Name:  "Imported",
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	live := []agenttypes.ImportedExchange{
		{Role: "user", Content: "u1", Timestamp: base},
		{Role: "agent", Content: "a1", Timestamp: base.Add(time.Minute)},
		{Role: "user", Content: "u2", Timestamp: base.Add(2 * time.Minute)},
		{Role: "agent", Content: "a2", Timestamp: base.Add(3 * time.Minute)},
	}
	for _, item := range live {
		if err := manager.AddExchangeForAgentAt(ctx, created, item.Role, item.Content, "codex", "", "", "", item.Timestamp); err != nil {
			t.Fatal(err)
		}
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	// ctx_seq 停在 2：live 尾段（u2/a2）写入后没有再发生过 ImportedCount>0 的同步
	if err := manager.UpdateAgentState(ctx, current, "codex", 2, "external-1"); err != nil {
		t.Fatal(err)
	}
	importer := &syncDeltaTestImporter{
		// Full 全量重读：历史 4 条 + 一条真正的新消息
		exchanges: append(append([]agenttypes.ImportedExchange(nil), live...),
			agenttypes.ImportedExchange{Role: "user", Content: "u3", Timestamp: base.Add(5 * time.Minute)}),
	}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: root, manager: manager, importer: importer}}
	out, err := svc.SyncExternalSessionDelta(ctx, SyncExternalSessionDeltaInput{
		RootID: root.ID,
		Key:    created.Key,
		Full:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.ImportedCount != 1 {
		t.Fatalf("ImportedCount = %d, want 1 (历史尾段不得重放)", out.ImportedCount)
	}
	latest, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 5 {
		contents := make([]string, 0, len(latest.Exchanges))
		for _, ex := range latest.Exchanges {
			contents = append(contents, ex.Role+":"+ex.Content)
		}
		t.Fatalf("len(exchanges) = %d, want 5: %v", len(latest.Exchanges), contents)
	}
	if got := latest.Exchanges[4].Content; got != "u3" {
		t.Fatalf("exchange[4] = %q, want u3", got)
	}
}

// 正常增量同步即便 ImportedCount=0（全部被 after 过滤）也必须把 agent_ctx_seq
// 刷新到当前库长度，防止下次 Full 同步按陈旧游标重放尾段。顺带保证用户过后真实
// 重发同内容消息（时间戳远晚于库内已有）不会被幂等护栏误伤。
func TestSyncExternalSessionDeltaIncrementalRefreshesCtxSeq(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(context.Background(), session.CreateInput{
		Type:  session.TypeChat,
		Agent: "codex",
		Name:  "Imported",
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	if err := manager.AddExchangeForAgentAt(ctx, created, "user", "u1", "codex", "", "", "", base); err != nil {
		t.Fatal(err)
	}
	if err := manager.AddExchangeForAgentAt(ctx, created, "agent", "a1", "codex", "", "", "", base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := manager.AddExchangeForAgentAt(ctx, created, "user", "ok", "codex", "", "", "", base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(ctx, current, "codex", 1, "external-1"); err != nil {
		t.Fatal(err)
	}
	// 增量同步无可导入内容（live 写入已在库内，被 after 过滤吞掉 → count=0）
	importer := &syncDeltaTestImporter{exchanges: nil}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: root, manager: manager, importer: importer}}
	if _, err := svc.SyncExternalSessionDelta(ctx, SyncExternalSessionDeltaInput{
		RootID: root.ID,
		Key:    created.Key,
	}); err != nil {
		t.Fatal(err)
	}
	binding, err := manager.FindAgentBinding(ctx, created.Key, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if binding == nil || binding.AgentCtxSeq != 3 {
		t.Fatalf("binding.AgentCtxSeq = %+v, want 3 (count=0 也要刷游标)", binding)
	}

	// 反向底线：隔一小时真实重发同内容 "ok"，必须照常入库，不得被幂等护栏吞掉
	// （先清 2s 节流表，否则背靠背的第二次同步被节流直接跳过）
	externalSessionSyncTimes.Delete(externalSyncLockKey(root.ID, created.Key))
	importer.exchanges = []agenttypes.ImportedExchange{
		{Role: "user", Content: "ok", Timestamp: base.Add(time.Hour)},
	}
	if _, err := svc.SyncExternalSessionDelta(ctx, SyncExternalSessionDeltaInput{
		RootID: root.ID,
		Key:    created.Key,
	}); err != nil {
		t.Fatal(err)
	}
	latest, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 4 {
		t.Fatalf("len(exchanges) = %d, want 4 (重发的 ok 不得被吞)", len(latest.Exchanges))
	}
}

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

type listExternalSessionsTestImporter struct {
	items []agenttypes.ExternalSessionSummary
}

func (i *listExternalSessionsTestImporter) AgentName() string { return "claude" }

func (i *listExternalSessionsTestImporter) ListExternalSessions(context.Context, agenttypes.ListExternalSessionsInput) (agenttypes.ListExternalSessionsResult, error) {
	return agenttypes.ListExternalSessionsResult{Items: i.items}, nil
}

func (i *listExternalSessionsTestImporter) ImportExternalSession(context.Context, agenttypes.ImportExternalSessionInput) (agenttypes.ImportedExternalSession, error) {
	return agenttypes.ImportedExternalSession{}, errors.New("not implemented")
}

type syncDeltaTestImporter struct {
	input     agenttypes.ImportExternalSessionInput
	exchanges []agenttypes.ImportedExchange
	subagents []agenttypes.ImportedSubagentSession
}

func (i *syncDeltaTestImporter) AgentName() string { return "codex" }

func (i *syncDeltaTestImporter) ListExternalSessions(context.Context, agenttypes.ListExternalSessionsInput) (agenttypes.ListExternalSessionsResult, error) {
	return agenttypes.ListExternalSessionsResult{}, nil
}

func (i *syncDeltaTestImporter) ImportExternalSession(_ context.Context, in agenttypes.ImportExternalSessionInput) (agenttypes.ImportedExternalSession, error) {
	i.input = in
	return agenttypes.ImportedExternalSession{
		Agent:          i.AgentName(),
		AgentSessionID: in.AgentSessionID,
		Cwd:            in.RootPath,
		Exchanges:      i.exchanges,
		Subagents:      i.subagents,
	}, nil
}

type syncDeltaTestRegistry struct {
	root     fs.RootInfo
	manager  *session.Manager
	importer agenttypes.ExternalSessionImporter
}

func (r *syncDeltaTestRegistry) GetRoot(rootID string) (fs.RootInfo, error) {
	if rootID != r.root.ID {
		return fs.RootInfo{}, errors.New("root not found")
	}
	return r.root, nil
}

func (r *syncDeltaTestRegistry) GetSessionManager(string) (*session.Manager, error) {
	return r.manager, nil
}

func (r *syncDeltaTestRegistry) UpsertRoot(string) (fs.RootInfo, error) {
	return fs.RootInfo{}, errors.New("not implemented")
}

func (r *syncDeltaTestRegistry) RemoveRoot(string) (fs.RootInfo, error) {
	return fs.RootInfo{}, errors.New("not implemented")
}

func (r *syncDeltaTestRegistry) RenameRoot(string, string, string) (fs.RootInfo, error) {
	return fs.RootInfo{}, errors.New("not implemented")
}

func (r *syncDeltaTestRegistry) UpdateDisplayName(string, string) (fs.RootInfo, error) {
	return fs.RootInfo{}, errors.New("not implemented")
}

func (r *syncDeltaTestRegistry) ListRoots() []fs.RootInfo { return nil }

func (r *syncDeltaTestRegistry) GetAgentPool() *agent.Pool { return nil }

func (r *syncDeltaTestRegistry) GetPreferences() *preferences.Store { return nil }

func (r *syncDeltaTestRegistry) GetExternalSessionImporter(string) (agenttypes.ExternalSessionImporter, error) {
	return r.importer, nil
}

func (r *syncDeltaTestRegistry) GetProber() *agent.Prober { return nil }

func (r *syncDeltaTestRegistry) GetCandidateRegistry() *CandidateRegistry { return nil }

func (r *syncDeltaTestRegistry) GetFileWatcher(string, *session.Manager) (*fs.SharedFileWatcher, error) {
	return nil, nil
}

func (r *syncDeltaTestRegistry) ReleaseFileWatcher(string, string) {}
