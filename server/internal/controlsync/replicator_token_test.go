package controlsync

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 主节点的变更流要鉴权：不给出 token 拿不到任何数据，而且错误必须是 401 而不是
// 「空页」——把 401 当空页会让副本永远停在旧值、却显示已追平。
func TestReplicatorSendsSyncToken(t *testing.T) {
	const token = "replicator-token-012345"
	primary := &fakePrimary{changes: []RemoteChange{
		remoteChange(t, 1, DomainPreferences, "agents", `{"v":1}`, false),
	}}
	store := openTestStore(t)
	var sawAuth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		if !AuthorizeSyncRequest(token, sawAuth) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		primary.handler().ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	ca := writeTempCA(t, srv.Certificate())

	noToken, err := NewReplicator(store, ReplicatorOptions{PrimaryURL: srv.URL, CAFile: ca})
	if err != nil {
		t.Fatalf("NewReplicator: %v", err)
	}
	if _, err := noToken.SyncOnce(t.Context()); err == nil {
		t.Fatalf("没有 token 时 SyncOnce 居然成功")
	}

	rep, err := NewReplicator(store, ReplicatorOptions{PrimaryURL: srv.URL, CAFile: ca, Token: token})
	if err != nil {
		t.Fatalf("NewReplicator(token): %v", err)
	}
	if _, err := rep.SyncOnce(t.Context()); err != nil {
		t.Fatalf("带 token 的 SyncOnce: %v", err)
	}
	if sawAuth != "Bearer "+token {
		t.Fatalf("请求头 = %q", sawAuth)
	}
}
