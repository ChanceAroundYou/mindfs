package kanban

import (
	"strings"
	"testing"
)

// S3：截断只能在真正的边界上。线上卡片上留下的是 `192.168.1.15:222` ——
// 旧实现把任何 ASCII 点号都当句末，地址、版本号、文件名里的点号全被当成
// 一句话的结尾，用户读到的是半截地址加半句正文。
func TestCutAtSentenceNeverStopsMidAddress(t *testing.T) {
	text := strings.Repeat("a", 50) + " 192.168.1.15:2222 的代理没有响应" + strings.Repeat("尾巴", 200)
	got := cutAtSentence(text, 60)
	if strings.Contains(got, "192.168") {
		t.Fatalf("cut mid-address: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated text must be marked with an ellipsis: %q", got)
	}
	if !strings.HasPrefix(text, strings.TrimSuffix(got, "…")) {
		t.Fatalf("cut must stay a prefix of the original: %q", got)
	}
}

// CJK 全角句号后面本来就不跟空格。把「标点后必须跟空白」无条件套上去，中文正文
// 就一个边界都找不到，全部退化成硬截 —— 这条钉住全角标点仍然独立成边界。
func TestCutAtSentenceKeepsCJKFullStopBoundary(t *testing.T) {
	text := strings.Repeat("很长的节点输出。", 12) // 96 runes
	got := cutAtSentence(text, 60)
	if !strings.HasSuffix(got, "。") {
		t.Fatalf("CJK full stop must stay a boundary: %q", tailRunes(got, 12))
	}
	if !strings.HasPrefix(text, got) {
		t.Fatalf("cut must stay a prefix of the original")
	}
}

// 没有句末标点就退到段落边界，而不是硬切半句话。
func TestCutAtSentenceFallsBackToParagraphBreak(t *testing.T) {
	head := strings.Repeat("过程记录没有标点", 10) // 80 runes
	text := head + "\n" + strings.Repeat("后面的正文", 100)
	got := cutAtSentence(text, 100)
	if strings.Contains(got, "\n") {
		t.Fatalf("paragraph fallback must not keep the newline: %q", tailRunes(got, 12))
	}
	if !strings.HasPrefix(text, strings.TrimSuffix(got, "…")) {
		t.Fatalf("cut must stay a prefix of the original: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated text must be marked with an ellipsis")
	}
}

// 结构优先：截断点落在一个结构化块（列表项 / 加粗标签 / 「没做」「未验证」段）
// 中间时，整块放弃，绝不留下半条 —— 「没做或未验」比少一段更误导。
func TestCutAtSentenceDoesNotSplitStructuralBlock(t *testing.T) {
	body := strings.Repeat("过程记录。", 100) // 500 runes
	block := "\n- **没做或未验证**：" + strings.Repeat("没有跑线上 e2e。", 30)
	text := body + block

	got := cutAtSentence(text, 560)
	if strings.Contains(got, "**没做") {
		t.Fatalf("structural block split in half: %q", tailRunes(got, 20))
	}
	if !strings.HasPrefix(text, strings.TrimSuffix(got, "…")) {
		t.Fatalf("cut must stay a prefix of the original: %q", got)
	}
}

// 存储上限与 Router 输入侧摘录同口径（1000 字），不再是 600。
func TestStoredSummaryLimitIsOneThousandRunes(t *testing.T) {
	if maxStoredSummaryRunes != 1000 {
		t.Fatalf("maxStoredSummaryRunes = %d, want 1000", maxStoredSummaryRunes)
	}
}
