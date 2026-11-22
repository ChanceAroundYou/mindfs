package controlsync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// memDomain 是最小的域实现：一张内存表，不碰任何文件。
// 它让采集逻辑的用例不依赖六个真实 store 的落盘细节。
type memDomain struct {
	name  string
	mu    sync.Mutex
	items map[string]json.RawMessage
	// applyErr 让用例模拟「写权威存储失败」（磁盘满、权限、落盘一半）。
	applyErr error
}

func newMemDomain(name string) *memDomain {
	return &memDomain{name: name, items: map[string]json.RawMessage{}}
}

func (d *memDomain) Name() string { return d.name }

func (d *memDomain) Snapshot(ctx context.Context) ([]Record, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	keys := make([]string, 0, len(d.items))
	for key := range d.items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]Record, 0, len(keys))
	for _, key := range keys {
		rec, err := newRecord(d.name, key, d.items[key])
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

func (d *memDomain) Apply(ctx context.Context, rec Record) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.applyErr != nil {
		return d.applyErr
	}
	if rec.Deleted {
		delete(d.items, rec.Key)
		return nil
	}
	d.items[rec.Key] = rec.Payload
	return nil
}

// set / remove 模拟「本地写入」：绕过 HTTP，也不经过 Apply。
func (d *memDomain) set(key, payload string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.items[key] = json.RawMessage(payload)
}

func (d *memDomain) remove(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.items, key)
}

func newCaptureFixture(t *testing.T) (*Store, *memDomain, *Capturer) {
	t.Helper()
	store := openTestStore(t)
	domain := newMemDomain(DomainPreferences)
	reg, err := NewRegistry(domain)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	capturer, err := NewCapturer(store, reg, "home")
	if err != nil {
		t.Fatalf("NewCapturer: %v", err)
	}
	return store, domain, capturer
}

