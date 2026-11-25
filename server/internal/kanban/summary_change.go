package kanban

import (
	"encoding/json"
	"strings"
)

// 结算总结「有实质变化」才覆盖卡片（S4）。
//
// 线上 #104：用户在任务会话里问了一句，随后的失败通知回合没有跑任何工具，却把
// 上一轮那段总结换个说法又写了一遍 —— `purpose=summary` 无条件覆盖
// `result_summary`，卡片上的实质结论被这句复读顶掉。
//
// 判据全部机械可判，不调模型：
//   - 这一轮真的跑过工具（`decision.HadToolCalls`）—— 有真实执行结果就该更新卡片；
//   - 这一轮是系统发起的（`out_of_band=false`）—— 用户在会话里自己推的那一轮
//     必然带来新要求，它写下的结论要落卡片；
//   - 这一轮没有作答提问卡（`answered_question=false`）—— 作答本身是新信息；
//   - 新总结与卡片上那条的字面高度相似（字符三元组 Jaccard > 0.6）。
//
// 四条同时成立才降级成 progress。宁可漏降级（多覆盖一次卡片），也不误降级
// （该更新的结论没写上去）。

// repeatedSummarySimilarity 是「同一句话换个说法」的阈值。0.6 留出「复验」→
// 「复验一次」、「端到端」→「端到端验证」这类改写的余量，又不至于把措辞不同
// 的两段实质结论判成重复（实测一对毫不相干的四段总结只有 0.02）。
const repeatedSummarySimilarity = 0.6

// demoteRepeatedSettlementSummary 报告这条结算总结是否只是复读、应降级成 progress。
func demoteRepeatedSettlementSummary(decision *RouterDecision, task Task, body string) bool {
	if decision == nil || decision.TriggerKind != RouterTriggerTurnSettled {
		return false
	}
	if decision.HadToolCalls {
		return false
	}
	if routerBriefEventFlag(decision.BriefJSON, "out_of_band") ||
		routerBriefEventFlag(decision.BriefJSON, "answered_question") {
		return false
	}
	previous := strings.TrimSpace(task.ResultSummary)
	if previous == "" {
		return false
	}
	return charTrigramJaccard(previous, body) > repeatedSummarySimilarity
}

// routerBriefEventFlag 读事件回合 brief 里 event.payload 的一个布尔标记。
// 解析失败或键不存在都当 false：判据缺证据时按「不是这个情形」处理，宁可不降级。
func routerBriefEventFlag(briefJSON, key string) bool {
	raw := strings.TrimSpace(briefJSON)
	if raw == "" || key == "" {
		return false
	}
	var brief SituationBrief
	if err := json.Unmarshal([]byte(raw), &brief); err != nil || brief.Event == nil {
		return false
	}
	value, _ := brief.Event.Payload[key].(bool)
	return value
}

// charTrigramJaccard 是两段文本的字符三元组 Jaccard 相似度（抹掉空白后比较）。
//
// 选字符三元组而不是分词：中文没有空格，按词切需要词典；三元组对「换个说法」
// 足够敏感，又是确定性的纯函数（同样的输入永远同样的分数，可写死断言）。
// 任一文本不足三元时按整串当唯一元素比较。
func charTrigramJaccard(a, b string) float64 {
	left := charTrigrams(a)
	right := charTrigrams(b)
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	common := 0
	for gram := range left {
		if _, ok := right[gram]; ok {
			common++
		}
	}
	union := len(left) + len(right) - common
	if union <= 0 {
		return 0
	}
	return float64(common) / float64(union)
}

// charTrigrams 把文本压成字符三元组集合（空白不参与比较）。
func charTrigrams(text string) map[string]struct{} {
	runes := make([]rune, 0, len(text))
	for _, r := range text {
		switch r {
		case ' ', '\t', '\n', '\r', '　':
			continue
		}
		runes = append(runes, r)
	}
	out := make(map[string]struct{}, len(runes))
	if len(runes) < 3 {
		if len(runes) > 0 {
			out[string(runes)] = struct{}{}
		}
		return out
	}
	for i := 0; i+3 <= len(runes); i++ {
		out[string(runes[i:i+3])] = struct{}{}
	}
	return out
}
