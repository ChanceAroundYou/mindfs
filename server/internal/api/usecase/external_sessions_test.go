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
	// S2d-2 起正文归块流，主日志一行的 content 恒空，所以这里按 mindfs_id 从块流读。
	// 这是「成交量」断言：ctx_seq 切片放行了多少条、放行的是哪几条。
	if got := importedBlockTexts(context.Background(), manager, created.Key, latest.Exchanges, 1); len(got) != 1 || got[0] != "new user" {
		t.Fatalf("exchange[1] blocks = %#v, want [new user]", got)
	}
	if got := importedBlockTexts(context.Background(), manager, created.Key, latest.Exchanges, 2); len(got) != 1 || got[0] != "new agent" {
		t.Fatalf("exchange[2] blocks = %#v, want [new agent]", got)
	}
}

// importedBlockTexts 读 seq 那一轮的 text 块（判重回查用的同一个口径，
// 见 recordedExchangeBody）。exchanges 用来把 buf 下标换成真实 seq。
func importedBlockTexts(ctx context.Context, manager *session.Manager, key string, exchanges []session.Exchange, idx int) []string {
	seq := exchanges[idx].Seq
	blocks, err := manager.GetBlocksWindow(ctx, key, map[int]bool{seq: true})
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		if b.Type == session.BlockTypeText {
			out = append(out, b.Text)
		}
	}
	return out
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
			contents = append(contents, ex.Role+":"+ex.LegacyContent)
		}
		t.Fatalf("len(exchanges) = %d, want 5: %v", len(latest.Exchanges), contents)
	}
	// S2d-2 起正文归块流（主日志一行的 content 恒空）。
	if got := importedBlockTexts(context.Background(), manager, created.Key, latest.Exchanges, 4); len(got) != 1 || got[0] != "u3" {
		t.Fatalf("exchange[4] blocks = %#v, want [u3]", got)
	}
}

// 复现 2026-10-09「点同步几乎百分百丢最新几轮」：live-owned 会话的库长度系统性大于
// 导入器条数（实时路径会写导入器不产出的 tool-only 空占位行），agent_ctx_seq 被刷成
// 库长度后，Full 同步按 `imported[ctx_seq:]` 切片恒为空 —— 比库里最新一条更新的
// 尾段永远导不进来。修复：live-owned 的 Full 同步改由导入器时间地板圈定增量，不再叠
// 序号切片。
func TestSyncExternalSessionDeltaFullImportsTailWhenCtxSeqOvershoots(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "Live"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)
	live := []agenttypes.ImportedExchange{
		{Role: "user", Content: "u1", Timestamp: base},
		{Role: "agent", Content: "a1", Timestamp: base.Add(time.Minute)},
		{Role: "user", Content: "u2", Timestamp: base.Add(2 * time.Minute)},
		{Role: "agent", Content: "a2", Timestamp: base.Add(3 * time.Minute)},
	}
	for _, item := range live {
		liveCtx := session.WithExchangeSource(ctx, session.ExchangeSourceLive)
		if err := manager.AddExchangeForAgentAt(liveCtx, created, item.Role, item.Content, "codex", "", "", "", item.Timestamp); err != nil {
			t.Fatal(err)
		}
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	// ctx_seq 被刷成「库长度 + 导入器不产出的占位行数」，超过导入器条数（4 条）
	if err := manager.UpdateAgentState(ctx, current, "codex", 10, "external-1"); err != nil {
		t.Fatal(err)
	}
	importer := &syncDeltaTestImporter{
		exchanges: append(append([]agenttypes.ImportedExchange(nil), live...),
			agenttypes.ImportedExchange{Role: "user", Content: "u3", Timestamp: base.Add(4 * time.Minute)},
			agenttypes.ImportedExchange{Role: "agent", Content: "a3", Timestamp: base.Add(5 * time.Minute)},
		),
	}
	svc := &Service{Registry: &syncDeltaTestRegistry{root: root, manager: manager, importer: importer}}
	externalSessionSyncTimes.Store(externalSyncLockKey(root.ID, created.Key), time.Now().Add(-time.Hour))
	out, err := svc.SyncExternalSessionDelta(ctx, SyncExternalSessionDeltaInput{RootID: root.ID, Key: created.Key, Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.ImportedCount != 2 {
		t.Fatalf("ImportedCount = %d, want 2（ctx_seq=10 超过导入器 6 条时仍要导入尾段）", out.ImportedCount)
	}
	latest, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 6 {
		contents := make([]string, 0, len(latest.Exchanges))
		for _, ex := range latest.Exchanges {
			contents = append(contents, ex.Role+":"+ex.LegacyContent)
		}
		t.Fatalf("len(exchanges) = %d, want 6: %v", len(latest.Exchanges), contents)
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

type listExternalSessionsTestImporter struct {
	items []agenttypes.ExternalSessionSummary
}

func (i *listExternalSessionsTestImporter) AgentName() string { return "claude" }

// HasTranscriptTimestamps 假件按 claude 语义：转录条目自带 timestamp。
func (i *listExternalSessionsTestImporter) HasTranscriptTimestamps() bool { return true }

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
	calls     int
}

func (i *syncDeltaTestImporter) AgentName() string { return "codex" }

// HasTranscriptTimestamps 假件按 codex 语义：转录行带 timestamp，时间地板对它成立。
func (i *syncDeltaTestImporter) HasTranscriptTimestamps() bool { return true }

func (i *syncDeltaTestImporter) ListExternalSessions(context.Context, agenttypes.ListExternalSessionsInput) (agenttypes.ListExternalSessionsResult, error) {
	return agenttypes.ListExternalSessionsResult{}, nil
}

func (i *syncDeltaTestImporter) ImportExternalSession(_ context.Context, in agenttypes.ImportExternalSessionInput) (agenttypes.ImportedExternalSession, error) {
	i.input = in
	i.calls++
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
