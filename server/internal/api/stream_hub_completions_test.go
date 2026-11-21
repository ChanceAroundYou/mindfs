package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompletionRingAppendSinceHead(t *testing.T) {
	ring := newCompletionRing()
	now := time.Now().UTC()
	first := ring.Append(SessionCompletion{RootID: "r1", SessionKey: "s1"}, now)
	second := ring.Append(SessionCompletion{RootID: "r1", SessionKey: "s2"}, now)
	if first != 1 || second != 2 {
		t.Fatalf("seq 必须单调递增，得到 first=%d second=%d", first, second)
	}
	items, seq, epoch := ring.Since(1)
	if len(items) != 1 || items[0].SessionKey != "s2" {
		t.Fatalf("Since(1) 应只返回 s2，得到 %+v", items)
	}
	if seq != 2 || epoch == "" {
		t.Fatalf("seq=%d epoch=%q", seq, epoch)
	}
	headSeq, headEpoch := ring.Head()
	if headSeq != 2 || headEpoch != epoch {
		t.Fatalf("Head 与 Since 不一致：%d/%q vs %d/%q", headSeq, headEpoch, seq, epoch)
	}
	if ring.HasSince(2) {
		t.Fatal("游标追平后不应再有新行")
	}
	if !ring.HasSince(1) {
		t.Fatal("游标落后时应报告有新行")
	}
}

func TestCompletionRingDropsOverflowAndExpired(t *testing.T) {
	ring := newCompletionRing()
	now := time.Now().UTC()
	for i := 0; i < completionRingLimit+5; i++ {
		ring.Append(SessionCompletion{SessionKey: "s"}, now)
	}
	if len(ring.items) != completionRingLimit {
		t.Fatalf("环上限应为 %d，实际 %d", completionRingLimit, len(ring.items))
	}
	// 超期的旧行也必须丢：TTL 之外的账本对「补发」没有意义，只会把载荷撑大。
	// 只追加的环按时间排，所以老行一定在头部，靠一次新追加触发修剪。
	stale := newCompletionRing()
	stale.Append(SessionCompletion{SessionKey: "old"}, now)
	stale.Append(SessionCompletion{SessionKey: "fresh"}, now.Add(completionRingTTL+time.Minute))
	items, _, _ := stale.Since(0)
	for _, item := range items {
		if item.SessionKey == "old" {
			t.Fatal("超过 TTL 的行没有被清掉")
		}
	}
}

func TestSetPendingReplyMarksSubSession(t *testing.T) {
	hub := NewStreamHub()
	hub.SetPendingReply("root-1", "child-1", "Child")
	items := hub.ListReplyingSessions()
	if len(items) != 1 || !items[0].SubSession {
		t.Fatalf("子会话 attach 置的 pending 必须带 SubSession，得到 %+v", items)
	}
	snapshot := hub.PendingSessionSnapshot("child-1")
	if !snapshot.SubSession {
		t.Fatal("PendingSessionSnapshot 必须把 SubSession 带出去（Web Push 靠它静默）")
	}

	hub.SetPendingUserAt("root-1", "top-1", "Top", "claude", "m", "M", "mode", "effort", "fast", false, "hi", time.Now().UTC())
	for _, item := range hub.ListReplyingSessions() {
		if item.SessionKey == "top-1" && item.SubSession {
			t.Fatal("顶层用户留言不能被子会话标记污染")
		}
	}
}

func TestRecordCompletionSkipsRootlessTurns(t *testing.T) {
	hub := NewStreamHub()
	// 没有 RootID ⇒ 这条会话从没出现在 replying 列表里，记进账本只会产生空提醒。
	hub.RecordCompletion(PendingSessionSnapshot{SessionTitle: "x"}, "s-x")
	if hub.HasCompletionsSince(0) {
		t.Fatal("没有 RootID 的回合不该入账")
	}
	hub.RecordCompletion(PendingSessionSnapshot{RootID: "root-1"}, "s-y")
	if !hub.HasCompletionsSince(0) {
		t.Fatal("正常回合必须入账")
	}
}

func TestShouldNotifySessionDoneSkipsSubSessions(t *testing.T) {
	if shouldNotifySessionDone("req-1", PendingSessionSnapshot{SubSession: true}) {
		t.Fatal("子会话完成不该推通知")
	}
	if !shouldNotifySessionDone("req-1", PendingSessionSnapshot{}) {
		t.Fatal("顶层会话完成必须推通知")
	}
	if shouldNotifySessionDone("scheduled:1", PendingSessionSnapshot{}) {
		t.Fatal("定时任务轮次仍按原规则静默")
	}
}

func TestShouldNotifySessionDoneSkipsSilentCompletions(t *testing.T) {
	if shouldNotifySessionDone("req-1", PendingSessionSnapshot{SilentCompletion: true}) {
		t.Fatal("工作流节点回合（silent）完成不该推通知")
	}
	if !shouldNotifySessionDone("req-1", PendingSessionSnapshot{SilentCompletion: false}) {
		t.Fatal("普通回合完成必须推通知")
	}
}

