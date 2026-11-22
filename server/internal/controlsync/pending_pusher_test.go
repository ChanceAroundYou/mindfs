package controlsync

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// fakePushSender 让 PendingPusher 的用例不碰网络：记下上行请求，按测试给的
// 函数回执。handler 在 PushOnce 的同一 goroutine 里跑，可以模拟「推送期间本地又改了」。
type fakePushSender struct {
	mu       sync.Mutex
	requests []PushRequest
	handler  func(PushRequest) (PushBatch, error)
}

func (f *fakePushSender) Push(ctx context.Context, req PushRequest) (PushBatch, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	handler := f.handler
	f.mu.Unlock()
	if handler == nil {
		return PushBatch{}, nil
	}
	return handler(req)
}

// setHandler 换掉回执行为（用例里模拟「主节点恢复后开始接受」）。
func (f *fakePushSender) setHandler(handler func(PushRequest) (PushBatch, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = handler
}

func (f *fakePushSender) lastRequest(t *testing.T) PushRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatalf("没有发生推送")
	}
	return f.requests[len(f.requests)-1]
}

func newPendingPusherFixture(t *testing.T, handler func(PushRequest) (PushBatch, error)) (*Store, *fakePushSender, *PendingPusher) {
	t.Helper()
	store := pendingFixture(t)
	sender := &fakePushSender{handler: handler}
	pusher, err := NewPendingPusher(store, sender, PendingPusherOptions{
		InstanceID: "edge-pc",
		BatchSize:  50,
	})
	if err != nil {
		t.Fatalf("NewPendingPusher: %v", err)
	}
	return store, sender, pusher
}

func seedPending(t *testing.T, store *Store, changeID, domain, key, payload string) {
	t.Helper()
	if err := store.PutPending(t.Context(), PendingChange{
		ChangeID: changeID,
		Domain:   domain,
		Key:      key,
		BaseHash: "sha256:ancestor",
		Payload:  json.RawMessage(payload),
	}); err != nil {
		t.Fatalf("PutPending: %v", err)
	}
}

func appliedResult(req PushRequest, hash string) PushBatch {
	batch := PushBatch{Results: make([]PushResult, 0, len(req.Changes))}
	for _, ch := range req.Changes {
		batch.Results = append(batch.Results, PushResult{
			ChangeID: ch.ChangeID, Status: PushApplied, Exists: true, Hash: hash,
		})
	}
	return batch
}

func TestPushOnceDeletesPendingWhenPrimaryApplies(t *testing.T) {
	var sawRequest PushRequest
	store, sender, pusher := newPendingPusherFixture(t, func(req PushRequest) (PushBatch, error) {
		sawRequest = req
		return appliedResult(req, "sha256:applied"), nil
	})
	ctx := t.Context()
	seedPending(t, store, "edge-pc:preferences:agents:1", DomainPreferences, "agents", `{"v":1}`)

	stats, err := pusher.PushOnce(ctx)
	if err != nil {
		t.Fatalf("PushOnce: %v", err)
	}
	if stats.Pushed != 1 || stats.Applied != 1 || stats.Conflicts != 0 || stats.Rejected != 0 {
		t.Fatalf("统计 = %+v", stats)
	}
	if n, err := store.CountPending(ctx); err != nil || n != 0 {
		t.Fatalf("pending = %d, %v，主节点接受后应清空", n, err)
	}
	if sawRequest.InstanceID != "edge-pc" {
		t.Fatalf("instance_id = %q", sawRequest.InstanceID)
	}
	if len(sawRequest.Changes) != 1 {
		t.Fatalf("上行条数 = %d", len(sawRequest.Changes))
	}
	ch := sawRequest.Changes[0]
	if ch.ChangeID != "edge-pc:preferences:agents:1" || ch.BaseHash != "sha256:ancestor" ||
		string(ch.Payload) != `{"v":1}` || ch.Deleted {
		t.Fatalf("上行变更 = %+v", ch)
	}
	_ = sender
}

// 推送期间本地又改了一版：回执只允许删掉推上去的那一条，新意图要留在库里，
// 共同祖先重定到主节点刚接受的值，下一拍才不会又判一次冲突。
func TestPushOnceKeepsNewerPendingAndRebasesAncestor(t *testing.T) {
	store := pendingFixture(t)
	sender := &fakePushSender{handler: func(req PushRequest) (PushBatch, error) {
		if err := store.PutPending(t.Context(), PendingChange{
			ChangeID: "edge-pc:preferences:agents:2",
			Domain:   DomainPreferences,
			Key:      "agents",
			BaseHash: "sha256:ancestor",
			Payload:  json.RawMessage(`{"v":2}`),
		}); err != nil {
			t.Fatalf("推送期间本地再改: %v", err)
		}
		return appliedResult(req, "sha256:applied"), nil
	}}
	pusher, err := NewPendingPusher(store, sender, PendingPusherOptions{InstanceID: "edge-pc"})
	if err != nil {
		t.Fatalf("NewPendingPusher: %v", err)
	}
	ctx := t.Context()
	seedPending(t, store, "edge-pc:preferences:agents:1", DomainPreferences, "agents", `{"v":1}`)

	stats, err := pusher.PushOnce(ctx)
	if err != nil {
		t.Fatalf("PushOnce: %v", err)
	}
	if stats.Applied != 1 {
		t.Fatalf("统计 = %+v", stats)
	}
	p, ok, err := store.PendingChange(ctx, DomainPreferences, "agents")
	if err != nil || !ok {
		t.Fatalf("pending 不该被旧回执删掉：ok=%v err=%v", ok, err)
	}
	if p.ChangeID != "edge-pc:preferences:agents:2" || string(p.Payload) != `{"v":2}` {
		t.Fatalf("留下的不是新意图：%+v", p)
	}
	if p.BaseHash != "sha256:applied" {
		t.Fatalf("base_hash = %q，应重定到刚被接受的值", p.BaseHash)
	}
}

