package app

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"mindfs/server/internal/api"
	"mindfs/server/internal/auth"
	"mindfs/server/internal/config"
	"mindfs/server/internal/controlsync"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/nodeinfo"
	"mindfs/server/internal/nodes"
	"mindfs/server/internal/preferences"
)

// controlSyncFixture 在临时 XDG_CONFIG_HOME 下建出四个既有 store。
// 域适配器只保存指针，构造本身不读写用户数据。
func controlSyncFixture(t *testing.T) (string, controlSyncStores) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	cfgDir, err := config.MindFSConfigDir()
	if err != nil {
		t.Fatalf("MindFSConfigDir: %v", err)
	}
	authStore, err := auth.EnsureStore()
	if err != nil {
		t.Fatalf("auth.EnsureStore: %v", err)
	}
	prefs, err := preferences.NewStore()
	if err != nil {
		t.Fatalf("preferences.NewStore: %v", err)
	}
	nodeStore, err := nodes.NewStore()
	if err != nil {
		t.Fatalf("nodes.NewStore: %v", err)
	}
	workflows, err := kanban.NewWorkflowStoreDefault()
	if err != nil {
		t.Fatalf("NewWorkflowStoreDefault: %v", err)
	}
	return cfgDir, controlSyncStores{
		auth: authStore, prefs: prefs, nodes: nodeStore, workflows: workflows,
	}
}

// syncTestToken 满足 MinSyncTokenLen：短 token 现在会在装配处被拒，
// 用例里的凭据必须是真的可用形状，否则测的就不是它想测的那件事。
const syncTestToken = "test-token-0123456789"

func TestStartControlSyncDisabledIsNoOp(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	ctx := t.Context()
	for name, opts := range map[string]*ControlSyncOptions{
		"nil":      nil,
		"disabled": {Enabled: false},
	} {
		t.Run(name, func(t *testing.T) {
			w, err := startControlSync(ctx, opts, nodeinfo.RoleControl, cfgDir, stores)
			if err != nil || w != nil {
				t.Fatalf("wiring=%v err=%v，未启用应 (nil,nil)", w, err)
			}
			if _, statErr := os.Stat(filepath.Join(cfgDir, "control_sync.db")); !os.IsNotExist(statErr) {
				t.Fatalf("未启用却建了同步库：%v", statErr)
			}
		})
	}
	// nil wiring 也要能安全装进 handler（每个按账户构建的 handler 都会走这一句）。
	var nilWiring *controlSyncWiring
	nilWiring.applyTo(&api.HTTPHandler{})
}

// 控制同步要搬的是整份控制面：worker 只跑数据面，开着它只会得到一个
// 永远转发的角色。启动时就拒绝，而不是让它跑起来。
func TestStartControlSyncRejectsWorkerNode(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	_, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled: true, Role: controlsync.RolePrimary, Token: syncTestToken,
	}, nodeinfo.RoleWorker, cfgDir, stores)
	if err == nil || !strings.Contains(err.Error(), "role=control") {
		t.Fatalf("err = %v，worker 上应拒绝启用同步", err)
	}
}

// 没有 token 的主节点不注册 /push /head /changes，边缘永远同步不了 ——
// 这不是可选项缺失，是同步根本没通。
func TestStartControlSyncPrimaryRequiresToken(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	_, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled: true, Role: controlsync.RolePrimary,
	}, nodeinfo.RoleControl, cfgDir, stores)
	if err == nil || !strings.Contains(err.Error(), "token required for primary") {
		t.Fatalf("err = %v，主节点缺 token 应拒绝启动", err)
	}
}

// token 是这条通道唯一的凭据，而通道搬的是含口令 hash 的整份控制面：
// 一个手输的短串（"123456"）挡不住任何东西，启动时就该拒绝。
func TestStartControlSyncRejectsWeakToken(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	for _, token := range []string{"short", "1234567890123"} {
		_, err := startControlSync(t.Context(), &ControlSyncOptions{
			Enabled: true, Role: controlsync.RolePrimary, Token: token,
		}, nodeinfo.RoleControl, cfgDir, stores)
		if err == nil || !strings.Contains(err.Error(), "token") {
			t.Fatalf("token %q 的 err = %v，弱 token 应拒绝启动", token, err)
		}
	}
}

