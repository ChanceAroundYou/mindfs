package kanban

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// 入站准入（spec §6.11）。这一轮不接任何真实渠道，先把渠道接入那天必须先有的形状
// 固化下来：
//
//  1. 校验器一次性列出**全部**问题，不遇错即返 —— 渠道方拿到的是一份可一次性修完的
//     清单，而不是「修一个、再发现一个」的往返；
//  2. 一张不可配置的审核分类表 —— 这些事命中即要求人工确认，Router 不得自动执行。
//     用常量而不是配置：它描述的是产品对「什么事必须问人」的判断，不是部署差异。
//
// 端口 TaskIntake 保持未实现、未注册（spec §6.11 最后一条）。

// 入站字段上限。数字只影响渠道方的载荷，不参与调度；放宽只是编辑这里的常量。
const (
	intakeRootIDMaxRunes = 128
	intakeTitleMaxRunes  = 200
	intakeBodyMaxBytes   = 32 * 1024
)

// IntakeRequest 是渠道适配层最终要交出来的形状。
type IntakeRequest struct {
	RootID string
	Title  string
	Body   string
	Source string
}

// IntakeKnownFields 是渠道适配层允许提交的载荷键。未在此列出的键一律拒绝 ——
// 静默丢弃未知键会让渠道方以为设置生效了。
var IntakeKnownFields = []string{"root_id", "title", "body", "source"}

// intakeSources 是当前认可的来源标识。新增渠道时在这里加一条，而不是放开任意字符串：
// 来源会进任务元数据，写错一个字母就是一个永远对不上的统计口径。
var intakeSources = map[string]bool{
	"web":     true,
	"api":     true,
	"cron":    true,
	"webhook": true,
}

// ValidateIntake 校验值本身，返回全部问题（空表示通过）。顺序固定为
// root_id → title → body → source，测试与提示词都按这个顺序读。
func ValidateIntake(req IntakeRequest) []string {
	var issues []string
	if req.RootID == "" {
		issues = append(issues, "root_id: 不能为空")
	} else if utf8.RuneCountInString(req.RootID) > intakeRootIDMaxRunes {
		issues = append(issues, fmt.Sprintf("root_id: 超过 %d 个字符", intakeRootIDMaxRunes))
	}
	if req.Title == "" {
		issues = append(issues, "title: 不能为空")
	} else if utf8.RuneCountInString(req.Title) > intakeTitleMaxRunes {
		issues = append(issues, fmt.Sprintf("title: 超过 %d 个字符", intakeTitleMaxRunes))
	}
	if req.Body == "" {
		issues = append(issues, "body: 不能为空")
	} else if len(req.Body) > intakeBodyMaxBytes {
		issues = append(issues, fmt.Sprintf("body: 超过 %d 字节", intakeBodyMaxBytes))
	}
	if req.Source == "" {
		issues = append(issues, "source: 不能为空")
	} else if !intakeSources[req.Source] {
		issues = append(issues, fmt.Sprintf("source: 未知来源 %q", req.Source))
	}
	return issues
}

// ValidateIntakeFields 校验原始载荷：未知键与值问题一起返回，同一次就能改完。
func ValidateIntakeFields(fields map[string]string) []string {
	var issues []string
	known := make(map[string]bool, len(IntakeKnownFields))
	for _, key := range IntakeKnownFields {
		known[key] = true
	}
	var unknown []string
	for key := range fields {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown) // map 遍历无序，问题列表的顺序必须是稳定的
	for _, key := range unknown {
		issues = append(issues, fmt.Sprintf("未知字段: %s", key))
	}
	return append(issues, ValidateIntake(IntakeRequest{
		RootID: fields["root_id"],
		Title:  fields["title"],
		Body:   fields["body"],
		Source: fields["source"],
	})...)
}

// ReviewCategories 是默认审核分类表：命中即要求人工确认，Router 不得自动执行。
// 顺序也是契约 —— Router 提示词按这个顺序写，前端清单与事件按它归档。
var ReviewCategories = []string{
	"删除数据",
	"停止服务",
	"花钱",
	"对外发消息",
	"改产品方向",
	"新建或轮换密钥",
	"修改权限",
}

// ReviewCategoriesHint 是 brief.policy_hints 里那一行：提示词之外，Router 每轮
// 收到的数据里也有同一份清单 —— 提示词可能被后来的改动裁剪，brief 不会。
func ReviewCategoriesHint() string {
	return "默认审核分类（命中先问用户）：" + strings.Join(ReviewCategories, " / ")
}
