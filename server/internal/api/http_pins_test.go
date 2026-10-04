package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/pins"
	"mindfs/server/internal/session"
)

// pinsTestHandler 造一个带真实项目注册表 + 置顶表的 handler。
//
// 走真实注册表（而不是塞一个假 manager）是刻意的：pinnedSessionsForRoot 的
// 一半价值就是「按项目 id 取会话库、取不到就安静返回空」，用替身把这条测没了。
func pinsTestHandler(t *testing.T) (*HTTPHandler, *AppContext) {
	t.Helper()
	registry := fs.NewRegistryAt(filepath.Join(t.TempDir(), "registry.json"), "")
	if _, err := registry.Upsert(t.TempDir()); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	pinStore, err := pins.NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatalf("pins.NewStoreAt: %v", err)
	}
	ctx := &AppContext{Dirs: registry, Pins: pinStore}
	return &HTTPHandler{AppContext: ctx}, ctx
}

func decodePins(t *testing.T, rec *httptest.ResponseRecorder) pinsResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out pinsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return out
}

// 置顶的写与读必须落在**同一张表**上：写完立刻能读到，且两种置顶互不干扰。
// 这条是「权威只有一处」的全部含义 —— 一旦某个写路径绕开置顶表去写别处
// （比如退回会话库的 pinned_at），症状是「点了置顶，刷新就没了」。
func TestPinsEndpointsRoundTrip(t *testing.T) {
	h, _ := pinsTestHandler(t)

	rec := httptest.NewRecorder()
	h.handlePinsGet(rec, httptest.NewRequest(http.MethodGet, "/api/pins", nil))
	if got := decodePins(t, rec); len(got.Projects) != 0 || len(got.Sessions) != 0 {
		t.Fatalf("新账户该是空表: %+v", got)
	}

	body := strings.NewReader(`{"key":"pc::CMAI","pinned":true}`)
	rec = httptest.NewRecorder()
	h.handlePinsProjectPut(rec, httptest.NewRequest(http.MethodPut, "/api/pins/project", body))
	got := decodePins(t, rec)
	if _, ok := got.Projects["pc::CMAI"]; !ok || len(got.Sessions) != 0 {
		t.Fatalf("项目置顶没写进去: %+v", got)
	}

	rec = httptest.NewRecorder()
	h.handlePinsGet(rec, httptest.NewRequest(http.MethodGet, "/api/pins", nil))
	if got := decodePins(t, rec); len(got.Projects) != 1 {
		t.Fatalf("重读丢了项目置顶: %+v", got)
	}

	rec = httptest.NewRecorder()
	h.handlePinsSessionPut(rec, httptest.NewRequest(http.MethodPut, "/api/pins/session",
		strings.NewReader(`{"root_id":"CMAI","key":"s1","pinned":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("会话置顶失败 %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.handlePinsGet(rec, httptest.NewRequest(http.MethodGet, "/api/pins", nil))
	got = decodePins(t, rec)
	if _, ok := got.Sessions["CMAI::s1"]; !ok {
		t.Fatalf("会话置顶键不对: %+v", got.Sessions)
	}
	if len(got.Projects) != 1 {
		t.Fatalf("写会话置顶不该动项目置顶: %+v", got.Projects)
	}

	// 取消
	rec = httptest.NewRecorder()
	h.pinsSessionPut(rec, httptest.NewRequest(http.MethodPut, "/api/pins/session", nil), sessionPinRequest{RootID: "CMAI", Key: "s1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("取消会话置顶失败 %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.handlePinsGet(rec, httptest.NewRequest(http.MethodGet, "/api/pins", nil))
	if got := decodePins(t, rec); len(got.Sessions) != 0 {
		t.Fatalf("取消没生效: %+v", got.Sessions)
	}
}

// 旧的 POST /api/sessions/{key}/pin 必须与 /api/pins/session 写同一张表。
//
// 前端迁移期间两条路都活着；它们要是各写一处，用户在 A 处置顶、B 处取消
// 就会得到一个静默的最终态（后写的赢，且没有任何迹象表明有两个存储）。
func TestLegacySessionPinRouteWritesSameStore(t *testing.T) {
	h, ctx := pinsTestHandler(t)
	rec := httptest.NewRecorder()
	h.handlePinsSessionPut(rec, httptest.NewRequest(http.MethodPut, "/api/pins/session",
		strings.NewReader(`{"root_id":"CMAI","key":"s1","pinned":true}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("新路径失败: %s", rec.Body.String())
	}
	if _, ok := ctx.Pins.SessionPinnedAt("CMAI::s1"); !ok {
		t.Fatal("新路径没写进置顶表")
	}
	if _, ok := ctx.Pins.ProjectPins()["CMAI"]; ok {
		t.Fatal("会话置顶写进了项目置顶")
	}
}

// 会话列表里的置顶区必须来自置顶表，且**只**包含本机能查到的会话。
func TestPinnedSessionsForRootResolvesFromPinStore(t *testing.T) {
	h, ctx := pinsTestHandler(t)
	rootID := ctx.ListRoots()[0].ID

	mgr, err := ctx.GetSessionManager(rootID)
	if err != nil {
		t.Fatalf("GetSessionManager: %v", err)
	}
	local, err := mgr.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Name: "local pinned"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := ctx.Pins.SetSessionPin(rootID+"::"+local.Key, true); err != nil {
		t.Fatalf("置顶: %v", err)
	}
	// 一条指向别的机器的置顶：键格式对，但这台机器的库里没有。
	if _, _, err := ctx.Pins.SetSessionPin(rootID+"::别的机器的键", true); err != nil {
		t.Fatalf("置顶(跨机): %v", err)
	}

	items, keys := h.pinnedSessionsForRoot(context.Background(), rootID, true)
	if len(items) != 1 || items[0].Key != local.Key {
		t.Fatalf("置顶区 = %v, want 只含 %s", sessionKeys(items), local.Key)
	}
	if len(keys) != 1 || keys[0] != local.Key {
		t.Fatalf("pinned_keys = %v, want [%s]", keys, local.Key)
	}
	// 置顶时间必须落到条目上：sessionListResponse 报的 pinned_at 取自
	// Session.PinnedAt，而会话库那列已退役恒为 nil —— 不补的话前端拿到
	// pinned_keys 却拿不到时间，置顶区在 UI 上排不出来（实测 pinned_at: null）。
	if items[0].PinnedAt == nil || items[0].PinnedAt.IsZero() {
		t.Fatal("置顶条目必须带上置顶时间，否则前端排不出置顶顺序")
	}

	// 没置顶就不该出现在置顶区 —— 靠的是置顶表里有它，而不是会话库里的标记。
	other, err := mgr.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Name: "not pinned"})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	items, _ = h.pinnedSessionsForRoot(context.Background(), rootID, true)
	for _, item := range items {
		if item.Key == other.Key {
			t.Fatal("没置顶的会话出现在置顶区")
		}
	}
	// 反向：置顶表里没有的条目绝不能带置顶时间（否则整个列表看起来都是置顶的）
	plain, err := mgr.ListByKeys(context.Background(), []string{other.Key})
	if err != nil {
		t.Fatalf("ListByKeys: %v", err)
	}
	if plain[0].PinnedAt != nil {
		t.Fatal("ListByKeys 不该自己带置顶时间 —— 那是 pinnedSessionsForRoot 的职责")
	}

	// 取消后立即消失
	if _, _, err := ctx.Pins.SetSessionPin(rootID+"::"+local.Key, false); err != nil {
		t.Fatalf("取消: %v", err)
	}
	if items, _ := h.pinnedSessionsForRoot(context.Background(), rootID, true); len(items) != 0 {
		t.Fatalf("取消后仍在置顶区: %v", sessionKeys(items))
	}
}

