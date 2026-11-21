package controlsync

import (
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// writeTempCA 把 httptest TLS 服务的自签证书写成 PEM 文件，模拟配置里的 ca_file。
func writeTempCA(t *testing.T, cert *x509.Certificate) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写 ca_file: %v", err)
	}
	return path
}

// fakePrimary 提供 GET /api/control-sync/changes 的最小实现：按 since 切片返回。
type fakePrimary struct {
	mu       sync.Mutex
	changes  []RemoteChange
	fail     bool
	requests []string
}

func (f *fakePrimary) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/control-sync/changes" {
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, r.URL.Query().Get("since"))
		fail := f.fail
		changes := append([]RemoteChange(nil), f.changes...)
		f.mu.Unlock()
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		since, limit := 0, 500
		_, _ = fmt.Sscanf(r.URL.Query().Get("since"), "%d", &since)
		if raw := r.URL.Query().Get("limit"); raw != "" {
			_, _ = fmt.Sscanf(raw, "%d", &limit)
		}
		var page []RemoteChange
		var head int64
		for _, ch := range changes {
			if ch.Rev > head {
				head = ch.Rev
			}
			if ch.Rev >= int64(since) && len(page) < limit {
				page = append(page, ch)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"changes": page, "head_rev": head})
	})
}

// remoteChange 造一条线上记录。payload 同时决定 hash：墓碑也带最后一次内容的 hash，
// 这样「主节点删了」和「主节点改完又删」在合并阶段仍可区分。
func remoteChange(t *testing.T, rev int64, domain, key, payload string, deleted bool) RemoteChange {
	t.Helper()
	ch := RemoteChange{Domain: domain, Key: key, Rev: rev, Deleted: deleted, Origin: "home"}
	hash, err := HashRecord(Record{Domain: domain, Key: key, Payload: json.RawMessage(payload)})
	if err != nil {
		t.Fatalf("HashRecord: %v", err)
	}
	ch.Hash = hash
	if !deleted {
		ch.Payload = json.RawMessage(payload)
	}
	return ch
}

func newReplicatorFixture(t *testing.T, primary *fakePrimary) (*Store, *Replicator) {
	t.Helper()
	store := openTestStore(t)
	srv := httptest.NewTLSServer(primary.handler())
	t.Cleanup(srv.Close)
	ca := writeTempCA(t, srv.Certificate())
	rep, err := NewReplicator(store, ReplicatorOptions{
		PrimaryURL: srv.URL,
		CAFile:     ca,
		Limit:      2,
	})
	if err != nil {
		t.Fatalf("NewReplicator: %v", err)
	}
	return store, rep
}

