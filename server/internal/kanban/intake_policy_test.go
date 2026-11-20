package kanban

import (
	"strings"
	"testing"
)

// 入站准入（Plan 2026-11-20 Task 11）：这一轮不接任何真实渠道，先把渠道接入那天
// 必须先有的形状钉住 —— 一次性列出全部问题（不是遇错即返），以及一张不可配置的
// 默认审核分类表。

func TestValidateIntakeAcceptsWellFormedRequest(t *testing.T) {
	req := IntakeRequest{
		RootID: "proj",
		Title:  "把登录页的报错文案改清楚",
		Body:   "现在报错只说「失败」，用户不知道该怎么办。",
		Source: "web",
	}
	if issues := ValidateIntake(req); len(issues) != 0 {
		t.Fatalf("ValidateIntake(合法请求) = %v, want 空", issues)
	}
}

func TestValidateIntakeReturnsAllProblemsAtOnce(t *testing.T) {
	req := IntakeRequest{
		RootID: "",
		Title:  strings.Repeat("长", intakeTitleMaxRunes+1),
		Body:   strings.Repeat("x", intakeBodyMaxBytes+1),
		Source: "carrier-pigeon",
	}
	issues := ValidateIntake(req)
	if len(issues) != 4 {
		t.Fatalf("ValidateIntake(四处问题) = %d 条 %v, want 4 条（遇错即返会只剩 1 条）", len(issues), issues)
	}
	for _, want := range []string{"root_id", "title", "body", "source"} {
		found := false
		for _, issue := range issues {
			if strings.Contains(issue, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("问题列表里没有提到 %q：%v", want, issues)
		}
	}
}

func TestValidateIntakeFieldsRejectsUnknownKeys(t *testing.T) {
	fields := map[string]string{
		"root_id":  "proj",
		"title":    "标题",
		"body":     "正文",
		"source":   "web",
		"priority": "high",
		"assignee": "someone",
	}
	issues := ValidateIntakeFields(fields)
	if len(issues) != 2 {
		t.Fatalf("ValidateIntakeFields(两个未知键) = %d 条 %v, want 2 条", len(issues), issues)
	}
	joined := strings.Join(issues, "\n")
	for _, want := range []string{"priority", "assignee"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("未知键 %q 没被点名：%v", want, issues)
		}
	}
}

func TestValidateIntakeFieldsMissingRequiredKey(t *testing.T) {
	issues := ValidateIntakeFields(map[string]string{"root_id": "proj", "source": "web"})
	joined := strings.Join(issues, "\n")
	if !strings.Contains(joined, "title") || !strings.Contains(joined, "body") {
		t.Fatalf("缺字段没有被一起列出：%v", issues)
	}
}

// 分类表有两个副本：Go 常量与 Router 提示词。两者漂移时 Router 会按一份过时的清单
// 判断「要不要问人」，所以这份拷贝由测试钉住。
func TestRouterPromptListsReviewCategories(t *testing.T) {
	if !strings.Contains(routerPromptText, "policy_hints") {
		t.Fatalf("Router 提示词里没有 policy_hints 段 —— 分类表没有交付给 Router")
	}
	for _, category := range ReviewCategories {
		if !strings.Contains(routerPromptText, category) {
			t.Fatalf("Router 提示词缺少审核分类 %q", category)
		}
	}
}

func TestReviewCategoriesHintListsAll(t *testing.T) {
	hint := ReviewCategoriesHint()
	for _, category := range ReviewCategories {
		if !strings.Contains(hint, category) {
			t.Fatalf("brief 的 policy_hints 那行漏了分类 %q：%s", category, hint)
		}
	}
}

func TestReviewCategoriesAreStable(t *testing.T) {
	want := []string{"删除数据", "停止服务", "花钱", "对外发消息", "改产品方向", "新建或轮换密钥", "修改权限"}
	if len(ReviewCategories) != len(want) {
		t.Fatalf("ReviewCategories = %d 项 %v, want %d 项", len(ReviewCategories), ReviewCategories, len(want))
	}
	seen := map[string]bool{}
	for i, category := range ReviewCategories {
		if category != want[i] {
			t.Fatalf("ReviewCategories[%d] = %q, want %q（顺序也是契约：提示词按这个顺序写）", i, category, want[i])
		}
		if seen[category] {
			t.Fatalf("ReviewCategories 有重复项 %q", category)
		}
		seen[category] = true
	}
}