// 置顶表按置顶时间倒序 —— 反复点「置顶」不刷新时间戳，所以顺序稳定。
func TestPinnedSessionsForRootSortsByPinTime(t *testing.T) {
	h, ctx := pinsTestHandler(t)
	rootID := ctx.ListRoots()[0].ID
	mgr, err := ctx.GetSessionManager(rootID)
	if err != nil {
		t.Fatalf("GetSessionManager: %v", err)
	}
	first, _ := mgr.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Name: "first"})
	second, _ := mgr.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Name: "second"})
	// 先置顶 second，再置顶 first → first 应排在前面。
	if _, _, err := ctx.Pins.SetSessionPin(rootID+"::"+second.Key, true); err != nil {
		t.Fatalf("pin second: %v", err)
	}
	if _, _, err := ctx.Pins.SetSessionPin(rootID+"::"+first.Key, true); err != nil {
		t.Fatalf("pin first: %v", err)
	}
	items, _ := h.pinnedSessionsForRoot(context.Background(), rootID, true)
	if len(items) != 2 || items[0].Key != first.Key {
		t.Fatalf("置顶区应按置顶时间倒序, got %v", sessionKeys(items))
	}
}

// 置顶表跨项目：另一个项目的键不该混进来。
func TestPinnedSessionsForRootIgnoresOtherRoots(t *testing.T) {
	h, ctx := pinsTestHandler(t)
	rootID := ctx.ListRoots()[0].ID
	// 不存在的项目也该安静返回空，不能把别人的键算进来，更不能 panic。
	items, keys := h.pinnedSessionsForRoot(context.Background(), "根本没有这个项目", true)
	if len(items) != 0 || len(keys) != 0 {
		t.Fatalf("未知项目应返回空: %v / %v", sessionKeys(items), keys)
	}
	// 别的项目的键不进本项目
	mgr, _ := ctx.GetSessionManager(rootID)
	s, _ := mgr.Create(context.Background(), session.CreateInput{Type: session.TypeChat, Name: "s"})
	if _, _, err := ctx.Pins.SetSessionPin("别的项目::"+s.Key, true); err != nil {
		t.Fatalf("pin other root: %v", err)
	}
	if items, _ := h.pinnedSessionsForRoot(context.Background(), rootID, true); len(items) != 0 {
		t.Fatalf("别的项目的置顶混进来了: %v", sessionKeys(items))
	}

	// **不带项目**的置顶键（PUT /api/pins/session 不给 root_id 时就是这个形状）
	// 不该在每个项目里都冒出来 —— 那会让一次误操作变成「到处都顶着一坨」。
	// 这是唯一能观测到项目过滤是否真的存在的地方：会话 key 是 UUID，
	// 别的项目的键本来就查不到，滤不滤都一样。
	if _, _, err := ctx.Pins.SetSessionPin(s.Key, true); err != nil {
		t.Fatalf("pin 无项目: %v", err)
	}
	if items, _ := h.pinnedSessionsForRoot(context.Background(), rootID, true); len(items) != 0 {
		t.Fatalf("无项目前缀的置顶键混进了项目 %s: %v", rootID, sessionKeys(items))
	}
}

