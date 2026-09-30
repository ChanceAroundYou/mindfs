package app

import (
	"errors"
	"os"
	"testing"

	"mindfs/server/internal/api"
	"mindfs/server/internal/auth"
)

// 未知账户必须解析成「空工作区」，而不是报错——账户只是可见性设置。
// 这是本次简化的核心行为：换个节点看不到项目 = 空，不是一串报错。
func TestWorkspaceForUnknownAccountIsEmpty(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.EnsureStoreAt(dir + "/users.json")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	m := &workspaceManager{
		baseDir: dir + "/users",
		entries: map[string]*workspaceEntry{},
		shared:  sharedServices{auth: store},
	}

	// 完全不认识的账户：应拿到空工作区，且不报错
	ctx, err := m.Workspace("u_nobody_here")
	if err != nil {
		t.Fatalf("未知账户不应报错（账户只是可见性设置），got: %v", err)
	}
	if ctx == nil {
		t.Fatal("未知账户应返回一个（空的）工作区")
	}
	if roots := ctx.ListRoots(); len(roots) != 0 {
		t.Errorf("未知账户 ListRoots() = %d 项，want 0", len(roots))
	}
	// 不应因为来个陌生 user= 就建出目录
	if _, statErr := os.Stat(dir + "/users/u_nobody_here"); statErr == nil {
		t.Error("未知账户不应在磁盘上建出目录")
	}
}

// 空工作区必须**挂上共享服务**，否则凡是依赖它们的端点都会回配置错误而不是空数据。
// 实测踩过：登录后看板直接报 "kanban service not configured"——语义应是「这个账户没有数据」。
// 注意这里只断言「服务在」，不起 Start/Schedule（空注册表下也没有 root 可调度）。
func TestEmptyWorkspaceHasServicesWired(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.EnsureStoreAt(dir + "/users.json")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	m := &workspaceManager{
		baseDir: dir + "/users",
		entries: map[string]*workspaceEntry{},
		shared:  sharedServices{auth: store},
	}

	ctx, err := m.Workspace("u_nobody_here")
	if err != nil {
		t.Fatalf("Workspace: %v", err)
	}
	if _, err := ctx.GetKanbanService(); err != nil {
		t.Errorf("空工作区必须有 Kanban 服务（否则看板端点回 503 而不是空列表）: %v", err)
	}
	if ctx.Scheduled == nil {
		t.Error("空工作区必须有 Scheduled 服务（否则定时任务端点回 503）")
	}
	if ctx.GitHub == nil {
		t.Error("空工作区必须有 GitHub 服务")
	}
	// 共享实例必须是同一份，不是各建各的
	if ctx.Auth != m.shared.auth {
		t.Error("空工作区的 Auth 应指向共享实例")
	}
}

// 路径穿越必须被拒（user= 由客户端自由填写），且同样返回空工作区而非报错。
func TestWorkspaceRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.EnsureStoreAt(dir + "/users.json")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	m := &workspaceManager{
		baseDir: dir + "/users",
		entries: map[string]*workspaceEntry{},
		shared:  sharedServices{auth: store},
	}
	for _, bad := range []string{"../etc", "..", ".", "a/b"} {
		ctx, err := m.Workspace(bad)
		if err != nil {
			t.Errorf("Workspace(%q) 不该报错，got: %v", bad, err)
			continue
		}
		if ctx == nil || len(ctx.ListRoots()) != 0 {
			t.Errorf("Workspace(%q) 应返回空工作区", bad)
		}
	}
}

// 未知账户不进 entries 缓存，避免随便造 user= 撑爆内存。
func TestUnknownAccountNotCached(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.EnsureStoreAt(dir + "/users.json")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	m := &workspaceManager{
		baseDir: dir + "/users",
		entries: map[string]*workspaceEntry{},
		shared:  sharedServices{auth: store},
	}
	for i := 0; i < 50; i++ {
		if _, err := m.Workspace("u_fake_" + string(rune('a'+i%26)) + string(rune('a'+i/26))); err != nil {
			t.Fatalf("Workspace: %v", err)
		}
	}
	if len(m.entries) != 0 {
		t.Errorf("entries 缓存了 %d 个未解析账户，want 0（会被随便造的 user= 撑爆）", len(m.entries))
	}
}

// 账户被删（本机有目录、但不在账户表里）必须继续回 404：
// 前端靠它把用户送回登录页，否则账户删掉后整页空白又退不出去。
// 这与「别的机器的账户」（无目录 → 空列表）是相反的处理，别搞混。
func TestDeletedLocalAccountStillUnknownUser(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.EnsureStoreAt(dir + "/users.json")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	m := &workspaceManager{
		baseDir: dir + "/users",
		entries: map[string]*workspaceEntry{},
		shared:  sharedServices{auth: store},
	}

	// 模拟「曾经存在、后来被删」：留下数据目录，但账户表里没有
	ghost := "u_deleted_account"
	if err := os.MkdirAll(dir+"/users/"+ghost, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := m.Workspace(ghost); err == nil {
		t.Fatal("本机有数据目录但不在账户表 = 账户被删，必须回错（前端据此登出）")
	} else if !errors.Is(err, api.ErrUnknownUser) {
		t.Fatalf("err = %v, want ErrUnknownUser（ScopedRouter 靠它映射 404）", err)
	}
}
