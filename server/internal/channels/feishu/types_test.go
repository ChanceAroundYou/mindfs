package feishu

import "testing"

// 目标确认卡上的可编辑字段（任务号、标题）要原样带到点击处理里，否则
// 「确认」只能确认卡片发送时的默认值，用户改过的就丢了。
func TestParseActionKeepsTargetFields(t *testing.T) {
	line := []byte(`{"type":"card.action.trigger","operator_id":"ou_owner","message_id":"om_9",` +
		`"action_value":"{\"card\":\"target_1\",\"choice\":0,\"fields\":{\"task_no\":\"#42\",\"title\":\"修登录\"}}"}`)
	ev, ok := ParseEvent(line)
	if !ok {
		t.Fatal("event not parsed")
	}
	act, ok := ev.ParseAction()
	if !ok {
		t.Fatal("action not parsed")
	}
	if act.CardID != "target_1" || act.Choice != 0 {
		t.Fatalf("card=%q choice=%d", act.CardID, act.Choice)
	}
	if act.Fields["task_no"] != "#42" {
		t.Fatalf("task_no = %q, want #42", act.Fields["task_no"])
	}
	if act.Fields["title"] != "修登录" {
		t.Fatalf("title = %q, want 修登录", act.Fields["title"])
	}
}

// 没有 fields（或 fields 为空对象）时是零值，不 panic、不伪造条目。
func TestParseActionWithoutFieldsIsEmpty(t *testing.T) {
	for _, raw := range []string{
		`{"type":"card.action.trigger","action_value":"{\"card\":\"gate_1\",\"choice\":1}"}`,
		`{"type":"card.action.trigger","action_value":"{\"card\":\"gate_1\",\"choice\":1,\"fields\":{}}"}`,
	} {
		ev, ok := ParseEvent([]byte(raw))
		if !ok {
			t.Fatalf("event not parsed: %s", raw)
		}
		act, ok := ev.ParseAction()
		if !ok {
			t.Fatalf("action not parsed: %s", raw)
		}
		if len(act.Fields) != 0 {
			t.Fatalf("fields = %v, want empty", act.Fields)
		}
		if act.Fields["task_no"] != "" {
			t.Fatalf("missing key read non-empty: %q", act.Fields["task_no"])
		}
	}
}

// 目标卡没有选项：Chosen() 返回空串，但 Target 仍可读 —— 点击处理要靠它
// 知道这次确认的是哪台节点、哪个项目、哪个任务。
func TestTargetCardKeepsTargetAfterChosen(t *testing.T) {
	card := Card{
		Kind: KindTarget,
		Target: &TargetRef{
			NodeID: "fn", RootID: "/srv/docs", TaskID: "task_1",
			TaskNumber: 42, TaskTitle: "修登录", ProjectLabel: "docs · fn",
		},
	}
	if got := card.Chosen(); got != "" {
		t.Fatalf("Chosen() = %q, want empty", got)
	}
	if card.Target == nil || card.Target.TaskNumber != 42 || card.Target.TaskID != "task_1" {
		t.Fatalf("target lost: %+v", card.Target)
	}
}