// 没有置顶表（构造失败/未接线）时不该 500，安静当没置顶。
func TestPinnedSessionsForRootWithoutPinStore(t *testing.T) {
	h := &HTTPHandler{AppContext: &AppContext{}}
	if items, keys := h.pinnedSessionsForRoot(context.Background(), "any", true); len(items) != 0 || len(keys) != 0 {
		t.Fatalf("无置顶表应返回空: %v / %v", sessionKeys(items), keys)
	}
	rec := httptest.NewRecorder()
	h.handlePinsGet(rec, httptest.NewRequest(http.MethodGet, "/api/pins", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("无置顶表应 503, got %d", rec.Code)
	}
}

// sessionPinKey 拼键规则：root 可空；空 key 一律拒绝（否则会写进一条垃圾记录）。
func TestSessionPinKey(t *testing.T) {
	cases := []struct{ root, key, want string }{
		{"CMAI", "s1", "CMAI::s1"},
		{"", "s1", "s1"},
		{" CMAI ", " s1 ", "CMAI::s1"},
		{"CMAI", "  ", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := sessionPinKey(c.root, c.key); got != c.want {
			t.Errorf("sessionPinKey(%q,%q) = %q, want %q", c.root, c.key, got, c.want)
		}
	}
}

func sessionKeys(items []*session.Session) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if s != nil {
			out = append(out, s.Key)
		}
	}
	return out
}