// 主节点判冲突：pending 原样保留，本地建一张边缘视角的卡（local = 自己、remote = 主节点）。
func TestPushOnceRecordsConflictWithPrimarySide(t *testing.T) {
	store, _, pusher := newPendingPusherFixture(t, func(req PushRequest) (PushBatch, error) {
		batch := PushBatch{Results: make([]PushResult, 0, len(req.Changes))}
		for _, ch := range req.Changes {
			batch.Results = append(batch.Results, PushResult{
				ChangeID: ch.ChangeID, Status: PushConflict, ConflictID: "primary-side-id",
				Exists: true, Hash: recordHash(t, DomainPreferences, "agents", `{"v":"primary"}`),
				Payload: json.RawMessage(`{"v":"primary"}`),
			})
		}
		return batch, nil
	})
	ctx := t.Context()
	seedPending(t, store, "edge-pc:preferences:agents:7", DomainPreferences, "agents", `{"v":"edge"}`)

	stats, err := pusher.PushOnce(ctx)
	if err != nil {
		t.Fatalf("PushOnce: %v", err)
	}
	if stats.Conflicts != 1 {
		t.Fatalf("统计 = %+v", stats)
	}
	if p, ok, _ := store.PendingChange(ctx, DomainPreferences, "agents"); !ok || p.ChangeID != "edge-pc:preferences:agents:7" {
		t.Fatalf("冲突时 pending 必须保留：ok=%v %+v", ok, p)
	}
	open, err := store.ListOpenConflicts(ctx)
	if err != nil || len(open) != 1 {
		t.Fatalf("未裁决冲突 = %d, %v", len(open), err)
	}
	c := open[0]
	if c.ID != PullConflictID(DomainPreferences, "agents") {
		t.Fatalf("冲突 id = %q，推与拉必须指向同一张卡", c.ID)
	}
	if string(c.LocalPayload) != `{"v":"edge"}` || c.LocalDeleted {
		t.Fatalf("local 侧 = %s deleted=%v，应是本机离线那一版", c.LocalPayload, c.LocalDeleted)
	}
	if c.LocalHash == "" || c.LocalHash == c.RemoteHash {
		t.Fatalf("local hash = %q, remote = %q，两侧内容不同，hash 必须分开", c.LocalHash, c.RemoteHash)
	}
	if string(c.RemotePayload) != `{"v":"primary"}` || c.RemoteDeleted {
		t.Fatalf("remote 侧 = %s deleted=%v，应是主节点当前值", c.RemotePayload, c.RemoteDeleted)
	}
	if c.RemoteHash != recordHash(t, DomainPreferences, "agents", `{"v":"primary"}`) {
		t.Fatalf("remote hash = %q", c.RemoteHash)
	}
}

// 主节点说这条记录已经不存在：冲突卡的 remote 侧必须标成删除，不能拿空载荷冒充。
func TestPushOnceRecordsConflictForDeletedPrimary(t *testing.T) {
	store, _, pusher := newPendingPusherFixture(t, func(req PushRequest) (PushBatch, error) {
		batch := PushBatch{Results: make([]PushResult, 0, len(req.Changes))}
		for _, ch := range req.Changes {
			batch.Results = append(batch.Results, PushResult{
				ChangeID: ch.ChangeID, Status: PushConflict, ConflictID: "id", Exists: false,
			})
		}
		return batch, nil
	})
	ctx := t.Context()
	seedPending(t, store, "edge-pc:pins:s1:3", DomainPins, "s1", `{"pinned":true}`)
	if _, err := pusher.PushOnce(ctx); err != nil {
		t.Fatalf("PushOnce: %v", err)
	}
	open, err := store.ListOpenConflicts(ctx)
	if err != nil || len(open) != 1 {
		t.Fatalf("未裁决冲突 = %d, %v", len(open), err)
	}
	if !open[0].RemoteDeleted {
		t.Fatalf("主节点没有这条记录，冲突卡 remote 侧却不算删除")
	}
}

