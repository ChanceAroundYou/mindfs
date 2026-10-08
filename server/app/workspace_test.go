package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/auth"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/nodeinfo"
	"mindfs/server/internal/nodes"
	"mindfs/server/internal/preferences"
	"mindfs/server/internal/webpush"
)

// 共享范围是用户明确定下的：只有「加载的项目」和「项目里的会话」按账户分，
// 其余一律共享。最容易的回归就是有人又把某个 store 改成按账户新建——
// 那会让两个账户的偏好/节点/订阅悄悄分裂成两份。这个测试守住它。
func TestWorkspaceSharingContract(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	// 账户表：一个主账户 + 一个普通账户
	usersPath := filepath.Join(cfgDir, "mindfs", "users.json")
	if err := os.MkdirAll(filepath.Dir(usersPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(usersPath), "login.json"),
		[]byte(`{"password":"root-secret"}`), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	authStore, err := auth.EnsureStoreAt(usersPath)
	if err != nil {
		t.Fatalf("EnsureStoreAt: %v", err)
	}
	if _, err := authStore.Create("bob", "bob-secret", auth.RoleUser); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	primaryID := authStore.PrimaryUserID()
	var bobID string
	for _, u := range authStore.List() {
		if u.Username == "bob" {
			bobID = u.ID
		}
	}
	if primaryID == "" || bobID == "" {
		t.Fatalf("bad fixture: primary=%q bob=%q", primaryID, bobID)
	}

	prefs, err := preferences.NewStore()
	if err != nil {
		t.Fatalf("preferences: %v", err)
	}
	nodeStore, err := nodes.NewStore()
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	templates, err := kanban.NewTemplateStore()
	if err != nil {
		t.Fatalf("kanban templates: %v", err)
	}
	pool := agent.NewPool(agent.Config{})

	mgr := newWorkspaceManager(context.Background(), sharedServices{
		auth:      authStore,
		prefs:     prefs,
		nodes:     nodeStore,
		webPush:   webpush.NewService(webpush.Config{}, webpush.NewStoreAt(filepath.Dir(usersPath))),
		templates: templates,
		pool:      pool,
	})
	mgr.SetBaseDir(filepath.Join(filepath.Dir(usersPath), "users"))

	primary, err := mgr.Workspace(primaryID)
	if err != nil {
		t.Fatalf("primary workspace: %v", err)
	}
	other, err := mgr.Workspace(bobID)
	if err != nil {
		t.Fatalf("bob workspace: %v", err)
	}
	if primary == other {
		t.Fatal("两个账户拿到了同一个 AppContext")
	}

	// —— 按账户分：项目列表 与 项目工作状态(meta) ——
	if primary.Dirs == other.Dirs {
		t.Fatal("两个账户共用了同一个项目注册表：项目列表必须各看各的")
	}
	if primary.MetaRoot() == other.MetaRoot() {
		t.Fatalf("两个账户的 meta 相同(%q)：会话/任务会串号", primary.MetaRoot())
	}
	if primary.MetaRoot() != "" {
		t.Fatalf("主账户的 meta 根应为空（沿用项目内 .mindfs）: %q", primary.MetaRoot())
	}
	if other.MetaRoot() == "" {
		t.Fatal("非主账户必须有私有 meta 根")
	}
	// 主账户的注册表要落在 <cfg>/ 而不是 users/<id>/
	if _, err := primary.Dirs.Upsert(filepath.Join(cfgDir, "proj-primary")); err != nil {
		t.Fatalf("upsert primary project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(usersPath), "registry.json")); err != nil {
		t.Fatalf("主账户的项目列表应在 <cfg>/registry.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(usersPath), "users", primaryID, "registry.json")); err == nil {
		t.Fatal("主账户的项目列表不该落在 users/<id>/ 下")
	}
	// 非主账户的注册表落在自己的账户目录
	if _, err := other.Dirs.Upsert(filepath.Join(cfgDir, "proj-bob")); err != nil {
		t.Fatalf("upsert bob project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(usersPath), "users", bobID, "registry.json")); err != nil {
		t.Fatalf("非主账户的项目列表应在 users/<id>/registry.json: %v", err)
	}

	// 两边看到对方加的项目 = 隔离失效
	for _, r := range primary.ListRoots() {
		if r.ID == "proj-bob" {
			t.Fatal("主账户看到了 bob 的项目")
		}
	}
	for _, r := range other.ListRoots() {
		if r.ID == "proj-primary" {
			t.Fatal("bob 看到了主账户的项目")
		}
	}

	// —— 按账户分：置顶 ——
	// 与偏好相反：置顶必须各账户一份，否则你的置顶会出现在别人的会话栏里。
	// 项目置顶原本住在**共享**偏好里，那正是它跨账户泄漏的地方。
	if primary.Pins == nil || other.Pins == nil {
		t.Fatal("置顶表不该为空")
	}
	if primary.Pins == other.Pins {
		t.Error("两个账户共用了同一个置顶表：置顶必须各看各的")
	}
	// 真的分开：主账户置一个，bob 那边必须看不见
	if _, _, err := primary.Pins.SetProjectPin("pc::CMAI", true); err != nil {
		t.Fatalf("主账户置顶失败: %v", err)
	}
	if _, ok := other.Pins.ProjectPins()["pc::CMAI"]; ok {
		t.Error("bob 看到了主账户的置顶：置顶跨账户泄漏了")
	}
	// 反向也一样，且两边可以顶不同的项目
	if _, _, err := other.Pins.SetProjectPin("pc::docs", true); err != nil {
		t.Fatalf("bob 置顶失败: %v", err)
	}
	if _, ok := primary.Pins.ProjectPins()["pc::docs"]; ok {
		t.Error("主账户看到了 bob 的置顶：置顶跨账户泄漏了")
	}
	// 会话置顶同样按账户分
	if _, _, err := primary.Pins.SetSessionPin("pc::CMAI::s1", true); err != nil {
		t.Fatalf("主账户会话置顶失败: %v", err)
	}
	if _, ok := other.Pins.SessionPinnedAt("pc::CMAI::s1"); ok {
		t.Error("bob 看到了主账户的会话置顶")
	}
	// 落盘位置：主账户在 <cfg>/，bob 在 users/<id>/（与 registry 同口径）
	if _, err := os.Stat(filepath.Join(filepath.Dir(usersPath), "session_pins.json")); err != nil {
		t.Errorf("主账户置顶表应在 <cfg>/session_pins.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(usersPath), "users", bobID, "session_pins.json")); err != nil {
		t.Errorf("bob 的置顶表应在 users/<id>/session_pins.json: %v", err)
	}

	// —— 共享：设置与 agent 资源必须是同一份实例 ——
	shared := []struct {
		name string
		lhs  any
		rhs  any
	}{
		{"偏好", primary.Prefs, other.Prefs},
		{"节点表", primary.Nodes, other.Nodes},
		{"WebPush", primary.WebPush, other.WebPush},
		{"agent 进程池", primary.Agents, other.Agents},
	}
	for _, c := range shared {
		if c.lhs != c.rhs {
			t.Errorf("%s 应全账户共享同一份实例，实际各建了一份", c.name)
		}
	}
	if primary.Prefs == nil || primary.Agents == nil {
		t.Fatal("共享实例不应为空")
	}

	// 共享实例上的写入必须两边都看得见
	if err := other.Prefs.UpdateNewProjectMetaLocation("home"); err != nil {
		t.Fatalf("写共享偏好失败: %v", err)
	}
	if got := primary.Prefs.NewProjectMetaLocation(); got != "home" {
		t.Errorf("bob 改偏好后主账户看到 %q：偏好没有真正共享", got)
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(usersPath), "preferences.json"))
	if err != nil {
		t.Fatalf("偏好应只落一份 <cfg>/preferences.json: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("偏好文件是空的")
	}
	_ = json.Valid(raw)

	// 每账户的 StreamHub 必须独立（否则广播跨账户泄漏），这部分在 api 侧另有测试
	if primary.GetSessionStreamHub() == other.GetSessionStreamHub() {
		t.Fatal("两个账户共用了 StreamHub：WS 广播会跨账户泄漏")
	}
}

// pinSeedFixtureAt 建一个「主账户 + bob」的最小环境，回填测试用。
//
// cfgDir / usersPath 由调用方给：回填测试要**两次**建 manager（模拟重启），
// 第二次必须落在同一个目录上，否则量的不是「重启后置顶有没有回来」而是新目录。
// 与上面那个测试各自建 fixture：那个测试守的是「共享实例必须是同一份指针」，
// 塞进共享 helper 会让它也依赖置顶的构造顺序，测出问题时不好定位。
func pinSeedFixtureAt(t *testing.T, cfgDir, usersPath string) (*workspaceManager, string, string, *preferences.Store) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	if err := os.MkdirAll(filepath.Dir(usersPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := os.Stat(usersPath); os.IsNotExist(err) {
		if err := os.WriteFile(filepath.Join(filepath.Dir(usersPath), "login.json"),
			[]byte(`{"password":"root-secret"}`), 0o600); err != nil {
			t.Fatalf("write legacy: %v", err)
		}
		authStore, err := auth.EnsureStoreAt(usersPath)
		if err != nil {
			t.Fatalf("EnsureStoreAt: %v", err)
		}
		if _, err := authStore.Create("bob", "bob-secret", auth.RoleUser); err != nil {
			t.Fatalf("create bob: %v", err)
		}
	}
	authStore, err := auth.EnsureStoreAt(usersPath)
	if err != nil {
		t.Fatalf("EnsureStoreAt: %v", err)
	}
	primaryID := authStore.PrimaryUserID()
	var bobID string
	for _, u := range authStore.List() {
		if u.Username == "bob" {
			bobID = u.ID
		}
	}
	if primaryID == "" || bobID == "" {
		t.Fatalf("bad fixture: primary=%q bob=%q", primaryID, bobID)
	}

	prefs, err := preferences.NewStore()
	if err != nil {
		t.Fatalf("preferences: %v", err)
	}
	nodeStore, err := nodes.NewStore()
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	templates, err := kanban.NewTemplateStore()
	if err != nil {
		t.Fatalf("kanban templates: %v", err)
	}
	mgr := newWorkspaceManager(context.Background(), sharedServices{
		auth:      authStore,
		prefs:     prefs,
		nodes:     nodeStore,
		webPush:   webpush.NewService(webpush.Config{}, webpush.NewStoreAt(filepath.Dir(usersPath))),
		templates: templates,
		pool:      agent.NewPool(agent.Config{}),
	})
	mgr.SetBaseDir(filepath.Join(filepath.Dir(usersPath), "users"))
	return mgr, primaryID, bobID, prefs
}

// 回填：项目置顶原本住在**共享**偏好里，第一次建账户时搬进该账户自己的文件。
//
// 关键性质是搬完就由新文件说话 —— 共享偏好里还留着旧值，但绝不能因此每次启动
// 都搬一次，否则用户在新存储里取消掉的置顶会在下次启动时自己复活。
//
// 「重启」必须是**真的**重启：workspaceManager 会缓存每个账户的 AppContext，
// 同一个 manager 再调 Workspace() 拿回的是同一个内存 store（根本不读盘），
// 那样这个测试在「每次都回填」的变异下也会通过 —— 钉不住任何东西。
// 所以下面重新建一个 manager，让 build() 从盘上重新读。
func TestPinSeedRunsOnceAndNeverResurrectsUnpinned(t *testing.T) {
	cfgDir := t.TempDir()
	usersPath := filepath.Join(cfgDir, "mindfs", "users.json")

	mgr, primaryID, bobID, prefs := pinSeedFixtureAt(t, cfgDir, usersPath)

	// 共享偏好里有一份历史置顶
	if err := prefs.UpdateSessionProjectPins(map[string]int64{"pc::CMAI": 1000}); err != nil {
		t.Fatalf("写共享偏好失败: %v", err)
	}
	primary, err := mgr.Workspace(primaryID)
	if err != nil {
		t.Fatalf("primary workspace: %v", err)
	}
	other, err := mgr.Workspace(bobID)
	if err != nil {
		t.Fatalf("bob workspace: %v", err)
	}
	// 两个账户都拿到了那份历史置顶（它们此前本来就能看到同一份共享值）
	if got := primary.Pins.ProjectPins()["pc::CMAI"]; got != 1000 {
		t.Errorf("主账户没回填到项目置顶: %v", primary.Pins.ProjectPins())
	}
	if got := other.Pins.ProjectPins()["pc::CMAI"]; got != 1000 {
		t.Errorf("bob 没回填到项目置顶: %v", other.Pins.ProjectPins())
	}

	// 用户在新存储里取消掉
	if _, _, err := primary.Pins.SetProjectPin("pc::CMAI", false); err != nil {
		t.Fatalf("取消置顶: %v", err)
	}
	// 真的重启：新建一个 manager，让 build() 从盘上重新读。
	// 复用同一个 cfgDir —— 量的就是「重启后置顶有没有回来」。
	// 共享偏好里旧值还在（我们从没删过它），所以只要回填不看 NeedsLegacySeed
	// 就会把它搬回来。
	restartedMgr, _, _, _ := pinSeedFixtureAt(t, cfgDir, usersPath)
	restarted, err := restartedMgr.Workspace(primaryID)
	if err != nil {
		t.Fatalf("重启后 primary workspace: %v", err)
	}
	if _, ok := restarted.Pins.ProjectPins()["pc::CMAI"]; ok {
		t.Fatal("取消掉的置顶在重启后自己回来了：回填被重复执行了")
	}
}

// worker 角色下，账户表查不到的用户名应该派生 id，走 build 不回空工作区。
// 这钉住方案 B 的核心行为：worker 没有账户表也能做数据分区。
func TestWorkspaceWorkerDerivesAccountID(t *testing.T) {
	cfgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgDir)

	usersPath := filepath.Join(cfgDir, "mindfs", "users.json")
	if err := os.MkdirAll(filepath.Dir(usersPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(usersPath), "login.json"),
		[]byte(`{"password":"root-secret"}`), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	authStore, err := auth.EnsureStoreAt(usersPath)
	if err != nil {
		t.Fatalf("EnsureStoreAt: %v", err)
	}

	prefs, err := preferences.NewStore()
	if err != nil {
		t.Fatalf("preferences: %v", err)
	}
	nodeStore, err := nodes.NewStore()
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	templates, err := kanban.NewTemplateStore()
	if err != nil {
		t.Fatalf("kanban templates: %v", err)
	}
	pool := agent.NewPool(agent.Config{})

	mgr := newWorkspaceManager(context.Background(), sharedServices{
		auth:      authStore,
		prefs:     prefs,
		nodes:     nodeStore,
		webPush:   webpush.NewService(webpush.Config{}, webpush.NewStoreAt(filepath.Dir(usersPath))),
		templates: templates,
		pool:      pool,
	})
	mgr.SetBaseDir(filepath.Join(filepath.Dir(usersPath), "users"))
	mgr.SetRole(nodeinfo.RoleWorker)

	// xiaokubao 不在账户表里（只有 admin），worker 应该派生 id
	username := "xiaokubao"
	ws, err := mgr.Workspace(username)
	if err != nil {
		t.Fatalf("Workspace(%q): %v", username, err)
	}

	// 派生 id 应被使用：项目落在 users/<派生id>/registry.json
	wantID := auth.DeriveAccountID(username)
	registryPath := filepath.Join(filepath.Dir(usersPath), "users", wantID, "registry.json")
	if _, err := ws.Dirs.Upsert(filepath.Join(cfgDir, "proj-worker")); err != nil {
		t.Fatalf("upsert worker project: %v", err)
	}
	if _, err := os.Stat(registryPath); err != nil {
		t.Errorf("worker 项目列表应在 %s: %v", registryPath, err)
	}
}