// 主节点启动就要把整份控制面写进变更流。兜底扫描 15 秒才有第一拍，而边缘完全可能
// 在这之前完成首次追平然后掉线 —— 那一版副本会缺记录（首次启动生成的管理员账户
// 就在其中），恢复时把主节点早就有的记录当成「两侧都改」报冲突。
func TestStartControlSyncPrimaryPublishesBaselineAtStartup(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	w, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled: true, Role: controlsync.RolePrimary, InstanceID: "home", Token: syncTestToken,
	}, nodeinfo.RoleControl, cfgDir, stores)
	if err != nil || w == nil {
		t.Fatalf("startControlSync: %v", err)
	}
	recs, err := w.runtime.Store().ListRecords(t.Context(), controlsync.DomainUsers)
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(recs) == 0 {
		t.Fatalf("启动后 users 域一条记录都没有：变更流是空的，边缘只能靠 15 秒后的兜底扫描")
	}
	changes, err := w.runtime.Store().ListChangesSince(t.Context(), 0, 100)
	if err != nil {
		t.Fatalf("ListChangesSince: %v", err)
	}
	if len(changes) == 0 {
		t.Fatalf("启动后变更流为空")
	}
}

func TestStartControlSyncPrimaryExposesPushAndFeed(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	w, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled: true, Role: controlsync.RolePrimary, InstanceID: "home", Token: syncTestToken,
	}, nodeinfo.RoleControl, cfgDir, stores)
	if err != nil {
		t.Fatalf("startControlSync: %v", err)
	}
	if w == nil || w.runtime == nil || w.capturer == nil {
		t.Fatalf("wiring = %+v，主节点也要跑兜底采集", w)
	}
	if w.pusher == nil || w.feed == nil {
		t.Fatalf("主节点缺 push/feed：%+v", w)
	}
	if w.token != syncTestToken {
		t.Fatalf("token = %q", w.token)
	}
	if w.service != nil {
		t.Fatalf("主节点不该有模式状态机")
	}
	if w.proxy != nil {
		t.Fatalf("主节点不转发")
	}
	h := &api.HTTPHandler{}
	w.applyTo(h)
	if h.ControlSyncToken != syncTestToken || h.ControlSyncPush == nil || h.ControlSyncFeed == nil {
		t.Fatalf("handler 没装上同步端点：%+v", h)
	}
	if h.ControlSync != nil {
		t.Fatalf("主节点不该暴露 /status /mode")
	}
	if _, statErr := os.Stat(filepath.Join(cfgDir, "control_sync.db")); statErr != nil {
		t.Fatalf("同步库没建：%v", statErr)
	}
}

func TestStartControlSyncEdgeWiresProxyAndMode(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	w, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled:    true,
		Role:       controlsync.RoleEdge,
		InstanceID: "hasee",
		PrimaryURL: "https://home.example:7331",
		Token:      syncTestToken,
	}, nodeinfo.RoleControl, cfgDir, stores)
	if err != nil {
		t.Fatalf("startControlSync: %v", err)
	}
	if w.service == nil || w.conflicts == nil {
		t.Fatalf("边缘缺状态/冲突端点：%+v", w)
	}
	if w.proxy == nil {
		t.Fatalf("边缘没有转发器")
	}
	if w.proxy.Mode == nil || w.proxy.BlockedDomain == nil {
		t.Fatalf("转发器缺模式或按域冻结判定：%+v", w.proxy)
	}
	// 边缘不暴露主节点侧端点：它没有 token，也不该被别人推送。
	if w.token != "" || w.pusher != nil || w.feed != nil {
		t.Fatalf("边缘不该有 push/feed/token：token=%q", w.token)
	}
	h := &api.HTTPHandler{}
	w.applyTo(h)
	if h.ControlSync == nil || h.ControlSyncConflicts == nil || h.ControlSyncToken != "" {
		t.Fatalf("handler 装载不对：%+v", h)
	}
	// 转发范围：控制面转发、本机域与控制同步自身不转发。
	if !w.proxy.ShouldForward("/api/preferences") {
		t.Fatalf("控制面请求应转发")
	}
	for _, path := range []string{"/api/control-sync/status", "/api/web-push/status", "/api/node-info", "/api/app/update"} {
		if w.proxy.ShouldForward(path) {
			t.Fatalf("%s 是本机域，不该转发", path)
		}
	}
}

func TestStartControlSyncEdgeRejectsBadConfig(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	cases := []struct {
		name string
		opts ControlSyncOptions
		want string
	}{
		{"缺实例 id", ControlSyncOptions{Enabled: true, Role: "edge",
			PrimaryURL: "https://home:7331", Token: syncTestToken}, "instance_id required"},
		{"明文主节点", ControlSyncOptions{Enabled: true, Role: "edge",
			InstanceID: "hasee", PrimaryURL: "http://home:7331", Token: syncTestToken}, "must be https"},
		{"缺 token", ControlSyncOptions{Enabled: true, Role: "edge",
			InstanceID: "hasee", PrimaryURL: "https://home:7331"}, "token required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := startControlSync(t.Context(), &tc.opts, nodeinfo.RoleControl, cfgDir, stores)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v，应含 %q", err, tc.want)
			}
		})
	}
}

