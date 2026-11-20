package conformance

import (
	"strings"
	"testing"
)

// 跨 harness 一致性探针（Plan 2026-11-20 Task 12 / spec §6.12）。这一组用例只覆盖
// 「形状比对」这一层：探针真的去连隔离实例时，拿回来的东西是否算一致由这里判定。
// 真机探针在 probe_live_test.go，缺 harness 时明确 skip。

func TestBuildTurnDedupesAndSorts(t *testing.T) {
	turn := BuildTurn("basic", []Observation{
		{Frame: "session.stream", Event: "message_done", Fields: []string{"data.tokenUsage", "data.contextWindow", "data.tokenUsage"}},
		{Frame: "session.accepted"},
		{Frame: "session.stream", Event: "message_chunk", Fields: []string{"data.text"}},
		{Frame: "session.done"},
		{Frame: "session.stream", Event: "message_chunk", Fields: []string{"data.text"}},
	})
	if got, want := strings.Join(turn.Frames, ","), "session.accepted,session.done,session.stream"; got != want {
		t.Fatalf("frames = %s, want %s", got, want)
	}
	if got, want := strings.Join(turn.Events, ","), "message_chunk,message_done"; got != want {
		t.Fatalf("events = %s, want %s", got, want)
	}
	if got, want := strings.Join(turn.Fields["message_done"], ","), "data.contextWindow,data.tokenUsage"; got != want {
		t.Fatalf("message_done fields = %s, want %s", got, want)
	}
}

func goodShapes() []Shape {
	return []Shape{
		{Harness: "claude", Turns: []Turn{
			BuildTurn("basic", []Observation{
				{Frame: "session.accepted"},
				{Frame: "session.stream", Event: "message_chunk", Fields: []string{"data.text"}},
				{Frame: "session.stream", Event: "message_done", Fields: usageFields()},
				{Frame: "session.done"},
			}),
			BuildTurn("cancel", []Observation{
				{Frame: "session.accepted"},
				{Frame: "session.stream", Event: "message_chunk", Fields: []string{"data.text"}},
				{Frame: "session.done"},
			}),
		}},
		{Harness: "codex", Turns: []Turn{
			BuildTurn("basic", []Observation{
				{Frame: "session.accepted"},
				{Frame: "session.stream", Event: "message_chunk", Fields: []string{"data.text"}},
				{Frame: "session.stream", Event: "message_done", Fields: usageFields()},
				{Frame: "session.done"},
			}),
			BuildTurn("cancel", []Observation{
				{Frame: "session.accepted"},
				{Frame: "session.stream", Event: "message_chunk", Fields: []string{"data.text"}},
				{Frame: "session.error"},
			}),
		}},
	}
}

func usageFields() []string {
	return []string{
		"data.contextWindow",
		"data.tokenUsage",
		"data.tokenUsage.inputTokens",
		"data.tokenUsage.outputTokens",
	}
}

func TestCompareAcceptsTwoMatchingHarnesses(t *testing.T) {
	if issues := Compare(goodShapes()...); len(issues) != 0 {
		t.Fatalf("两个形状一致的 harness 不应有问题：%v", issues)
	}
}

func TestCompareReportsMissingFrame(t *testing.T) {
	shapes := goodShapes()
	shapes[1].Turns[0] = BuildTurn("basic", []Observation{
		{Frame: "session.accepted"},
		{Frame: "session.stream", Event: "message_chunk", Fields: []string{"data.text"}},
		{Frame: "session.stream", Event: "message_done", Fields: usageFields()},
	})
	issues := Compare(shapes...)
	if !hasIssue(issues, "codex", "session.done") {
		t.Fatalf("缺终止帧没有被报出来：%v", issues)
	}
}

func TestCompareReportsMissingUsageField(t *testing.T) {
	shapes := goodShapes()
	shapes[1].Turns[0] = BuildTurn("basic", []Observation{
		{Frame: "session.accepted"},
		{Frame: "session.stream", Event: "message_chunk", Fields: []string{"data.text"}},
		{Frame: "session.stream", Event: "message_done", Fields: []string{"data.contextWindow"}},
		{Frame: "session.done"},
	})
	issues := Compare(shapes...)
	if !hasIssue(issues, "codex", "data.tokenUsage") {
		t.Fatalf("没有用量的 harness 必须被点名：%v", issues)
	}
}

func TestCompareReportsCrossHarnessFieldDrift(t *testing.T) {
	shapes := goodShapes()
	// claude 多报一个 codex 没有的字段：必须报出来，除非登记进例外表。
	shapes[0].Turns[0].Fields["message_done"] = append(shapes[0].Turns[0].Fields["message_done"], "data.sessionId")
	issues := Compare(shapes...)
	if !hasIssue(issues, "codex", "data.sessionId") {
		t.Fatalf("跨 harness 字段漂移没有被报出来：%v", issues)
	}
}

func TestCompareHonorsExceptionTable(t *testing.T) {
	shapes := goodShapes()
	shapes[0].Turns[0].Fields["message_done"] = append(shapes[0].Turns[0].Fields["message_done"], "data.tokenUsage.cacheReadTokens")
	shapes[1].Turns[0].Fields["message_done"] = append(shapes[1].Turns[0].Fields["message_done"], "data.threadId")
	issues := Compare(shapes...)
	if len(issues) != 0 {
		t.Fatalf("例外表里的字段不应算漂移：%v", issues)
	}
}

func TestCompareRequiresCancelTurnToSettle(t *testing.T) {
	shapes := goodShapes()
	shapes[1].Turns[1] = BuildTurn("cancel", []Observation{
		{Frame: "session.accepted"},
		{Frame: "session.stream", Event: "message_chunk", Fields: []string{"data.text"}},
	})
	issues := Compare(shapes...)
	if !hasIssue(issues, "codex", "cancel") {
		t.Fatalf("取消后没有终帧必须报出来：%v", issues)
	}
}

func TestCompareRejectsUnregisteredHarness(t *testing.T) {
	shapes := goodShapes()
	shapes[1].Harness = "gemini"
	issues := Compare(shapes...)
	if !hasIssue(issues, "gemini", "例外表") {
		t.Fatalf("新 harness 必须先登记例外表（哪怕为空）：%v", issues)
	}
}

func TestExceptionsCoverProbedHarnesses(t *testing.T) {
	for _, harness := range ProbedHarnesses {
		if _, ok := Exceptions[harness]; !ok {
			t.Fatalf("ProbedHarnesses 里的 %q 没有例外表条目", harness)
		}
	}
}

func hasIssue(issues []string, parts ...string) bool {
	for _, issue := range issues {
		matched := true
		for _, part := range parts {
			if !strings.Contains(issue, part) {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}
