package acp

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// 建立会话失败的进程必须被回收：Runtime 按 agent 缓存进程，只有
// KillAgentProcess/CloseAll 摘得掉，所以一个「零会话」的进程会一直常驻。
func TestReleaseUnusedProcessClosesProcessWithoutSessions(t *testing.T) {
	tree := &retryProcessTree{}
	proc := processForCloseTest(tree)
	rt := NewRuntime(context.Background())
	rt.processes["dsh"] = proc

	rt.releaseUnusedProcess("dsh")

	if _, ok := rt.processes["dsh"]; ok {
		t.Fatal("零会话的进程没有被摘掉，会常驻")
	}
	if tree.kills != 1 || tree.closes != 1 {
		t.Fatalf("进程没有被真正关掉：kills=%d closes=%d", tree.kills, tree.closes)
	}
}

// 还有别的会话挂在这个进程上时绝不能关它 —— 同 agent 的会话共用一个进程。
func TestReleaseUnusedProcessKeepsProcessWithLiveSessions(t *testing.T) {
	tree := &retryProcessTree{}
	proc := processForCloseTest(tree)
	proc.sessions = map[string]*sessionState{"other-session": {}}
	rt := NewRuntime(context.Background())
	rt.processes["dsh"] = proc

	rt.releaseUnusedProcess("dsh")

	if _, ok := rt.processes["dsh"]; !ok {
		t.Fatal("还有会话在用，进程却被摘掉了")
	}
	if tree.kills != 0 {
		t.Fatalf("还有会话在用，进程却被杀了：kills=%d", tree.kills)
	}
}

// 不回应 initialize 的 agent 不能让握手无限期挂着：getOrCreateProcess 用的是
// pool 生命周期的 processCtx，没有 deadline，而调用方持有 per-agent 运行时锁，
// 于是后续每个请求都堵在同一把锁上。
//
// 去掉 session.go 里的 initializeTimeout 包装后这个测试会挂到 go test 超时。
func TestHandshakeTimeoutReleasesSilentAgent(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep 不可用")
	}
	restore := initializeTimeout
	initializeTimeout = 500 * time.Millisecond
	defer func() { initializeTimeout = restore }()

	rt := NewRuntime(context.Background())
	defer rt.CloseAll()

	type result struct {
		err     error
		elapsed time.Duration
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		_, err := rt.getOrCreateProcess(OpenOptions{
			AgentName:  "silent",
			SessionKey: "s1",
			Command:    "sleep", // 既不读 stdin 也不吐 ACP 帧
			Args:       []string{"60"},
			Cwd:        t.TempDir(),
		})
		done <- result{err: err, elapsed: time.Since(start)}
	}()

	select {
	case got := <-done:
		if got.err == nil {
			t.Fatal("对着一个不讲 ACP 的进程握手居然成功了")
		}
		if got.elapsed > 30*time.Second {
			t.Fatalf("握手没有被封顶，耗时 %s：%v", got.elapsed, got.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("握手永不返回：initialize 没有超时")
	}

	if procs := rt.listProcesses(); len(procs) != 0 {
		t.Fatalf("握手失败的进程被缓存下来了：%d 个", len(procs))
	}
}
