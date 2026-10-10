package usecase

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// S2d-1：旧数据回填。
//
// S2d 的前提是「块流是唯一正文载体」，但本机实测：**320 个会话、2166 行、0 个块流文件**
// （2026-10-11；S2a 已提交但从未部署）。所以直接删 `Content` 等于把全部历史正文清空。
//
// 回填的口径刻意是**读时惰性 + 幂等**，不写迁移脚本：
//
//   · 一写迁移就意味着「必须有人记得跑」，忘了就静默丢数据 —— 而会话库按项目/账户
//     分区散在各处（本机就有 4 个 `.mindfs/sessions`），没有任何一处能保证跑全；
//   · 回填的输入（`Content` + aux）在读的时候本来就在手上，不额外读盘。
//
// 它还顺手救回一类**已经永久丢内容**的行：实测 300 行 agent 正文为空（那个 bug
// 已落盘的形态），但它们的 aux 里还留着 thought —— 正是「正文没了、思考还在」。

func blocksFileOf(root fs.RootInfo, key string) string {
	return filepath.Join(root.MetaDir(), "sessions", key+".blocks.jsonl")
}

// thinkAux 用的是**真实存量形状**：实测 10826 条 aux 带 `thought` 字符串字段。
// 换句话说这就是那 300 行「正文已丢、思考还在」的轮次在盘上的样子。
func thinkAux(seq int, text string) session.ExchangeAux {
	return session.ExchangeAux{Seq: seq, Line: 1, Thought: text}
}

// backfillWindow 模拟读路径的调用方式：exchanges 与 aux 都是调用方**已经**有的数据，
// 不额外读盘 —— 这正是 backfillTurnsFromWindow 零额外 IO 的契约。
func backfillWindow(ctx context.Context, manager *session.Manager, key string) (int, error) {
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		return 0, err
	}
	seqs := make(map[int]bool, len(current.Exchanges))
	for _, ex := range current.Exchanges {
		if ex.Seq > 0 {
			seqs[ex.Seq] = true
		}
	}
	aux, err := manager.GetExchangeAuxWindow(ctx, key, seqs)
	if err != nil {
		return 0, err
	}
	return backfillTurnsFromWindow(ctx, manager, key, current.Exchanges, aux, seqs)
}

