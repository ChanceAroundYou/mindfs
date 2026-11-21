package kanban

import (
	"context"
	"strings"
	"testing"
)

// W2 T1：brief 里的逐节点卡片。Router 要当节点级管理员，就得在**一次决策**里
// 看见每个节点现在是什么状态、跑过几次、上一次说了什么、现在能对它做什么 ——
// 而不是每问一个节点调一次工具。动作表由状态唯一决定，模型不猜。

func TestBriefRunCarriesNodeCards(t *testing.T) {
	ctx := context.Background()
	store, _, run, _ := newWorkflowCommentFixture(t, "task-cards", "run-cards", WorkflowDefinition{
		Nodes: []WorkflowNode{
			{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "改代码"}},
			{ID: "verify", Kind: NodeKindAgent, Params: map[string]any{"prompt": "验证"}},
		},
		Edges: []WorkflowEdge{{From: "execute", To: "verify"}},
	})
	nr := startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	if _, err := store.MarkWorkflowNodeRunUnknown(ctx, nr, "上游 502，回合未完成"); err != nil {
		t.Fatalf("MarkWorkflowNodeRunUnknown: %v", err)
	}
	task, err := store.GetTask(ctx, "task-cards")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	brief, err := buildSituationBrief(ctx, store, nil, RouterConfig{}.Normalized(), task, RouterTriggerUserMessage, "")
	if err != nil {
		t.Fatalf("buildSituationBrief: %v", err)
	}
	if brief.LatestRun == nil || len(brief.LatestRun.Nodes) != 2 {
		t.Fatalf("node cards = %+v, want two cards", brief.LatestRun)
	}
	failed, pending := brief.LatestRun.Nodes[0], brief.LatestRun.Nodes[1]
	if failed.NodeID != "execute" || failed.Kind != NodeKindAgent || failed.Status != NodeRunUnknown || failed.Attempt != 1 {
		t.Fatalf("failed card = %+v", failed)
	}
	if !strings.Contains(failed.Summary, "502") {
		t.Fatalf("failed card summary = %q, want the last output excerpt", failed.Summary)
	}
	if !hasBriefNodeAction(failed, "retry_node") || !hasBriefNodeAction(failed, "set_node_model") {
		t.Fatalf("failed card actions = %v, want retry_node + set_node_model", failed.Actions)
	}
	if pending.NodeID != "verify" || pending.Status != NodeRunPending || pending.Attempt != 0 {
		t.Fatalf("pending card = %+v", pending)
	}
	if !hasBriefNodeAction(pending, "skip_node") || !hasBriefNodeAction(pending, "set_node_model") {
		t.Fatalf("pending card actions = %v, want skip_node + set_node_model", pending.Actions)
	}
	if hasBriefNodeAction(pending, "retry_node") {
		t.Fatalf("pending card must not offer retry: %v", pending.Actions)
	}
}

// 运行中的节点只能取消，不能改档、跳过或重试 —— 「运行中不切换」是这一层的硬约束。
func TestBriefNodeCardRunningOffersOnlyCancel(t *testing.T) {
	ctx := context.Background()
	store, _, run, _ := newWorkflowCommentFixture(t, "task-cards-run", "run-cards-run", WorkflowDefinition{
		Nodes: []WorkflowNode{{ID: "execute", Kind: NodeKindAgent, Params: map[string]any{"prompt": "改代码"}}},
	})
	startWorkflowAgentNode(t, store, run.ID, "execute", "sess-execute")
	task, err := store.GetTask(ctx, "task-cards-run")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	brief, err := buildSituationBrief(ctx, store, nil, RouterConfig{}.Normalized(), task, RouterTriggerUserMessage, "")
	if err != nil {
		t.Fatalf("buildSituationBrief: %v", err)
	}
	card := brief.LatestRun.Nodes[0]
	if card.Status != NodeRunRunning {
		t.Fatalf("card status = %q, want running (projection may lag the row)", card.Status)
	}
	if len(card.Actions) != 1 || card.Actions[0] != "cancel_node" {
		t.Fatalf("running card actions = %v, want exactly [cancel_node]", card.Actions)
	}
}

// 卡片有上限：节点很多的图不能把 brief 撑爆，超出的节点只保留总数。
func TestBriefNodeCardsAreCapped(t *testing.T) {
	ctx := context.Background()
	nodes := make([]WorkflowNode, 0, 24)
	for i := 0; i < 24; i++ {
		nodes = append(nodes, WorkflowNode{ID: "n" + string(rune('a'+i)), Kind: NodeKindAgent, Params: map[string]any{"prompt": "x"}})
	}
	store, _, run, _ := newWorkflowCommentFixture(t, "task-cards-cap", "run-cards-cap", WorkflowDefinition{Nodes: nodes})
	task, err := store.GetTask(ctx, "task-cards-cap")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if run == nil {
		t.Fatal("run missing")
	}
	brief, err := buildSituationBrief(ctx, store, nil, RouterConfig{}.Normalized(), task, RouterTriggerUserMessage, "")
	if err != nil {
		t.Fatalf("buildSituationBrief: %v", err)
	}
	if got := len(brief.LatestRun.Nodes); got != briefNodeCardLimit {
		t.Fatalf("cards = %d, want the cap %d", got, briefNodeCardLimit)
	}
	if brief.LatestRun.NodeCardsTruncated != len(nodes)-briefNodeCardLimit {
		t.Fatalf("truncated = %d, want %d", brief.LatestRun.NodeCardsTruncated, len(nodes)-briefNodeCardLimit)
	}
}

func hasBriefNodeAction(card RouterBriefNode, action string) bool {
	for _, got := range card.Actions {
		if got == action {
			return true
		}
	}
	return false
}

// 卡片也要参与 brief 的 24 KiB 硬上限裁剪：16 张卡片各带 240 字节摘要时会和
// 其它段抢预算，超限必须先压卡片（摘要在前、条数在后），并把省略数写进
// node_cards_truncated —— 悄悄少几张卡比报错更糟。
func TestCapSituationBriefTrimsNodeCards(t *testing.T) {
	cards := make([]RouterBriefNode, 0, briefNodeCardLimit)
	for i := 0; i < briefNodeCardLimit; i++ {
		cards = append(cards, RouterBriefNode{
			NodeID:  "n" + string(rune('a'+i)),
			Kind:    NodeKindAgent,
			Status:  NodeRunFail,
			Attempt: 1,
			Summary: strings.Repeat("x", routerBriefNodeExcerpt),
		})
	}
	brief := &SituationBrief{
		Version: routerBriefVersion,
		Trigger: RouterBriefTrigger{Kind: RouterTriggerUserMessage},
		LatestRun: &RouterBriefRun{
			ID:     "run-cap",
			Status: StatusRunning,
			Nodes:  cards,
		},
	}
	// 塞一段超长留言把 brief 顶过上限，让裁剪只可能发生在卡片上。
	brief.Trigger.UserMessage = &RouterBriefUserMessage{Body: strings.Repeat("y", routerBriefMaxBytes)}
	capped := capSituationBrief(brief)
	if size := briefSize(capped); size > routerBriefMaxBytes {
		t.Fatalf("capped brief = %d bytes, want <= %d", size, routerBriefMaxBytes)
	}
	if capped.LatestRun == nil || len(capped.LatestRun.Nodes) == 0 {
		t.Fatalf("node cards dropped entirely: %+v", capped.LatestRun)
	}
	for _, card := range capped.LatestRun.Nodes {
		if len(card.Summary) > routerBriefNodeExcerpt/2+3 {
			t.Fatalf("card summary = %d bytes, want the shortened tier", len(card.Summary))
		}
	}
}
