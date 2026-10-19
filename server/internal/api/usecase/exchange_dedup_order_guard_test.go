package usecase

import (
	"os"
	"strings"
	"testing"
)

// 源码守卫：**判重循环里时间窗必须先于取正文**。
//
// 为什么需要它：`exchangeAlreadyRecorded` 的内容兜底循环要拿每一行历史去和来行比对，
// 而「取正文」不是读内存 —— S2d-2 之后正文住在块流里，`recordedExchangeBody` 走的是
// `Manager.GetBlocksWindow`，一次就是「读盘 + 反序列化整段块窗口」。
//
// 判据顺序换一下结果一模一样（纯合取），代价却差一个数量级：
//
//	时间窗在后 → 每行历史都要解析一次块窗口 → 导入 N 条 = O(N²) 次解析
//	时间窗在前 → 只有时间上真挨着的那一两行需要读正文 → O(N)
//
// 实测（2026-10-19，pc-win 一份 105MB 的 codex 转录）：顺序反着时每条耗时从 5.3s
// 一路涨到 21s，整份导入 20 分钟起步；换过来后 150 条 **3.7 秒**导完。
//
// 这条守卫钉的是「便宜且高选择性的判据不许排到贵判据后面」—— 未来若有人往循环里
// 加新的读盘判据，请一并确认它排在了时间窗之后。
func TestExchangeDedupChecksTimestampWindowBeforeReadingBody(t *testing.T) {
	b, err := os.ReadFile("session_turn_identity.go")
	if err != nil {
		t.Fatalf("读 session_turn_identity.go 失败: %v", err)
	}
	src := string(b)

	start := strings.Index(src, "func exchangeAlreadyRecorded(")
	if start < 0 {
		t.Fatal("找不到 exchangeAlreadyRecorded —— 函数被改名或删除了，守卫需要跟着更新")
	}
	body := src[start:]
	if end := strings.Index(body[1:], "\nfunc "); end >= 0 {
		body = body[:end+1]
	}

	window := strings.Index(body, "userExchangeRepeatTolerance")
	read := strings.Index(body, "recordedExchangeBody")
	if window < 0 {
		t.Fatalf("exchangeAlreadyRecorded 里没有 userExchangeRepeatTolerance —— " +
			"时间窗判据没了，判重会把任意历史行都当成候选")
	}
	if read < 0 {
		t.Fatalf("exchangeAlreadyRecorded 里没有 recordedExchangeBody —— 内容兜底判据没了")
	}
	if window > read {
		t.Errorf("exchangeAlreadyRecorded 里 recordedExchangeBody 出现在时间窗判据之前 —— "+
			"那等于给每一行历史都解析一次块窗口，导入 N 条就是 O(N²) 次反序列化"+
			"（实测 105MB 转录 20 分钟起步；顺序正确时 3.7 秒）。"+
			"请把 `ex.Timestamp.Sub(ts)` 的窗口判断提到取正文之前。\n"+
			"  时间窗偏移=%d  取正文偏移=%d", window, read)
	}
}
