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
