package controlsync

import (
	"context"
	"encoding/json"
	"testing"

	"mindfs/server/internal/nodes"
)

func newTestNodeStore(t *testing.T) *nodes.Store {
	t.Helper()
	store, err := nodes.NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatalf("nodes.NewStoreAt: %v", err)
	}
	return store
}

func TestNodeBaseKeyNormalizesProtocolHostAndPrefix(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:7331":                       "http://127.0.0.1:7331",
		"HTTP://Home.Local:7331/":                     "http://home.local:7331",
		"https://pc.xiaokubao.space/mindfs":           "https://pc.xiaokubao.space/mindfs",
		"https://pc.xiaokubao.space/mindfs/":          "https://pc.xiaokubao.space/mindfs",
		"https://fn.xiaokubao.space/mindfs/?x=1#frag": "https://fn.xiaokubao.space/mindfs",
	}
	for in, want := range cases {
		if got := NodeBaseKey(in); got != want {
			t.Errorf("NodeBaseKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNodesDomainSnapshotIsStable(t *testing.T) {
	store := newTestNodeStore(t)
	if err := store.Replace([]nodes.NodeConnection{
		{ID: "home", Name: "home", URL: "http://127.0.0.1:7331"},
		{ID: "fn", Name: "fn", URL: "https://fn.xiaokubao.space/mindfs", Remote: true},
	}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	domain := NewNodesDomain(store)
	first, err := domain.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	second, err := domain.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot 二次: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("records = %d, want 2", len(first))
	}
	if !sameRecordHashes(first, second) {
		t.Fatalf("两次快照 hash 不同：%v vs %v", recordKeyHashes(first), recordKeyHashes(second))
	}
	keys := recordKeys(first)
	want := []string{"http://127.0.0.1:7331", "https://fn.xiaokubao.space/mindfs"}
	if !sameStrings(keys, want) {
		t.Fatalf("keys = %v, want %v（不按 id 合并）", keys, want)
	}
}

func TestNodesDomainApplyAndTombstone(t *testing.T) {
	source := newTestNodeStore(t)
	if err := source.Replace([]nodes.NodeConnection{{ID: "home", Name: "home", URL: "http://127.0.0.1:7331"}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	recs, err := NewNodesDomain(source).Snapshot(context.Background())
	if err != nil || len(recs) != 1 {
		t.Fatalf("Snapshot = %v, %v", recs, err)
	}

	target := newTestNodeStore(t)
	domain := NewNodesDomain(target)
	if err := domain.Apply(context.Background(), recs[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := domain.Apply(context.Background(), recs[0]); err != nil {
		t.Fatalf("Apply 幂等: %v", err)
	}
	list := target.List()
	if len(list) != 1 || list[0].ID != "home" || list[0].URL != "http://127.0.0.1:7331" {
		t.Fatalf("目标节点表 = %+v", list)
	}

	dead := recs[0]
	dead.Deleted = true
	dead.Payload = nil
	dead.Hash, err = HashRecord(dead)
	if err != nil {
		t.Fatalf("HashRecord: %v", err)
	}
	if err := domain.Apply(context.Background(), dead); err != nil {
		t.Fatalf("Apply tombstone: %v", err)
	}
	if list := target.List(); len(list) != 0 {
		t.Fatalf("墓碑之后节点仍在：%+v", list)
	}
}

func TestNodesDomainRejectsKeyPayloadMismatch(t *testing.T) {
	store := newTestNodeStore(t)
	domain := NewNodesDomain(store)
	payload := nodes.NodeConnection{ID: "fn", Name: "fn", URL: "https://fn.xiaokubao.space/mindfs"}
	rec, err := newRecord(DomainNodes, "http://127.0.0.1:7331", payload)
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	if err := domain.Apply(context.Background(), rec); err == nil {
		t.Fatalf("key 与 payload 的 URL 不一致居然被接受：两台不同机器会被合并成一条")
	}
	if list := store.List(); len(list) != 0 {
		t.Fatalf("被拒的记录仍然写进了存储：%+v", list)
	}
}

func TestNodesDomainRejectsInvalidPayload(t *testing.T) {
	store := newTestNodeStore(t)
	domain := NewNodesDomain(store)
	rec, err := newRecord(DomainNodes, "http://127.0.0.1:7331", map[string]any{"id": "home"})
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	if err := domain.Apply(context.Background(), rec); err == nil {
		t.Fatalf("缺 name/url 的节点被接受：store.Replace 会静默丢掉它，同步就无声丢数据")
	}
	if list := store.List(); len(list) != 0 {
		t.Fatalf("非法记录写进了存储：%+v", list)
	}
}

func TestRegistryRejectsDuplicateAndUnknownDomains(t *testing.T) {
	store := newTestNodeStore(t)
	if _, err := NewRegistry(NewNodesDomain(store), NewNodesDomain(store)); err == nil {
		t.Fatalf("重复域名没被拒绝")
	}
	reg, err := NewRegistry(NewNodesDomain(store))
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, err := reg.Get("users"); err == nil {
		t.Fatalf("未注册的域没报错")
	}
	if got := reg.Names(); len(got) != 1 || got[0] != DomainNodes {
		t.Fatalf("Names = %v", got)
	}
}

func TestNewRecordIsCanonicalAcrossKeyOrder(t *testing.T) {
	a, err := newRecord(DomainNodes, "k", map[string]any{"b": 2, "a": 1})
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	b, err := newRecord(DomainNodes, "k", json.RawMessage(`{"b":2,"a":1}`))
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	if a.Hash != b.Hash {
		t.Fatalf("同一对象换一种书写方式就换了 hash：%s vs %s", a.Hash, b.Hash)
	}
}

func recordKeys(recs []Record) []string {
	out := make([]string, 0, len(recs))
	for _, rec := range recs {
		out = append(out, rec.Key)
	}
	return out
}

func recordKeyHashes(recs []Record) map[string]string {
	out := make(map[string]string, len(recs))
	for _, rec := range recs {
		out[rec.Domain+"/"+rec.Key] = rec.Hash
	}
	return out
}

func sameRecordHashes(a, b []Record) bool {
	if len(a) != len(b) {
		return false
	}
	ha, hb := recordKeyHashes(a), recordKeyHashes(b)
	for k, v := range ha {
		if hb[k] != v {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
