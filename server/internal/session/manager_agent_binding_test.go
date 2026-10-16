package session

import (
	"context"
	"testing"

	rootfs "mindfs/server/internal/fs"
)

// 徽标的 agent 来源是列表路径的 AgentCtxSeq 单键推导，而它靠 session_agent_bindings 灌入。
// 绑定只在回合结束时写 → 首轮运行期间列表行 agent=="" → 前端 AgentIcon 落回「AI」占位。
// 这一组钉住「建会话的那一刻起，列表就能报出 agent」。
func TestManagerCreateBindsAgentForListInference(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Agent: "claude", Name: "Fresh"})
	if err != nil {
		t.Fatal(err)
	}

	// 必须走列表路径：它从库里重读 meta 再用绑定回填 AgentCtxSeq，
	// 用 Create 的返回值断言会拿到内存里那个已经带 AgentCtxSeq 的对象，等于没测。
	items, err := manager.List(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var listed *Session
	for _, item := range items {
		if item.Key == created.Key {
			listed = item
			break
		}
	}
	if listed == nil {
		t.Fatalf("created session %s missing from list", created.Key)
	}
	if got := InferAgentFromSession(listed); got != "claude" {
		t.Fatalf("列表行推断 agent = %q, want %q（首轮未结束时徽标必须已是 Claude，而不是 AI 占位）", got, "claude")
	}
}

// 中途换过 agent 的会话有两个绑定行，列表路径必须报出**最近在用**的那个。
//
// 2026-10-07 实测：工作台上「大多数用 dsh 的任务没有 agent 徽标」。dsh 恰恰都是在已有
// claude 会话里中途切过去的，必然双绑定 —— 旧的 `len(AgentCtxSeq) == 1` 回退对它们一律
// 返回空串，徽标落回 AgentIcon 的「AI」文字占位。
func TestManagerListInferAgentForSwitchedSession(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Agent: "claude", Name: "Switched"})
	if err != nil {
		t.Fatal(err)
	}
	// 先在 claude 上跑一段（ctx_seq 是会话行数游标，40 行）
	if err := manager.UpdateAgentState(ctx, created, "claude", 40, "claude-id"); err != nil {
		t.Fatal(err)
	}
	// 中途切到 dsh：切换路径真正用的是 EnsureAgentBinding（先建 0 的占位行），
	// 本轮跑完再由 UpdateAgentState 把游标推到 44。
	if err := manager.EnsureAgentBinding(ctx, created.Key, "dsh"); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(ctx, created, "dsh", 44, "dsh-id"); err != nil {
		t.Fatal(err)
	}

	// 必须走列表路径：只有它从库里重读 meta + bindings，exchanges 为空 —— 正是出 bug 的那条路。
	items, err := manager.List(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var listed *Session
	for _, item := range items {
		if item.Key == created.Key {
			listed = item
			break
		}
	}
	if listed == nil {
		t.Fatalf("created session %s missing from list", created.Key)
	}
	if len(listed.Exchanges) != 0 {
		t.Fatalf("列表路径不该加载 exchanges，否则测不到 AgentCtxSeq 回退（got %d 条）", len(listed.Exchanges))
	}
	if got := InferAgentFromSession(listed); got != "dsh" {
		t.Fatalf("双绑定会话列表 agent = %q, want %q（最近写入 exchange 的那个 agent）", got, "dsh")
	}
}

// 平局（两个绑定都还是 0，即切换后第一轮还没跑完）不能靠 map 遍历顺序定 ——
// 同一份数据两次调用必须报同一个 agent，否则徽标会在两次列表刷新之间来回跳。
func TestInferAgentFromSessionIsDeterministicOnTie(t *testing.T) {
	s := &Session{AgentCtxSeq: map[string]int{"claude": 0, "dsh": 0}}
	first := InferAgentFromSession(s)
	for i := 0; i < 50; i++ {
		if got := InferAgentFromSession(s); got != first {
			t.Fatalf("平局时推断不稳定：%q vs %q", got, first)
		}
	}
	if first == "" {
		t.Fatal("平局也必须报出一个 agent，不能落回空串（空串就是「AI」占位）")
	}
}

