package kanban

import (
	"strings"
	"testing"
)

// M2：harness 的错误行后面跟了大段正文时，此前会被判成「正常回复」。
//
// `harnessFailureText` 原先有一条 `len > 400 字就返回空` 的提前退出，本意是
// 「别把正常回复里恰好出现的词判成错误」，但它把最需要报错的形状挡在外面：
// 那些行首就是 `Prompt is too long`、后面却跟了 2000–2400 字正文的回合
// （线上实测 3 条）全部被记成 completed，任务最后以 success 收尾。
//
// 新判据：先只认「开头」——首行或首 200 字命中已知签名即判失败，远距离出现的
// 关键词仍然不算（那正是原门槛想防的）。返回的说明仍走一句人话，正文留在原地
// 供诊断。
func TestHarnessFailureDetectedWhenErrorLineHeadsLongBody(t *testing.T) {
	// 三条真实夹具的开头（正文取自线上任务库，见 m2 取证）。
	bodies := []string{
		"Prompt is too long\n\n三点都已实现，提交 `7ea0f827`（`Scope: G-NY`），**未 push**。",
		"Prompt is too long\n\n现在开始实现。先处理共用的 Enter 键语义：",
		"Prompt is too long\n\n## 前端部署完成（仅本机静态资源，未重启任何服务）",
	}
	for _, body := range bodies {
		padded := body + strings.Repeat("\n后续正文。", 600) // 拉到 2000+ 字，复现线上长度
		if msg := harnessFailureText(padded); msg == "" {
			t.Fatalf("首行是 harness 错误时必须判失败：%q", headLine(padded))
		}
	}
}

// 反向守卫：关键词出现在正文中段（不是开头）仍然不算失败 —— 否则正常的长回复
// 只要提到「context length」就会被判红。
func TestHarnessFailureIgnoresKeywordDeepInBody(t *testing.T) {
	body := strings.Repeat("这是一段正常的技术说明。", 100) +
		"\n我们顺带聊到 prompt is too long 这条报错的成因，但那不是本轮结论。"
	if msg := harnessFailureText(body); msg != "" {
		t.Fatalf("中段关键词不该判失败：%q", msg)
	}
}

// 短错误行（真实 215 字那条）仍要照旧识别。
func TestHarnessFailureDetectsShortErrorLine(t *testing.T) {
	body := "这一轮虽标记为有输出，但输出内容只有 `Prompt is too longPrompt is too long`"
	if msg := harnessFailureText(body); msg == "" {
		t.Fatalf("短错误行必须判失败")
	}
}

func headLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