func TestControlSyncDomainsCoverAllSix(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	domains, err := controlSyncDomains(cfgDir, stores)
	if err != nil {
		t.Fatalf("controlSyncDomains: %v", err)
	}
	got := map[string]bool{}
	for _, d := range domains {
		got[d.Name()] = true
	}
	for _, want := range []string{
		controlsync.DomainUsers, controlsync.DomainPreferences, controlsync.DomainNodes,
		controlsync.DomainWorkflows, controlsync.DomainPrompts, controlsync.DomainPins,
	} {
		if !got[want] {
			t.Fatalf("缺域 %s：%v", want, got)
		}
	}
	// 少一份存储就少一个域，那会让副本「看起来能用」。必须显式失败。
	missing := stores
	missing.nodes = nil
	if _, err := controlSyncDomains(cfgDir, missing); err == nil {
		t.Fatalf("缺 nodes 存储却建出了域列表")
	}
}

// 主节点必须真的把本机控制面写采进变更流：没有这一步，边缘永远拉不到新值。
func TestControlSyncWrapRootCapturesLocalWrites(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	w, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled: true, Role: controlsync.RolePrimary, InstanceID: "home", Token: syncTestToken,
	}, nodeinfo.RoleControl, cfgDir, stores)
	if err != nil {
		t.Fatalf("startControlSync: %v", err)
	}
	next := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if err := stores.prefs.SetField("theme", json.RawMessage(`"dark"`)); err != nil {
			t.Errorf("SetField: %v", err)
		}
		rw.WriteHeader(http.StatusNoContent)
	})
	h := w.wrapRoot(next)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/preferences", nil))
	head, err := w.feed.Head(t.Context())
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if head.HeadRev == 0 {
		t.Fatalf("控制面写入没进变更流：head = 0")
	}
	// 数据面写入不归同步管：同样的 POST 落在 /api/tasks 上不该推进水位。
	before := head.HeadRev
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/tasks", nil))
	head, _ = w.feed.Head(t.Context())
	if head.HeadRev != before {
		t.Fatalf("数据面写入推进了变更流：%d → %d", before, head.HeadRev)
	}
}

// 边缘在线时控制面请求必须真的发到主节点、并且**不**在本机执行；
// 数据面请求留在本机；掉线后控制面写入留在本机（离线可用）。
// 前端镜像只属于边缘：主节点自己就是产物的来源，没东西可镜像。
// 有镜像之后，handler 的静态目录要跟着镜像走 —— 否则「同步了但页面还是旧的」。
func TestControlSyncWiresUIMirrorOnlyOnEdge(t *testing.T) {
	primary := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer primary.Close()

	edgeDir, edgeStores := controlSyncFixture(t)
	edge, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled: true, Role: controlsync.RoleEdge, InstanceID: "hasee",
		PrimaryURL: primary.URL, Token: syncTestToken, BuildVersion: "v1",
	}, nodeinfo.RoleControl, edgeDir, edgeStores)
	if err != nil {
		t.Fatalf("edge startControlSync: %v", err)
	}
	mirror := edge.uiMirrorOf()
	if mirror == nil {
		t.Fatal("边缘没有前端镜像：主节点不可达时页面就打不开了")
	}
	// 还没同步到任何版本时 CurrentDir 为空，调用方回落到安装目录。
	if got := mirror.CurrentDir(); got != "" {
		t.Fatalf("首次同步前不该有可用目录，实际 %q", got)
	}

	primaryDir, primaryStores := controlSyncFixture(t)
	prim, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled: true, Role: controlsync.RolePrimary, InstanceID: "home",
		Token: syncTestToken, BuildVersion: "v1",
	}, nodeinfo.RoleControl, primaryDir, primaryStores)
	if err != nil {
		t.Fatalf("primary startControlSync: %v", err)
	}
	if prim.uiMirrorOf() != nil {
		t.Fatal("主节点不该装前端镜像 —— 它自己就是产物的来源")
	}
}