func TestManagerCreateBindsAgentWithoutTranscriptID(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Agent: "claude", Name: "No transcript yet"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := manager.GetAgentBinding(ctx, created.Key, "claude")
	if err != nil {
		t.Fatalf("create 后必须已有绑定行: %v", err)
	}
	if binding.AgentSessionID != "" {
		t.Fatalf("agent_session_id = %q, want 空（表示已选 agent、尚无转录）", binding.AgentSessionID)
	}
	if binding.AgentCtxSeq != 0 {
		t.Fatalf("agent_ctx_seq = %d, want 0（非 0 会被 prependSwitchHint 当成已同步而吞掉切换提示）", binding.AgentCtxSeq)
	}

	// 回合结束时真实 id 必须**覆盖**这一行，而不是多出一行。
	if err := manager.UpdateAgentState(ctx, created, "claude", 2, "real-id"); err != nil {
		t.Fatal(err)
	}
	again, err := manager.GetAgentBinding(ctx, created.Key, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if again.AgentSessionID != "real-id" || again.AgentCtxSeq != 2 {
		t.Fatalf("回合结束后 binding = %+v, want id=real-id ctx=2", again)
	}
	bindings, err := manager.listAgentBindingsUnsafe(created.Key)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 {
		t.Fatalf("绑定行数 = %d, want 1（占位行必须被覆盖而不是并存）", len(bindings))
	}
	items, err := manager.List(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Key == created.Key && InferAgentFromSession(item) != "claude" {
			t.Fatalf("回合结束后列表 agent = %q, want claude", InferAgentFromSession(item))
		}
	}
}

func TestManagerEnsureAgentBindingIsIdempotent(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	created, err := manager.Create(ctx, CreateInput{Type: TypeChat, Agent: "claude", Name: "Switch"})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(ctx, created, "claude", 7, "claude-id"); err != nil {
		t.Fatal(err)
	}
	// 回合开始时的补写不能把真实转录 id 打回空。
	if err := manager.EnsureAgentBinding(ctx, created.Key, "claude"); err != nil {
		t.Fatal(err)
	}
	binding, err := manager.GetAgentBinding(ctx, created.Key, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if binding.AgentSessionID != "claude-id" || binding.AgentCtxSeq != 7 {
		t.Fatalf("EnsureAgentBinding 覆盖了已有绑定: %+v", binding)
	}

	// 中途换 agent：新 agent 要立刻有占位行，老 agent 的行不动。
	if err := manager.EnsureAgentBinding(ctx, created.Key, "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.GetAgentBinding(ctx, created.Key, "codex"); err != nil {
		t.Fatalf("换 agent 后新 agent 必须有占位绑定: %v", err)
	}
	stillClaude, err := manager.GetAgentBinding(ctx, created.Key, "claude")
	if err != nil || stillClaude.AgentSessionID != "claude-id" {
		t.Fatalf("老 agent 的绑定被改动了: %+v err=%v", stillClaude, err)
	}

	// 空 agent（command 会话）不写行、不报错。
	if err := manager.EnsureAgentBinding(ctx, created.Key, "  "); err != nil {
		t.Fatalf("空 agent 必须静默跳过: %v", err)
	}
}

func TestManagerEnsureAgentBindingDoesNotLeakEmptyIDIntoAliases(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	ctx := context.Background()

	a, err := manager.Create(ctx, CreateInput{Type: TypeChat, Agent: "claude", Name: "Alpha"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := manager.Create(ctx, CreateInput{Type: TypeChat, Agent: "claude", Name: "Beta"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.LookupAliasForAgent("claude", ""); ok {
		t.Fatal("空 agent_session_id 不得命中别名表——否则两个会话会串名")
	}
	// Rename 会拿 agentSessionIDsUnsafe 的每个 (agent,id) 去写 session_external_names；
	// 空 id 必须被挡在门外，不能落进表里。
	if _, err := manager.Rename(ctx, a.Key, "Alpha renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Rename(ctx, b.Key, "Beta renamed"); err != nil {
		t.Fatal(err)
	}
	db, err := manager.ensureSessionMetaDBUnsafe()
	if err != nil {
		t.Fatal(err)
	}
	var leaked int
	if err := db.QueryRow(`SELECT count(*) FROM session_external_names WHERE agent_session_id = ''`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("session_external_names 里有 %d 行空 agent_session_id", leaked)
	}
}
