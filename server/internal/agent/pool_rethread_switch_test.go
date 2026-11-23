package agent

import (
	"testing"

	"mindfs/server/internal/agent/proxy"
)

// 会话里现场换绑（SendMessage 撞到供应商不一致后 fork）与打开时换绑必须做同一
// 件事：告诉代理这条线程的推理密文是旧供应商加密的。
//
// 打开路径记得这件事（pool.go 的 MarkProviderSwitch），现场换绑走的是
// onThreadStart → RegisterThread，只登记新线程不标记 —— 那条 fork 出来的历史
// 里仍然带着旧供应商的 encrypted_content，新供应商会整条请求拒掉
// （invalid_encrypted_content，G-PK 修的就是这个）。两条路径必须一致。
func TestRegisterRethreadedThreadMarksProviderSwitch(t *testing.T) {
	p, err := proxy.NewServerWithLiveCompact(proxy.LiveCompactConfig{ArchiveDir: t.TempDir()})
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}
	defer p.Close()

	pool := &Pool{compactProxy: p}
	pool.mu.Lock()
	pool.sessions = map[string]*sessionEntry{"sess-1": {sessionKey: "sess-1"}}
	pool.mu.Unlock()

	const forked = "01a203d2-f784-76b2-9856-dbfd674a366a"
	pool.RegisterRethreadedThread("sess-1", forked)

	if swapped := pool.compactProxy.ProviderSwitchMarked(forked); !swapped {
		t.Fatal("rethreaded thread must be marked as crossing a provider switch")
	}
}