func TestPushOnceKeepsPendingWhenRejected(t *testing.T) {
	var reported []error
	store := pendingFixture(t)
	sender := &fakePushSender{handler: func(req PushRequest) (PushBatch, error) {
		batch := PushBatch{Results: make([]PushResult, 0, len(req.Changes))}
		for _, ch := range req.Changes {
			batch.Results = append(batch.Results, PushResult{
				ChangeID: ch.ChangeID, Status: PushRejected, Reason: "bad payload",
			})
		}
		return batch, nil
	}}
	pusher, err := NewPendingPusher(store, sender, PendingPusherOptions{
		InstanceID: "edge-pc",
		OnError:    func(err error) { reported = append(reported, err) },
	})
	if err != nil {
		t.Fatalf("NewPendingPusher: %v", err)
	}
	ctx := t.Context()
	seedPending(t, store, "edge-pc:pins:s1:1", DomainPins, "s1", `{"pinned":true}`)

	stats, err := pusher.PushOnce(ctx)
	if err != nil {
		t.Fatalf("单条被拒不该中断整轮: %v", err)
	}
	if stats.Rejected != 1 || stats.Applied != 0 {
		t.Fatalf("统计 = %+v", stats)
	}
	if n, _ := store.CountPending(ctx); n != 1 {
		t.Fatalf("被拒的 pending 不该删：%d", n)
	}
	if len(reported) != 1 || reported[0] == nil {
		t.Fatalf("被拒应上报一次: %v", reported)
	}
}

// 回执少一条就不能猜它成功了：整拍报错，pending 一条不动。
func TestPushOnceFailsWhenResultMissing(t *testing.T) {
	store := pendingFixture(t)
	sender := &fakePushSender{handler: func(req PushRequest) (PushBatch, error) {
		return PushBatch{Results: []PushResult{{
			ChangeID: req.Changes[0].ChangeID, Status: PushApplied, Exists: true, Hash: "sha256:x",
		}}}, nil
	}}
	pusher, err := NewPendingPusher(store, sender, PendingPusherOptions{InstanceID: "edge-pc"})
	if err != nil {
		t.Fatalf("NewPendingPusher: %v", err)
	}
	ctx := t.Context()
	seedPending(t, store, "edge-pc:pins:s1:1", DomainPins, "s1", `{"pinned":true}`)
	seedPending(t, store, "edge-pc:pins:s2:2", DomainPins, "s2", `{"pinned":false}`)

	if _, err := pusher.PushOnce(ctx); err == nil {
		t.Fatalf("缺一条回执居然没报错")
	}
	if n, _ := store.CountPending(ctx); n != 2 {
		t.Fatalf("pending = %d，回执不全时不该删任何一条", n)
	}
}

// 冲突按域冻结：冻结域里的 pending 一律不推，其他域照常推。冻结不是「整条通道停摆」，
// 否则一个 pin 的冲突会把所有账户偏好一起卡住。
func TestPushOnceSkipsFrozenDomains(t *testing.T) {
	var pushed []PushChange
	store := pendingFixture(t)
	sender := &fakePushSender{handler: func(req PushRequest) (PushBatch, error) {
		pushed = append(pushed, req.Changes...)
		return appliedResult(req, "sha256:applied"), nil
	}}
	pusher, err := NewPendingPusher(store, sender, PendingPusherOptions{InstanceID: "edge-pc"})
	if err != nil {
		t.Fatalf("NewPendingPusher: %v", err)
	}
	ctx := t.Context()
	seedPending(t, store, "edge-pc:pins:s1:1", DomainPins, "s1", `{"pinned":true}`)
	seedPending(t, store, "edge-pc:preferences:agents:2", DomainPreferences, "agents", `{"v":1}`)
	pusher.SetFrozenDomains(map[string]bool{DomainPins: true})

	stats, err := pusher.PushOnce(ctx)
	if err != nil {
		t.Fatalf("PushOnce: %v", err)
	}
	if len(pushed) != 1 || pushed[0].Domain != DomainPreferences {
		t.Fatalf("上行 = %+v，冻结域的 pending 不该被推", pushed)
	}
	if stats.Applied != 1 || stats.Pushed != 1 {
		t.Fatalf("统计 = %+v", stats)
	}
	if p, ok, _ := store.PendingChange(ctx, DomainPins, "s1"); !ok || string(p.Payload) != `{"pinned":true}` {
		t.Fatalf("冻结域的 pending 应原样保留：ok=%v %+v", ok, p)
	}
}

func TestPushOnceLeavesPendingWhenNetworkFails(t *testing.T) {
	store := pendingFixture(t)
	sender := &fakePushSender{handler: func(PushRequest) (PushBatch, error) {
		return PushBatch{}, errors.New("dial tcp: connection refused")
	}}
	pusher, err := NewPendingPusher(store, sender, PendingPusherOptions{InstanceID: "edge-pc"})
	if err != nil {
		t.Fatalf("NewPendingPusher: %v", err)
	}
	ctx := t.Context()
	seedPending(t, store, "edge-pc:pins:s1:1", DomainPins, "s1", `{"pinned":true}`)
	if _, err := pusher.PushOnce(ctx); err == nil {
		t.Fatalf("网络失败居然没报错")
	}
	if n, _ := store.CountPending(ctx); n != 1 {
		t.Fatalf("网络失败时 pending 应原样保留，现在 %d", n)
	}
}