// legacySession 造一个 S2a 之前形态的会话：只有主日志 + aux，没有任何块。
func legacySession(t *testing.T) (*session.Manager, *session.Session, fs.RootInfo) {
	t.Helper()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(context.Background(), session.CreateInput{
		Type: session.TypeChat, Agent: "dsh", Name: "Legacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC)
	// 老写法：正文进主日志，块流不写（这正是 S2a 之前的每一轮）。
	if err := manager.AddExchangeForAgentAt(context.Background(), created, "user", "请继续", "dsh", "", "", "", ts); err != nil {
		t.Fatal(err)
	}
	if err := manager.AddExchangeForAgentAt(context.Background(), created, "agent", "正文第一段", "dsh", "", "", "", ts.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	return manager, current, root
}

// 回填后：老轮次有了块，且块的正文与老主日志**逐字相同**。
func TestBackfillCompilesLegacyExchangesIntoBlocks(t *testing.T) {
	ctx := context.Background()
	manager, current, _ := legacySession(t)
	key := current.Key

	if blocks, err := manager.ReadBlocks(ctx, key); err != nil || len(blocks) != 0 {
		t.Fatalf("前置条件：块流应为空，blocks=%v err=%v", blocks, err)
	}
	n, err := backfillWindow(ctx, manager, key)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("backfilled = %d, want 2（user 行 + agent 行）", n)
	}
	blocks, err := manager.ReadBlocks(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	bySeq := session.GroupBlocksBySeq(blocks)
	if got := bySeq[1]; len(got) != 1 || got[0].Text != "请继续" {
		t.Fatalf("user 块 = %+v", got)
	}
	if got := bySeq[2]; len(got) != 1 || got[0].Text != "正文第一段" {
		t.Fatalf("agent 块 = %+v", got)
	}
}

// 幂等：跑两次不会写出第二份。重复块会让同一个 seq 渲染出两段正文。
func TestBackfillIsIdempotent(t *testing.T) {
	ctx := context.Background()
	manager, current, root := legacySession(t)
	key := current.Key
	if _, err := backfillWindow(ctx, manager, key); err != nil {
		t.Fatal(err)
	}
	path := blocksFileOf(root, key)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := backfillWindow(ctx, manager, key)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("第二次回填 = %d, want 0", n)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() != after.Size() {
		t.Fatalf("块流被重写了：%d → %d 字节", before.Size(), after.Size())
	}
	blocks, err := manager.ReadBlocks(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2（重复块会让同一 seq 渲染出两段正文）", len(blocks))
	}
}

// 核心价值：正文为空、只有 aux 里 reasoning 的轮次也要被救回来。
// 实测这类行有 300 条 —— 正文在落盘时就丢了（那个 bug），但思考还在 aux 里。
// 不回填，它们永远是空气泡。
func TestBackfillRecoversReasoningOnlyTurns(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "dsh", Name: "Legacy"})
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC)
	if err := manager.AddExchangeForAgentAt(ctx, created, "user", "问题", "dsh", "", "", "", ts); err != nil {
		t.Fatal(err)
	}
	// 正文为空 —— os 模型只产出 reasoning 时就是这个形态。
	if err := manager.AddExchangeForAgentAt(ctx, created, "agent", "", "dsh", "", "", "", ts.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := manager.AddExchangeAux(ctx, created.Key, thinkAux(2, "先想想再回答")); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backfillWindow(ctx, manager, current.Key); err != nil {
		t.Fatal(err)
	}
	blocks, err := manager.ReadBlocks(ctx, current.Key)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, b := range blocks {
		if b.Seq == 2 && b.Type == session.BlockTypeReasoning && b.Text != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("正文为空的轮次没有被回填出 reasoning 块: %+v", blocks)
	}
}

// 已经有块的轮次**一个字节都不动**。S2a 之后写入的数据是权威的，
// 拿老正文覆盖它就是数据倒退。
func TestBackfillLeavesBackfilledTurnsAlone(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "dsh", Name: "Mixed"})
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 10, 7, 6, 0, 0, 0, time.UTC)
	if err := manager.AddExchangeForAgentAt(ctx, created, "user", "老问题", "dsh", "", "", "", ts); err != nil {
		t.Fatal(err)
	}
	// 这一轮 S2a 之后写的：块流里有权威正文，主日志里也还留着 content。
	if err := manager.AddExchangeForAgentAt(ctx, created, "agent", "新正文", "dsh", "", "", "", ts.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := manager.AppendBlocks(ctx, created.Key, session.BuildTurnBlocks(2, 1, "", "新正文", nil)); err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	n, err := backfillWindow(ctx, manager, current.Key)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("backfilled = %d, want 1（只回填没有块的 seq=1）", n)
	}
	blocks, err := manager.ReadBlocks(ctx, current.Key)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		if b.Seq == 2 && b.Text != "新正文" {
			t.Fatalf("已有块的轮次被改写了: %+v", b)
		}
	}
}

// 一个都没有可编译的（正文与 aux 全空）就不写文件，避免制造空块流。
func TestBackfillSkipsSessionsWithNothingToCompile(t *testing.T) {
	ctx := context.Background()
	root := fs.NewRootInfo("root", "Root", t.TempDir())
	manager := session.NewManager(root)
	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "dsh", Name: "Empty"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatal(err)
	}
	n, err := backfillWindow(ctx, manager, current.Key)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("backfilled = %d, want 0", n)
	}
	if _, err := os.Stat(blocksFileOf(root, current.Key)); !os.IsNotExist(err) {
		t.Fatalf("不该创建空块流文件，stat err = %v", err)
	}
}

// 回填必须与「已经有块」的判定用**同一个**口径 —— 两处各写一份判据，
// 就会出现「回填说没有、读路径说有」的空窗（正文忽然变空气泡，且只在老会话复现）。
func TestBackfillAndReadAgreeOnWhatNeedsBackfill(t *testing.T) {
	ctx := context.Background()
	manager, current, _ := legacySession(t)
	key := current.Key
	seqs := map[int]bool{1: true, 2: true}
	if missing := missingBlockSeqs(nil, seqs); len(missing) != 2 {
		t.Fatalf("missingBlockSeqs = %v, want 2 个", missing)
	}
	if _, err := backfillWindow(ctx, manager, key); err != nil {
		t.Fatal(err)
	}
	blocks, err := manager.GetBlocksWindow(ctx, key, seqs)
	if err != nil {
		t.Fatal(err)
	}
	if missing := missingBlockSeqs(blocks, seqs); len(missing) != 0 {
		t.Fatalf("回填后 missingBlockSeqs = %v, want 空", missing)
	}
}

// seq=0 不参与回填：那是「还没落盘的占位行」，没有正文可编译。
func TestBackfillIgnoresZeroSeq(t *testing.T) {
	missing := missingBlockSeqs(nil, map[int]bool{0: true, 3: true})
	if len(missing) != 1 || missing[0] != 3 {
		t.Fatalf("missingBlockSeqs = %v, want [3]", missing)
	}
}