func countChanges(t *testing.T, store *Store) int {
	t.Helper()
	var n int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM changes`).Scan(&n); err != nil {
		t.Fatalf("count changes: %v", err)
	}
	return n
}

// 新增 / 修改 / 删除各产生一条 change；删除的墓碑保留最后一次内容的 hash，
// 接收方才分得清「我删的」和「远端改完再删的」是不是同一个起点。
func TestCaptureEmitsOneChangePerMutation(t *testing.T) {
	store, domain, capturer := newCaptureFixture(t)
	ctx := context.Background()
	if n, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil || n != 0 {
		t.Fatalf("空库首次采集 = %d, %v，应为 0", n, err)
	}
	domain.set("a", `{"v":1}`)
	if n, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil || n != 1 {
		t.Fatalf("新增采集 = %d, %v，应为 1", n, err)
	}
	domain.set("a", `{"v":2}`)
	if n, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil || n != 1 {
		t.Fatalf("修改采集 = %d, %v，应为 1", n, err)
	}
	domain.remove("a")
	if n, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil || n != 1 {
		t.Fatalf("删除采集 = %d, %v，应为 1", n, err)
	}
	if got := countChanges(t, store); got != 3 {
		t.Fatalf("changes = %d，三次变更应恰好三条", got)
	}
	var deleted int
	var hash string
	if err := store.db.QueryRow(`SELECT deleted, hash FROM records WHERE domain = ? AND key = 'a'`, DomainPreferences).
		Scan(&deleted, &hash); err != nil {
		t.Fatalf("读墓碑: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("删除后 records.deleted = %d，应为 1", deleted)
	}
	want, err := HashRecord(Record{Domain: DomainPreferences, Key: "a", Payload: json.RawMessage(`{"v":2}`)})
	if err != nil {
		t.Fatalf("HashRecord: %v", err)
	}
	if hash != want {
		t.Fatalf("墓碑 hash = %s，应等于最后一次内容 hash %s", hash, want)
	}
}

// 没有变化就不写 change —— 防抖。这条也是「同一 rev 不重复分配」的守门用例：
// 第二次采集若又写一条，changes 会多出一行、rev 会被再分配一次。
func TestCaptureIsQuietWhenNothingChanged(t *testing.T) {
	store, domain, capturer := newCaptureFixture(t)
	ctx := context.Background()
	domain.set("a", `{"v":1}`)
	if n, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil || n != 1 {
		t.Fatalf("首次采集 = %d, %v", n, err)
	}
	for i := 0; i < 3; i++ {
		if n, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil || n != 0 {
			t.Fatalf("第 %d 次空转采集 = %d, %v，应为 0", i, n, err)
		}
	}
	if got := countChanges(t, store); got != 1 {
		t.Fatalf("changes = %d，空转不得新增变更", got)
	}
	rows, err := store.db.Query(`SELECT rev FROM changes ORDER BY rev`)
	if err != nil {
		t.Fatalf("读 changes: %v", err)
	}
	defer rows.Close()
	var revs []int64
	for rows.Next() {
		var rev int64
		if err := rows.Scan(&rev); err != nil {
			t.Fatalf("scan rev: %v", err)
		}
		revs = append(revs, rev)
	}
	if len(revs) != 1 || revs[0] <= 0 {
		t.Fatalf("revs = %v，应恰好一条正数 rev", revs)
	}
}

// 应用远端记录期间不产生 change：否则边缘把主节点的改动当成本地改动回传，
// 主节点再回传一次，同步通道会自激成死循环。
func TestSuppressKeepsRemoteApplyOutOfChangeLog(t *testing.T) {
	store, domain, capturer := newCaptureFixture(t)
	ctx := context.Background()
	domain.set("a", `{"v":1}`)
	if n, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil || n != 1 {
		t.Fatalf("基线采集 = %d, %v", n, err)
	}
	err := capturer.Suppress(ctx, []string{DomainPreferences}, func() error {
		// 远端记录经 Apply 落进权威存储，对采集器来说「存储变了但不是我写的」。
		if err := domain.Apply(ctx, mustRecord(t, DomainPreferences, "b", `{"v":7}`)); err != nil {
			return err
		}
		return domain.Apply(ctx, mustRecord(t, DomainPreferences, "a", `{"v":9}`))
	})
	if err != nil {
		t.Fatalf("Suppress: %v", err)
	}
	if n, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil || n != 0 {
		t.Fatalf("远端应用后的采集 = %d, %v，应为 0（自激）", n, err)
	}
	if got := countChanges(t, store); got != 1 {
		t.Fatalf("changes = %d，远端应用不得新增变更", got)
	}
}

// 非 HTTP 写入（启动迁移、定时任务）没有中间件可挂，靠定时兜底扫描捕捉。
func TestFallbackScanCatchesNonHTTPWrites(t *testing.T) {
	store, domain, capturer := newCaptureFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		capturer.Run(ctx, 10*time.Millisecond)
	}()
	domain.set("a", `{"v":1}`)
	deadline := time.Now().Add(5 * time.Second)
	for countChanges(t, store) == 0 {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("兜底扫描没有采集到非 HTTP 写入")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
}

// 中间件只在「控制面写请求成功」后采集：GET 不改状态、失败请求不算数、
// 数据面路径不映射到任何域。
func TestWriteCaptureMiddlewareOnlyCapturesControlPlaneWrites(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		path     string
		status   int
		wantHits int
	}{
		{name: "写请求成功", method: http.MethodPut, path: "/api/preferences/cors", status: http.StatusOK, wantHits: 1},
		{name: "GET 不采集", method: http.MethodGet, path: "/api/preferences/cors", status: http.StatusOK, wantHits: 0},
		{name: "失败不采集", method: http.MethodPut, path: "/api/preferences/cors", status: http.StatusInternalServerError, wantHits: 0},
		{name: "数据面不采集", method: http.MethodPost, path: "/api/sessions/fork", status: http.StatusOK, wantHits: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, domain, capturer := newCaptureFixture(t)
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				domain.set("cors", `{"v":true}`)
				w.WriteHeader(tc.status)
			})
			rec := httptest.NewRecorder()
			capturer.WriteCapture(inner).ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if got := countChanges(t, store); got != tc.wantHits {
				t.Fatalf("changes = %d，期望 %d", got, tc.wantHits)
			}
			// 采集不能拖住响应：响应码必须原样透传。
			if rec.Code != tc.status {
				t.Fatalf("响应码 = %d，期望 %d", rec.Code, tc.status)
			}
		})
	}
}

func TestDomainForPath(t *testing.T) {
	cases := []struct {
		path string
		want string
		ok   bool
	}{
		{"/api/users", DomainUsers, true},
		{"/api/users/u_abc/primary", DomainUsers, true},
		{"/api/preferences", DomainPreferences, true},
		{"/api/preferences/cors", DomainPreferences, true},
		{"/api/nodes", DomainNodes, true},
		{"/api/nodes/n1", DomainNodes, true},
		{"/api/prompts", DomainPrompts, true},
		{"/api/pins/session", DomainPins, true},
		{"/api/workflows/wf1/draft", DomainWorkflows, true},
		// 不在同步域里的控制面路径与数据面：一律不采集。
		{"/api/node-info", "", false},
		{"/api/web-push/status", "", false},
		{"/api/app/update", "", false},
		{"/api/auth/login", "", false},
		{"/api/control-sync/status", "", false},
		{"/api/sessions", "", false},
		{"/api/preferences-extra", "", false},
		{"/api/node-info", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := DomainForPath(tc.path)
		if got != tc.want || ok != tc.ok {
			t.Errorf("DomainForPath(%q) = %q, %v；期望 %q, %v", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}

// 交接后的基线要包含「副本还不认识的本地记录」：边缘首次启动会自己生成默认账户、
// 内置工作流，它们不是用户改动。只按副本建基线的话，交接收尾的 RefreshBaseline
// 会把这些记录留在基线外，兜底扫描随即把它们当成待回传，恢复时与主节点逐条撞冲突。
func TestRefreshBaselineKeepsLocalOnlyRecords(t *testing.T) {
	store, domain, capturer := newCaptureFixture(t)
	ctx := context.Background()
	// 副本里有主节点给的 a；本地存储里还有一个副本从不认识的 b。
	if _, err := store.WriteRecord(ctx, mustRecord(t, DomainPreferences, "a", `{"v":1}`)); err != nil {
		t.Fatalf("WriteRecord: %v", err)
	}
	domain.set("a", `{"v":1}`)
	domain.set("b", `{"v":2}`)
	if err := capturer.RefreshBaseline(ctx); err != nil {
		t.Fatalf("RefreshBaseline: %v", err)
	}
	if n, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil || n != 0 {
		t.Fatalf("交接后采集 = %d, %v；本地既有记录不该被当成待回传", n, err)
	}
	// 交接之后新出现的记录仍然要能扫出来 —— 兜底采集不能被整体废掉。
	domain.set("c", `{"v":3}`)
	if n, err := capturer.CaptureDomain(ctx, DomainPreferences); err != nil || n != 1 {
		t.Fatalf("交接后新写入采集 = %d, %v，应为 1", n, err)
	}
}

// 兜底扫描要等这一轮离线交接跑完：交接前本地存储里还混着本机自己生成的默认记录。
func TestFallbackScanWaitsForSweepWhen(t *testing.T) {
	store, domain, capturer := newCaptureFixture(t)
	var allowed atomic.Bool
	capturer.SweepWhen = allowed.Load
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		capturer.Run(ctx, 10*time.Millisecond)
	}()
	defer func() {
		cancel()
		<-done
	}()
	domain.set("a", `{"v":1}`)
	time.Sleep(120 * time.Millisecond)
	if got := countChanges(t, store); got != 0 {
		t.Fatalf("交接未完成时兜底扫描采到了 %d 条，应为 0", got)
	}
	allowed.Store(true)
	deadline := time.Now().Add(5 * time.Second)
	for countChanges(t, store) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("放行后兜底扫描仍没采集到写入")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func mustRecord(t *testing.T, domain, key, payload string) Record {
	t.Helper()
	rec, err := newRecord(domain, key, json.RawMessage(payload))
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	return rec
}