func TestMarkPendingCompletionSilentBindsToCurrentTurn(t *testing.T) {
	hub := NewStreamHub()
	_, turnID := hub.SetPendingUserAt("root-1", "sess-1", "S", "claude", "m", "M", "mode", "effort", "fast", false, "hi", time.Now().UTC())

	hub.MarkPendingCompletionSilent("sess-1", "another-turn")
	if snapshot := hub.PendingSessionSnapshot("sess-1"); snapshot.SilentCompletion {
		t.Fatal("不是当前回合的 silent 标记必须被拒绝")
	}

	hub.MarkPendingCompletionSilent("sess-1", turnID)
	if snapshot := hub.PendingSessionSnapshot("sess-1"); !snapshot.SilentCompletion {
		t.Fatal("当前回合的 silent 标记必须落到 pending 上")
	}
	if items := hub.ListReplyingSessions(); len(items) != 1 || !items[0].Silent {
		t.Fatalf("ListReplyingSessions 必须把 silent 带出去（长轮询客户端靠它静默），得到 %+v", items)
	}
}

func TestBuildSessionDoneResponseCarriesSilentFlag(t *testing.T) {
	silent := buildSessionDoneResponse("root-1", "sess-1", "req-1", true)
	if silent.Payload["silent"] != true {
		t.Fatalf("silent 回合的 done 帧必须带 silent=true，得到 %#v", silent.Payload)
	}
	loud := buildSessionDoneResponse("root-1", "sess-1", "req-1", false)
	if _, ok := loud.Payload["silent"]; ok {
		t.Fatalf("普通回合的 done 帧不应带 silent 键，得到 %#v", loud.Payload)
	}
}

func TestReplyingSessionsPayloadExposesSubSessionFlag(t *testing.T) {
	h, hub := newReplyingWaitTestHandler(t)
	hub.SetPendingReply("root-1", "child-1", "Child")
	hub.SetPendingUserAt("root-1", "top-1", "Top", "claude", "m", "M", "mode", "effort", "fast", false, "hi", time.Now().UTC())

	w := httptest.NewRecorder()
	h.handleReplyingSessions(w, httptest.NewRequest(http.MethodGet, "/api/replying-sessions", nil))
	body := w.Body.String()
	if !strings.Contains(body, `"sessionKey":"child-1"`) || !strings.Contains(body, `"subSession":true`) {
		t.Fatalf("子会话必须在载荷里带 subSession=true，得到 %s", body)
	}
	if strings.Count(body, `"subSession":true`) != 1 {
		t.Fatalf("顶层会话不应带 subSession，得到 %s", body)
	}
}

func TestReplyingSessionsWaitSeedsCompletionCursorWithoutBacklog(t *testing.T) {
	h, hub := newReplyingWaitTestHandler(t)
	hub.RecordCompletion(PendingSessionSnapshot{RootID: "root-1", SessionTitle: "A"}, "s-a")

	w := httptest.NewRecorder()
	h.handleReplyingSessionsWait(w, httptest.NewRequest(http.MethodGet, "/api/replying-sessions/wait?timeout=5", nil))
	body := w.Body.String()
	if !strings.Contains(body, `"completedSeq":1`) {
		t.Fatalf("首帧必须回游标供播种，得到 %s", body)
	}
	if strings.Contains(body, `"completed"`) {
		t.Fatalf("首帧不该回历史完成（新装客户端会被旧完成刷屏），得到 %s", body)
	}
}

func TestReplyingSessionsWaitReplaysBacklogWhenCursorGiven(t *testing.T) {
	h, hub := newReplyingWaitTestHandler(t)
	hub.RecordCompletion(PendingSessionSnapshot{RootID: "root-1", SessionTitle: "A", Summary: "done"}, "s-a")

	w := httptest.NewRecorder()
	h.handleReplyingSessionsWait(w, httptest.NewRequest(http.MethodGet, "/api/replying-sessions/wait?timeout=5&completed_since=0", nil))
	body := w.Body.String()
	if !strings.Contains(body, `"sessionKey":"s-a"`) || !strings.Contains(body, `"summary":"done"`) {
		t.Fatalf("带游标时必须补发历史完成，得到 %s", body)
	}
}

func TestReplyingSessionsWaitWakesOnNewCompletion(t *testing.T) {
	h, hub := newReplyingWaitTestHandler(t)
	replyingWaitTuning(t, 10*time.Millisecond, time.Second, 5*time.Second, 20*time.Second, 30*time.Second)
	// 形状完全不动：pending 表里没有任何会话，只有完成账本会变。
	// 先播种 ETag —— 不带 If-None-Match 的请求走的是「首帧立即返回」，根本不进等待循环。
	etag := seedReplyingWaitETag(t, h)
	req := httptest.NewRequest(http.MethodGet, "/api/replying-sessions/wait?timeout=5&completed_since=0", nil)
	req.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.handleReplyingSessionsWait(w, req)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	hub.RecordCompletion(PendingSessionSnapshot{RootID: "root-1", SessionTitle: "A", Summary: "finished"}, "s-done")

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("新完成应在 1s 内唤醒等待（形状不变也要补发）")
	}
	if !strings.Contains(w.Body.String(), `"sessionKey":"s-done"`) {
		t.Fatalf("唤醒后必须带回补发行，得到 %s", w.Body.String())
	}
}
