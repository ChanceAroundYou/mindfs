package agent

import "testing"

// HasActiveUse 是「这个会话现在有回合在跑吗」的唯一判据，换目录（repoint）靠它决定
// 能不能动手 —— repoint 第一件事是 pool.Close 掐掉 agent 进程，跑着的回合会被腰斩。
//
// 三种形态必须分清：池里没这个会话（0）、开着但空闲（0）、有回合在跑（1）。
// 把「开着但空闲」也当成忙会让换目录永远做不了（会话一被用过就留在池里），
// 反过来把「在跑」漏成不忙，代价是用户丢半句话。
func TestPoolHasActiveUse(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	defer pool.CloseAll()

	if pool.HasActiveUse("never-opened") {
		t.Fatal("a session that was never opened must not report an active use")
	}

	pool.sessions["s-idle"] = &sessionEntry{agentName: "test-agent", sessionKey: "s-idle"}
	if pool.HasActiveUse("s-idle") {
		t.Fatal("an open but idle session must not report an active use — repoint may close it")
	}

	finish := pool.BeginSessionUse("s-idle")
	if !pool.HasActiveUse("s-idle") {
		t.Fatal("a session with a turn in flight must report an active use")
	}
	finish()
	if pool.HasActiveUse("s-idle") {
		t.Fatal("active use must be released when the turn finishes")
	}
	// 重复调用 finish 不能把计数打成负数（否则下一次 BeginSessionUse 要加两次才回正）。
	finish()
	pool.BeginSessionUse("s-idle")()
	if pool.HasActiveUse("s-idle") {
		t.Fatal("finish must be idempotent: a double release must not leave the entry busy")
	}
}

// BeginSessionUse 对不存在的会话返回空操作，不能凭空建条目 —— 否则 HasActiveUse 会把
// 一个从没开过的会话报成忙，换目录被永久挡住。
func TestPoolBeginSessionUseDoesNotCreateEntries(t *testing.T) {
	pool := NewPool(loadPoolTestConfig(t))
	defer pool.CloseAll()

	pool.BeginSessionUse("ghost")()
	if _, ok := pool.sessions["ghost"]; ok {
		t.Fatal("BeginSessionUse must not create an entry for an unknown session")
	}
	if pool.HasActiveUse("ghost") {
		t.Fatal("unknown session reported busy")
	}
}
