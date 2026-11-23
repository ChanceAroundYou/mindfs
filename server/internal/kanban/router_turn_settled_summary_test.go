package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// turnSettledEventPayload 造一份 turn_settled 载荷；toolCalls 是这一轮会话里
// 真实发生的工具调用次数（0 = 纯文本闲聊）。
func turnSettledEventPayload(t *testing.T, turnID string, toolCalls int) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"session_key": "sess-" + turnID,
		"turn_id":     turnID,
		"has_output":  true,
		"tool_calls":  toolCalls,
		"excerpt":     "#68 本轮跑完三步。待你拍板：1) 是否上线 2) 换哪个模型 3) 要不要改摘要口径",
	})
	must(t, err, "marshal turn_settled payload")
	return string(raw)
}

// consumeTurnSettled 让 Router 消费一条 turn_settled 事件并等它落到终态。
func consumeTurnSettled(t *testing.T, f *coordinateFixture, turnID string, toolCalls int, sayBody string) RouterEvent {
	t.Helper()
	f.agent.calls = []scriptedRouterCall{{tool: "router_say", args: map[string]any{
		"body": sayBody, "purpose": "progress",
	}}}
	ev := f.addEvent(t, RouterEvent{
		Kind:           RouterEventTurnSettled,
		TurnID:         turnID,
		UserActivityID: f.addUserActivity(t, "uact-"+turnID),
		PayloadJSON:    turnSettledEventPayload(t, turnID, toolCalls),
	})
	f.router.ConsumeRouterEvents(context.Background(), time.Now().UTC())
	f.waitEvent(t, ev.ID, RouterEventDone)
	return ev
}

// 会话里真的跑过工具的一轮，结算时最后一条 say 要升成总结并落到任务行。
//
// 线上 #68：Router 在 turn_settled 回合里把四条待决策项完整说了一遍，写成的是
// purpose=progress，于是任务摘要一直是异步压缩器给的那段短句，决策项全丢 ——
// 用户看到的就是「总结完全偏离要点」。判据是「这一轮有真实执行结果」，不是
// 「回合结算了」（每轮都结算，全升就把闲聊顶成卡片结论）。
func TestTurnSettledPromotesLastSayWhenTurnHadToolCalls(t *testing.T) {
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)

	body := "本轮结论：三步都跑通了。待你拍板：1) 是否上线 2) 换哪个模型 3) 要不要改摘要口径"
	consumeTurnSettled(t, f, "turn-tools", 3, body)

	task, err := f.store.GetTask(context.Background(), "task-1")
	must(t, err, "GetTask")
	if task.ResultSummary != body {
		t.Fatalf("result_summary = %q, want the router summary %q", task.ResultSummary, body)
	}
}

// Router 写下的总结不能被迟到的异步压缩器覆盖。
//
// 压缩是「调度时记下期望值、落地时 CAS」的两步写：调度在回合结束那一刻，落地
// 可能晚几十秒（线上真实耗时 25–90s）。Router 的结算总结恰好落在这段窗口里；
// 若压缩器不看当前值就写，卡片会从「四条待决策项」退回压缩器那段短句 —— 那正是
// #68「总结完全偏离要点」的另一半。
func TestRouterSummarySurvivesLateCompression(t *testing.T) {
	ctx := context.Background()
	raw := longDirectReply()
	compressor := &stubCompressor{
		out:     "压缩器给出的短句",
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	f, svc, act := directSummaryFixture(t, compressor)
	f.service.SetDirectExecutor(directStubExecutor{out: raw})

	task, err := f.tasks.GetTask(ctx, "task-1")
	must(t, err, "GetTask")
	f.service.executeDirectActivity(ctx, act, task, "把这三处改掉")

	select {
	case <-compressor.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("compressor never started")
	}
	routerSummary := "本轮结论：三处已改完。待你拍板：1) 是否上线 2) 换哪个模型"
	if err := f.tasks.UpdateTaskResultSummary(ctx, "task-1", routerSummary); err != nil {
		t.Fatalf("UpdateTaskResultSummary: %v", err)
	}
	close(compressor.release)
	if err := svc.WaitActivityTurnsIdle(ctx); err != nil {
		t.Fatalf("WaitActivityTurnsIdle: %v", err)
	}

	loaded, err := f.tasks.GetTask(ctx, "task-1")
	must(t, err, "GetTask after compression")
	if loaded.ResultSummary != routerSummary {
		t.Fatalf("result_summary = %q, want the router summary %q (压缩器覆盖了它)", loaded.ResultSummary, routerSummary)
	}
}

// 纯文本回合（零工具调用）不能顶成卡片结论：没有执行结果可总结，
// 升上去只会让「每轮闲聊」变成任务摘要。
func TestTurnSettledKeepsProgressWhenTurnHadNoToolCalls(t *testing.T) {
	f := newCoordinateFixture(t, RouterModeCoordinate)
	f.stopLoop(t)

	consumeTurnSettled(t, f, "turn-chat", 0, "收到，我把口径记下来了。")

	task, err := f.store.GetTask(context.Background(), "task-1")
	must(t, err, "GetTask")
	if strings.TrimSpace(task.ResultSummary) != "" {
		t.Fatalf("result_summary = %q, want empty (纯文本回合不该写卡片结论)", task.ResultSummary)
	}
}
