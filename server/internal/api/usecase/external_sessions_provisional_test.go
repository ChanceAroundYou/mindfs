package usecase

import (
	"context"
	"strings"
	"testing"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

// 真 kill 探针的现场：转录尾停在 assistant(tool_use)，没有助手正文；MindFS 库里
// 只有用户行，但流式路径已经把用户看到的文字写成了 provisional block。重启同步
// 必须把这些文字封口成助手行，而不是去 importer 里捞一个还没有最终回复的开放尾。
func TestSyncExternalSessionRecoversProvisionalBlocksWithoutImporter(t *testing.T) {
	root, manager := newLiveTailTestManager(t)
	ctx := context.Background()
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "codex", Name: "Provisional"})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 11, 22, 8, 0, 0, 0, time.UTC)
	const turnID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	liveCtx := session.WithExchangeSource(ctx, session.ExchangeSourceLive)
	userCtx := session.WithExchangeIdentity(liveCtx, session.ExchangeIdentity{MindFSID: turnID, TurnSeq: 1})
	if err := manager.AddExchangeForAgentAt(userCtx, created, "user", "请先说一句再调用工具", "codex", "", "", "", stamp); err != nil {
		t.Fatal(err)
	}
	if err := manager.AppendProvisionalText(ctx, created.Key, 2, turnID, 0, "收到，我先说明一下。"); err != nil {
		t.Fatal(err)
	}
	if err := manager.AppendProvisionalToolCall(ctx, created.Key, 2, turnID, 1, agenttypes.ToolCall{
		CallID: "call-live", Title: "Bash", Status: "running", Kind: agenttypes.ToolKindExecute,
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.AppendProvisionalText(ctx, created.Key, 2, turnID, 2, "工具还没返回。"); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(ctx, current, "codex", 1, "thread-provisional"); err != nil {
		t.Fatal(err)
	}

	importer := interruptedTailImporter(stamp)
	svc := &Service{Registry: &syncDeltaTestRegistry{root: *root, manager: manager, importer: importer}}

	// delta：provisional 恢复自己就能收口，不能再去 importer 捞开放尾。
	runLiveTailSync(t, svc, root.ID, created.Key)
	if importer.calls != 0 {
		t.Fatalf("provisional 已恢复，importer 不该被调用，实际 %d 次", importer.calls)
	}

	latest, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 2 {
		t.Fatalf("恢复后应只有用户行+助手行，实际 %d 行", len(latest.Exchanges))
	}
	agent := latest.Exchanges[1]
	if agent.Role != "agent" || !agent.Partial || agent.MindFSID != turnID {
		t.Fatalf("助手行应是同 turnID 的 partial 行，实际 %#v", agent)
	}
	if text := blocksText(blocksForSeq(t, manager, created.Key, 2)); !strings.Contains(text, "收到，我先说明一下。") || !strings.Contains(text, "工具还没返回。") {
		t.Fatalf("provisional 文本未恢复: %q", text)
	}
	blocks := blocksForSeq(t, manager, created.Key, 2)
	interrupted := 0
	for _, block := range blocks {
		if block.Type == session.BlockTypeToolCall && block.ToolCall != nil && block.ToolCall.Status == "interrupted" {
			interrupted++
		}
	}
	if interrupted != 1 {
		t.Fatalf("未完成工具调用应标 interrupted，实际 %d", interrupted)
	}

	// Full：通用导入的 floor 必须用恢复后的最新行重算，不能停在旧用户行时刻。
	// 假 importer 不自己过滤 floor，这里直接检查传给它的地板，并把可导入内容清空。
	importer.exchanges = nil
	if _, err := svc.SyncExternalSessionDelta(ctx, SyncExternalSessionDeltaInput{RootID: root.ID, Key: created.Key, Full: true}); err != nil {
		t.Fatal(err)
	}
	if !importer.input.TimestampFloor.After(stamp.Add(40 * time.Second)) {
		t.Fatalf("Full 的 floor 没按恢复后的行重算: %v", importer.input.TimestampFloor)
	}
	latest, err = manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Exchanges) != 2 {
		t.Fatalf("Full 后不应重复导入同一轮，实际 %d 行", len(latest.Exchanges))
	}
}
