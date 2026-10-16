package acp

import (
	"sync"
	"testing"
	"time"
)

// TestPendingAskUserRegistryConcurrency 守住「同一 callID 只登记一次」与
// 「删除与投递互斥」这两条不变量 —— waiter 容量 1 且每条目只投递一次，
// 靠 elicitationMu 保证，一旦破掉就是 panic 或永久阻塞。
func TestPendingAskUserRegistryConcurrency(t *testing.T) {
	proc := newElicitationTestProcess()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			proc.registerPendingAskUser("call-1", "session-a", []elicitationQuestion{{ID: "a", Question: "甲？"}})
			proc.reapPendingAskUser("call-1", "completed")
			proc.cancelPendingAskUser("session-a")
			proc.dropAllPendingAskUser()
		}(i)
	}
	wg.Wait()

	proc.elicitationMu.Lock()
	defer proc.elicitationMu.Unlock()
	if len(proc.pendingAskUserByCallID) != 0 {
		t.Fatalf("pending = %d, want 0", len(proc.pendingAskUserByCallID))
	}
}

// TestBindElicitationIsExclusiveUnderConcurrency 钉住「匹配与占位必须原子」：
// 两个并发的 elicitation（不同会话问了同一道题）不能绑到同一条目，
// 否则一个拿到答案、另一个只能干等到 ctx 取消。
func TestBindElicitationIsExclusiveUnderConcurrency(t *testing.T) {
	shortenElicitationBindWait(t)
	proc := newElicitationTestProcess()
	questions := []elicitationQuestion{{ID: "confirm", Question: "继续吗？", Options: []string{"继续"}}}
	for _, callID := range []string{"call-a", "call-b", "call-c", "call-d"} {
		proc.registerPendingAskUser(callID, "session-"+callID, questions)
	}

	const workers = 8
	results := make(chan *pendingAskUser, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- proc.bindElicitation(questions)
		}()
	}
	wg.Wait()
	close(results)

	bound := map[string]int{}
	for entry := range results {
		if entry != nil {
			bound[entry.callID]++
		}
	}
	if len(bound) != 4 {
		t.Fatalf("bound = %#v, want 4 distinct entries（每个条目最多被绑一次）", bound)
	}
	for callID, count := range bound {
		if count != 1 {
			t.Fatalf("%s bound %d times, want 1", callID, count)
		}
	}
}

// TestReapReleasesBoundElicitation 钉住「提问被放弃」这条路的收尾：
// 工具卡进终态时，已绑定的条目也要删，并且要往 waiter 投一个空结果把阻塞的
// handler 放出来（实测踩过：第一次实测那轮提问被打断，条目就这么挂着）。
func TestReapReleasesBoundElicitation(t *testing.T) {
	proc := newElicitationTestProcess()
	questions := []elicitationQuestion{{ID: "confirm", Question: "继续吗？"}}
	proc.registerPendingAskUser("call-1", "session-a", questions)

	entry := proc.bindElicitation(questions)
	if entry == nil {
		t.Fatal("bindElicitation = nil, want entry")
	}

	proc.reapPendingAskUser("call-1", "completed")

	proc.elicitationMu.Lock()
	_, still := proc.pendingAskUserByCallID["call-1"]
	proc.elicitationMu.Unlock()
	if still {
		t.Fatal("bound entry survived reapPendingAskUser")
	}
	select {
	case result := <-entry.waiter:
		if len(result.content) != 0 {
			t.Fatalf("result = %#v, want empty（放弃 = decline）", result)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter was not released —— handler 会一直阻塞")
	}
}

// TestReapKeepsEntryWhileRunning 钉住 reap 只认终态：
// in_progress/running 时清掉条目会让正常提问永远关联不上。
func TestReapKeepsEntryWhileRunning(t *testing.T) {
	proc := newElicitationTestProcess()
	proc.registerPendingAskUser("call-1", "session-a", []elicitationQuestion{{ID: "confirm", Question: "继续吗？"}})
	for _, status := range []string{"running", "pending", "in_progress", ""} {
		proc.reapPendingAskUser("call-1", status)
		proc.elicitationMu.Lock()
		_, still := proc.pendingAskUserByCallID["call-1"]
		proc.elicitationMu.Unlock()
		if !still {
			t.Fatalf("status %q 清掉了条目，want kept", status)
		}
	}
}

// TestReleasePendingAskUserUnblocksReregistration 钉住 ctx 取消那条路的释放：
// 释放后同一道题再问一次必须能重新登记并被匹配到 —— 不释放的话，上一次的废弃条目
// 会因为 id+文本相同而被优先匹配，答案投进没人读的 waiter。
func TestReleasePendingAskUserUnblocksReregistration(t *testing.T) {
	proc := newElicitationTestProcess()
	questions := []elicitationQuestion{{ID: "confirm", Question: "继续吗？"}}

	proc.registerPendingAskUser("call-1", "session-a", questions)
	first := proc.bindElicitation(questions)
	if first == nil {
		t.Fatal("first bind = nil")
	}
	// 模拟 handler 的 ctx 被取消：handler 自己释放条目。
	proc.releasePendingAskUser(first)

	proc.elicitationMu.Lock()
	_, still := proc.pendingAskUserByCallID["call-1"]
	proc.elicitationMu.Unlock()
	if still {
		t.Fatal("entry survived releasePendingAskUser")
	}

	// 再问一次同一道题（新的 callID）。
	proc.registerPendingAskUser("call-2", "session-a", questions)
	second := proc.bindElicitation(questions)
	if second == nil || second.callID != "call-2" {
		t.Fatalf("second bind = %v, want call-2", second)
	}
}

// TestReleasePendingAskUserIgnoresStaleEntry 钉住指针比较：拿着旧条目去释放，
// 不能误删同 callID 的新条目。
func TestReleasePendingAskUserIgnoresStaleEntry(t *testing.T) {
	proc := newElicitationTestProcess()
	questions := []elicitationQuestion{{ID: "confirm", Question: "继续吗？"}}
	stale := &pendingAskUser{callID: "call-1", sessionKey: "session-a", questions: questions}

	proc.registerPendingAskUser("call-1", "session-a", questions)
	proc.releasePendingAskUser(stale)

	proc.elicitationMu.Lock()
	current := proc.pendingAskUserByCallID["call-1"]
	proc.elicitationMu.Unlock()
	if current == nil {
		t.Fatal("stale entry release deleted the live entry")
	}
}