// 镜像装好了、指针也会切，但 handler 拿不到 provider 的话页面永远出安装目录那份：
// 症状只有「更新了但页面没变」。这里钉的是最后这一跳。
func TestControlSyncAppliesMirrorStaticDirOnlyOnEdge(t *testing.T) {
	primary := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer primary.Close()
	edgeDir, edgeStores := controlSyncFixture(t)
	edge, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled: true, Role: controlsync.RoleEdge, InstanceID: "hasee",
		PrimaryURL: primary.URL, Token: syncTestToken, BuildVersion: "v1",
	}, nodeinfo.RoleControl, edgeDir, edgeStores)
	if err != nil {
		t.Fatalf("edge startControlSync: %v", err)
	}
	edgeWorkspaces := &workspaceManager{}
	edgeWorkspaces.SetHandlerDefaults("/install/web", nil)
	applyUIMirrorStaticDir(edgeWorkspaces, edge)
	if _, provider, _, _ := edgeWorkspaces.handlerDefaults(); provider == nil {
		t.Fatal("边缘装了镜像却没接到静态目录：页面会一直出安装目录那版")
	}
	// 按账户构建的 handler 才是真正服务页面的那个：provider 必须落到它身上。
	if h := edgeWorkspaces.NewHTTPHandler(nil, "v1"); h.StaticDirProvider == nil {
		t.Fatal("handler 没拿到 provider：镜像切了版本，页面还是旧的")
	}

	primaryDir, primaryStores := controlSyncFixture(t)
	prim, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled: true, Role: controlsync.RolePrimary, InstanceID: "home",
		Token: syncTestToken, BuildVersion: "v1",
	}, nodeinfo.RoleControl, primaryDir, primaryStores)
	if err != nil {
		t.Fatalf("primary startControlSync: %v", err)
	}
	primaryWorkspaces := &workspaceManager{}
	primaryWorkspaces.SetHandlerDefaults("/install/web", nil)
	applyUIMirrorStaticDir(primaryWorkspaces, prim)
	if _, provider, _, _ := primaryWorkspaces.handlerDefaults(); provider != nil {
		t.Fatal("主节点不该有前端镜像 provider：它自己就是产物的来源")
	}
}

func TestControlSyncWrapRootForwardsOnlyOnEdge(t *testing.T) {
	cfgDir, stores := controlSyncFixture(t)
	var primaryHits int32
	primary := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只数被转发的控制面请求：边缘还会自己来拉前端清单与包（UI 镜像），
		// 那是另一条链路，混进来会让这条断言变成「主节点一共收到几次请求」。
		if r.URL.Path == "/api/preferences" {
			atomic.AddInt32(&primaryHits, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer primary.Close()
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: primary.Certificate().Raw})
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	w, err := startControlSync(t.Context(), &ControlSyncOptions{
		Enabled:    true,
		Role:       controlsync.RoleEdge,
		InstanceID: "hasee",
		PrimaryURL: primary.URL,
		CAFile:     caPath,
		Token:      syncTestToken,
	}, nodeinfo.RoleControl, cfgDir, stores)
	if err != nil {
		t.Fatalf("startControlSync: %v", err)
	}
	var nextHit int32
	next := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&nextHit, 1)
		rw.WriteHeader(http.StatusOK)
	})
	h := w.wrapRoot(next)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/preferences", nil))
	if got := atomic.LoadInt32(&primaryHits); got != 1 {
		t.Fatalf("控制面写入没转发给主节点：primary hits = %d", got)
	}
	if got := atomic.LoadInt32(&nextHit); got != 0 {
		t.Fatalf("在线时控制面写入在本机执行了 %d 次", got)
	}

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/tasks", nil))
	if got := atomic.LoadInt32(&nextHit); got != 1 {
		t.Fatalf("数据面请求没留在本机：next hits = %d", got)
	}

	// 主节点不可达 → 本机离线模式：控制面写入落本地副本，不再转发。
	if err := w.service.SwitchLocal(t.Context()); err != nil {
		t.Fatalf("SwitchLocal: %v", err)
	}
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/preferences", nil))
	if got := atomic.LoadInt32(&primaryHits); got != 1 {
		t.Fatalf("离线时仍在转发：primary hits = %d", got)
	}
	if got := atomic.LoadInt32(&nextHit); got != 2 {
		t.Fatalf("离线时控制面写入没落到本机：next hits = %d", got)
	}
}

func TestBlockedDomainCheckerFreezesOnlyConflictDomains(t *testing.T) {
	store, err := controlsync.Open(filepath.Join(t.TempDir(), "control_sync.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	check := blockedDomainChecker(store)
	if check(controlsync.DomainPins) {
		t.Fatalf("没有冲突却冻结了 pins")
	}
	if _, err := store.InsertConflict(t.Context(), controlsync.Conflict{
		ID:     controlsync.PullConflictID(controlsync.DomainPins, "s1"),
		Domain: controlsync.DomainPins, Key: "s1",
		LocalHash: "sha256:local", RemoteHash: "sha256:remote",
	}); err != nil {
		t.Fatalf("InsertConflict: %v", err)
	}
	// 按域冻结：冲突域挡住，其它域照常写本地。
	if !check(controlsync.DomainPins) {
		t.Fatalf("冲突域没被冻结")
	}
	if check(controlsync.DomainPreferences) {
		t.Fatalf("无冲突的域被连带冻结")
	}
	// 查库失败按冻结处理：恢复期宁可让用户重试一次，也不放一条会和主节点分叉的写。
	_ = store.Close()
	if !check(controlsync.DomainPreferences) {
		t.Fatalf("查询失败时应 fail closed")
	}
}
