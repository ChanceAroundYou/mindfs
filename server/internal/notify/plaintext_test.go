package notify

import (
	"strings"
	"testing"
)

func TestPlainTextStripsMarkdownButKeepsContent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "fenced code block",
			in:   "结果如下：\n```go\nfunc main() { fmt.Println(\"hi\") }\n```\n完成",
			want: "结果如下： func main() { fmt.Println(\"hi\") } 完成",
		},
		{
			name: "inline code and emphasis",
			in:   "请跑 `go test`，**不要** 用 *默认* 值，~~旧结论~~",
			want: "请跑 go test，不要 用 默认 值，旧结论",
		},
		{
			name: "heading quote list rule",
			in:   "# 结论\n> 引用\n- 第一项\n1. 第二项\n---\n正文",
			want: "结论 引用 第一项 第二项 正文",
		},
		{
			name: "links and images",
			in:   "见 [文档](https://example.com/a) 和 ![截图](https://example.com/b.png)",
			want: "见 文档 和 截图",
		},
		{
			name: "table",
			in:   "| 项 | 值 |\n| --- | --- |\n| a | 1 |",
			want: "项 值 a 1",
		},
		{
			name: "code with markdown markers",
			in:   "```md\n**粗体** 与 `代码`\n```",
			want: "粗体 与 代码",
		},
		{
			// 表格行去掉竖线，但代码里的 shell 管道不能跟着被吃掉。
			name: "shell pipe survives",
			in:   "跑 `go test ./... | tail -5` 看结果",
			want: "跑 go test ./... | tail -5 看结果",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PlainText(tc.in, 0); got != tc.want {
				t.Fatalf("PlainText = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestPlainTextKeepsTailWhenTruncated(t *testing.T) {
	in := strings.Repeat("前", BodyMaxRunes) + "结尾"
	got := PlainText(in, BodyMaxRunes)
	if !strings.HasPrefix(got, "...") {
		t.Fatalf("truncated text should start with ellipsis, got %q", got)
	}
	if !strings.HasSuffix(got, "结尾") {
		t.Fatalf("truncated text should keep the tail, got %q", got)
	}
	if n := len([]rune(got)); n != BodyMaxRunes+3 {
		t.Fatalf("truncated text length = %d; want %d", n, BodyMaxRunes+3)
	}
}

func TestPlainTextLeavesShortTextAlone(t *testing.T) {
	if got := PlainText("短消息", BodyMaxRunes); got != "短消息" {
		t.Fatalf("PlainText = %q", got)
	}
}
