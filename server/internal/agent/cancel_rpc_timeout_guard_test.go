package agent

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 源码守卫：**取消路径上的跨进程 RPC 必须带超时**（G-G 不变量）。
//
// 为什么需要它：agent session 的取消（用户点「停止」）会打到后端的 RPC ——
// claude 的 `stream.Interrupt(ctx)`、acp 的 `conn.Cancel(ctx, ...)`。这些调用在
// CLI/对端迟迟不 ack 时会**永久挂起**（SDK 内部用 context.Background()，无超时），
// 卡死整个会话，表现为「停止按钮失效」。上层虽有 turn ctx 兜底，但那是取消本轮
// 生成、不等于让这个 RPC 返回。
//
// 三处曾各自为政：claude 补了 3s 超时（8bc5a43），acp 一直是裸 Background，
// 2026-10-13 起草后端规范时靠 reviewer 自查命令才扫出来（`-A3` 窗口还够不到，
// 改用 `-A15`）。因为 acp/codex 是上游实现、合上游会整体覆盖回去，这里用源码
// 扫描钉住纪律：**只要取消路径调了 RPC，同一函数里就必须出现 WithTimeout**。
func TestCancelPathRPCHasTimeout(t *testing.T) {
	// 取消路径上会阻塞的跨进程调用。codex 的 CancelCurrentTurn 只取消本地 context
	// （s.turn.Cancel()），不走 RPC，刻意不在列 —— 本地取消不需要超时。
	cases := []struct {
		file string
		call string
	}{
		{"claude/session.go", ".Interrupt("},
		{"acp/process.go", ".Cancel("},
	}

	for _, c := range cases {
		b, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", c.file, err)
		}
		src := string(b)

		// 只看取消路径：cancel/interrupt 相关函数体。用「函数名含 Cancel/Interrupt」
		// 定位，避免误伤初始化等其它 RPC 调用点。
		for _, fn := range cancelFuncs(src) {
			if !strings.Contains(fn.body, c.call) {
				continue
			}
			if !strings.Contains(fn.body, "WithTimeout") {
				t.Errorf("%s:%s 调用了 %s 但函数体内没有 WithTimeout —— "+
					"取消路径的跨进程 RPC 必须加超时，否则对端不 ack 会永久挂起（停止按钮失效）",
					c.file, fn.name, c.call)
			}
		}
	}
}

type funcBody struct {
	name string
	body string
}

var funcDeclRe = regexp.MustCompile(`(?m)^func \([^)]*\) ([A-Za-z0-9_]+)\(`)

// cancelFuncs 抽出函数名含 Cancel/Interrupt 的函数体（按大括号配对切）。
func cancelFuncs(src string) []funcBody {
	locs := funcDeclRe.FindAllStringSubmatchIndex(src, -1)
	var out []funcBody
	for _, loc := range locs {
		name := src[loc[2]:loc[3]]
		if !strings.Contains(strings.ToLower(name), "cancel") &&
			!strings.Contains(strings.ToLower(name), "interrupt") {
			continue
		}
		start := loc[1] - 1 // 指向函数名后的 '(' 之前的 ')' 收尾处，从声明尾找 '{'
		brace := strings.Index(src[loc[1]:], "{")
		if brace < 0 {
			continue
		}
		open := loc[1] + brace
		depth := 0
		end := open
		for i := open; i < len(src); i++ {
			switch src[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end != open {
				break
			}
		}
		_ = start
		out = append(out, funcBody{name: name, body: src[open:end]})
	}
	return out
}
