package kanban

import (
	"strings"
	"testing"
)

// WorkflowNodeSessionName 的唯一职责：让同一条任务的每轮 worker 会话在会话列表里
// 分得开。名字本身是给人看的，所以只做「任务名 / #N · 节点名 · 留言首行」这条规则，
// 不做去重查询（纯函数，不碰 store）。

func TestWorkflowNodeSessionNameCombinesLabelAndUserInput(t *testing.T) {
	got := WorkflowNodeSessionName("任务详情面板修改 / #87", "实现", "修一下运行视图", NodeRun{})
	want := "任务详情面板修改 / #87 · 实现 · 修一下运行视图"
	if got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}

func TestWorkflowNodeSessionNameSkipsRedundantLabel(t *testing.T) {
	// base 在任务没名字时本来就会回落到节点名，再拼一遍就是噪音。
	if got := WorkflowNodeSessionName("实现", "实现", "", NodeRun{}); got != "实现" {
		t.Fatalf("name = %q, want 实现", got)
	}
}

func TestWorkflowNodeSessionNameDegradesGracefully(t *testing.T) {
	cases := []struct {
		name  string
		base  string
		label string
		input string
		want  string
	}{
		{"only base", " 任务 / #1 ", " ", "  ", "任务 / #1"},
		{"base and input", "任务 / #1", "", "跑一下测试", "任务 / #1 · 跑一下测试"},
		{"base and label", "任务 / #1", "实现", "", "任务 / #1 · 实现"},
		{"no base", "", "实现", "跑一下测试", "实现 · 跑一下测试"},
		{"nothing", "", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorkflowNodeSessionName(tc.base, tc.label, tc.input, NodeRun{}); got != tc.want {
				t.Fatalf("name = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWorkflowNodeSessionNameUsesFirstLineOnly(t *testing.T) {
	got := WorkflowNodeSessionName("任务 / #1", "实现", "第一行需求\n第二行细节\n第三行", NodeRun{})
	if strings.Contains(got, "第二行") {
		t.Fatalf("name = %q, must only carry the first line", got)
	}
	if got != "任务 / #1 · 实现 · 第一行需求" {
		t.Fatalf("name = %q, want 任务 / #1 · 实现 · 第一行需求", got)
	}
}

func TestWorkflowNodeSessionNameTruncatesByRune(t *testing.T) {
	// 20 个 rune 是上限：中文不能被按 byte 切断成乱码。
	input := strings.Repeat("需", 30)
	got := WorkflowNodeSessionName("", "", input, NodeRun{})
	want := strings.Repeat("需", 20) + "…"
	if got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("name %q contains a broken rune", got)
	}
}

// 两轮不同留言必须得到不同名字 —— 这是这个函数存在的理由。
func TestWorkflowNodeSessionNameDiffersAcrossRounds(t *testing.T) {
	first := WorkflowNodeSessionName("任务 / #1", "实现", "第一条需求", NodeRun{})
	second := WorkflowNodeSessionName("任务 / #1", "实现", "第二条需求", NodeRun{})
	if first == second {
		t.Fatalf("two rounds share the same name %q", first)
	}
}

// 重试是同一条留言把同一个节点又跑一遍：节点名与留言逐字相同，标签只能来自
// attempt。线上真的出现过（task_94ec32d 的 execute 两次尝试两个会话）。
func TestWorkflowNodeSessionNameDiffersAcrossAttempts(t *testing.T) {
	first := NodeRun{Attempt: 1}
	second := NodeRun{Attempt: 2}
	got1 := WorkflowNodeSessionName("任务 / #1", "实现", "修一下运行视图", first)
	got2 := WorkflowNodeSessionName("任务 / #1", "实现", "修一下运行视图", second)
	if got1 == got2 {
		t.Fatalf("two attempts share the same name %q", got1)
	}
	if want := "任务 / #1 · 实现 · 第2次 · 修一下运行视图"; got2 != want {
		t.Fatalf("attempt 2 name = %q, want %q", got2, want)
	}
	// 首次尝试不带噪音。
	if want := "任务 / #1 · 实现 · 修一下运行视图"; got1 != want {
		t.Fatalf("attempt 1 name = %q, want %q", got1, want)
	}
}

// 循环迭代走的是同一条路：每次迭代都是新 NodeRun、新会话、同样的留言。
func TestWorkflowNodeSessionNameDiffersAcrossLoopIterations(t *testing.T) {
	first := NodeRun{Attempt: 1, Iteration: 1}
	second := NodeRun{Attempt: 1, Iteration: 2}
	got1 := WorkflowNodeSessionName("任务 / #1", "实现", "跑一遍", first)
	got2 := WorkflowNodeSessionName("任务 / #1", "实现", "跑一遍", second)
	if got1 == got2 {
		t.Fatalf("two loop iterations share the same name %q", got1)
	}
	if want := "任务 / #1 · 实现 · 第2轮 · 跑一遍"; got2 != want {
		t.Fatalf("iteration 2 name = %q, want %q", got2, want)
	}
	if want := "任务 / #1 · 实现 · 跑一遍"; got1 != want {
		t.Fatalf("iteration 1 name = %q, want %q", got1, want)
	}
}

func TestWorkflowNodeRunMarker(t *testing.T) {
	cases := []struct {
		name    string
		nodeRun NodeRun
		want    string
	}{
		{"first attempt", NodeRun{Attempt: 1}, ""},
		{"zero value", NodeRun{}, ""},
		{"retry", NodeRun{Attempt: 3}, "第3次"},
		{"loop first round", NodeRun{Attempt: 1, Iteration: 1}, ""},
		{"loop second round", NodeRun{Attempt: 1, Iteration: 2}, "第2轮"},
		{"loop retry", NodeRun{Attempt: 2, Iteration: 3}, "第3轮 第2次"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorkflowNodeRunMarker(tc.nodeRun); got != tc.want {
				t.Fatalf("marker = %q, want %q", got, tc.want)
			}
		})
	}
}