// 远端新增 / 修改 / 删除各追平：records 跟上主节点的最终状态，删除落成墓碑。
func TestReplicatorAppliesAddModifyDelete(t *testing.T) {
	primary := &fakePrimary{changes: []RemoteChange{
		remoteChange(t, 1, DomainPreferences, "agents", `{"v":1}`, false),
		remoteChange(t, 2, DomainPreferences, "agents", `{"v":2}`, false),
		remoteChange(t, 3, DomainPreferences, "agents", `{"v":2}`, true),
	}}
	store, rep := newReplicatorFixture(t, primary)
	ctx := t.Context()
	if _, err := rep.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	var deleted int
	var payload sql.NullString
	var hash string
	if err := store.db.QueryRow(`SELECT deleted, payload, hash FROM records WHERE domain = ? AND key = 'agents'`, DomainPreferences).
		Scan(&deleted, &payload, &hash); err != nil {
		t.Fatalf("读副本: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("删除没有追平成墓碑: deleted = %d", deleted)
	}
	if payload.Valid {
		t.Fatalf("墓碑仍然带着内容: %q", payload.String)
	}
	want, err := HashRecord(Record{Domain: DomainPreferences, Key: "agents", Payload: json.RawMessage(`{"v":2}`)})
	if err != nil {
		t.Fatalf("HashRecord: %v", err)
	}
	if hash != want {
		t.Fatalf("墓碑 hash = %s，应保留最后一次内容的 hash %s", hash, want)
	}
	if rev, err := store.BaseRev(ctx); err != nil || rev != 3 {
		t.Fatalf("base_rev = %d, %v，应追到 3", rev, err)
	}
}

// 拉取失败不推进水位：下一次从同一处续传，不会丢一条变更。
func TestReplicatorKeepsWatermarkOnFailure(t *testing.T) {
	primary := &fakePrimary{
		changes: []RemoteChange{remoteChange(t, 1, DomainPreferences, "agents", `{"v":1}`, false)},
		fail:    true,
	}
	store, rep := newReplicatorFixture(t, primary)
	ctx := t.Context()
	if _, err := rep.SyncOnce(ctx); err == nil {
		t.Fatalf("主节点 500 时 SyncOnce 居然成功了")
	}
	if rev, err := store.BaseRev(ctx); err != nil || rev != 0 {
		t.Fatalf("失败后 base_rev = %d, %v，应保持 0", rev, err)
	}
	var rows int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM records`).Scan(&rows); err != nil {
		t.Fatalf("count records: %v", err)
	}
	if rows != 0 {
		t.Fatalf("失败后副本里留下 %d 行：事务没回滚", rows)
	}
	// 主节点恢复后从 since=1 续传，之前的变更不丢。
	primary.mu.Lock()
	primary.fail = false
	primary.mu.Unlock()
	if _, err := rep.SyncOnce(ctx); err != nil {
		t.Fatalf("恢复后 SyncOnce: %v", err)
	}
	if rev, _ := store.BaseRev(ctx); rev != 1 {
		t.Fatalf("恢复后 base_rev = %d，应追到 1", rev)
	}
}

// 重复拉取幂等：第二次从 since=base_rev 往后问，空页即停；副本不重复写。
func TestReplicatorRepeatedPullIsIdempotent(t *testing.T) {
	primary := &fakePrimary{changes: []RemoteChange{
		remoteChange(t, 1, DomainPreferences, "a", `{"v":1}`, false),
		remoteChange(t, 2, DomainPreferences, "b", `{"v":2}`, false),
	}}
	store, rep := newReplicatorFixture(t, primary)
	ctx := t.Context()
	if _, err := rep.SyncOnce(ctx); err != nil {
		t.Fatalf("首次 SyncOnce: %v", err)
	}
	firstReqs := len(primary.requests)
	if _, err := rep.SyncOnce(ctx); err != nil {
		t.Fatalf("第二次 SyncOnce: %v", err)
	}
	primary.mu.Lock()
	reqs := append([]string(nil), primary.requests[firstReqs:]...)
	primary.mu.Unlock()
	if len(reqs) != 1 || reqs[0] != "3" {
		t.Fatalf("第二次拉取的 since = %v，应只问一次 since=3", reqs)
	}
	if rev, _ := store.BaseRev(ctx); rev != 2 {
		t.Fatalf("base_rev = %d，重复拉取不得推进", rev)
	}
}

// 分页：每页 limit 条，逐页推进 until 空页。
func TestReplicatorPaginatesUntilEmptyPage(t *testing.T) {
	primary := &fakePrimary{}
	for i := int64(1); i <= 5; i++ {
		primary.changes = append(primary.changes,
			remoteChange(t, i, DomainPreferences, fmt.Sprintf("k%d", i), `{"v":true}`, false))
	}
	store, rep := newReplicatorFixture(t, primary)
	if _, err := rep.SyncOnce(t.Context()); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	var rows int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM records`).Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 5 {
		t.Fatalf("分页后副本 %d 行，应为 5", rows)
	}
	primary.mu.Lock()
	seq := strings.Join(primary.requests, ",")
	primary.mu.Unlock()
	if seq != "1,3,5,6" {
		t.Fatalf("since 序列 = %s，应为 1,3,5,6", seq)
	}
	if rev, _ := store.BaseRev(t.Context()); rev != 5 {
		t.Fatalf("base_rev = %d，应为 5", rev)
	}
}

// 重放旧页不得把副本倒回旧内容：水位以内的记录一律跳过，而不是照抄。
func TestApplyRemoteChangesIgnoresReplayedOldPage(t *testing.T) {
	store := openTestStore(t)
	ctx := t.Context()
	old := remoteChange(t, 1, DomainPreferences, "agents", `{"v":1}`, false)
	newer := remoteChange(t, 2, DomainPreferences, "agents", `{"v":2}`, false)
	if err := store.ApplyRemoteChanges(ctx, []RemoteChange{old, newer}); err != nil {
		t.Fatalf("ApplyRemoteChanges: %v", err)
	}
	if err := store.ApplyRemoteChanges(ctx, []RemoteChange{old}); err != nil {
		t.Fatalf("重放旧页: %v", err)
	}
	var payload, hash string
	if err := store.db.QueryRow(`SELECT payload, hash FROM records WHERE domain = ? AND key = 'agents'`, DomainPreferences).
		Scan(&payload, &hash); err != nil {
		t.Fatalf("读副本: %v", err)
	}
	if payload != `{"v":2}` || hash != newer.Hash {
		t.Fatalf("重放旧页把副本倒回了 %s（hash %s）", payload, hash)
	}
	if rev, _ := store.BaseRev(ctx); rev != 2 {
		t.Fatalf("重放旧页后 base_rev = %d，应保持 2", rev)
	}
}

func TestNewReplicatorRejectsPlainHTTP(t *testing.T) {
	store := openTestStore(t)
	if _, err := NewReplicator(store, ReplicatorOptions{PrimaryURL: "http://home:7331"}); err == nil {
		t.Fatalf("red 接受了明文 primary_url")
	}
}
