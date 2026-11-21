package app

import (
	"context"
	"path/filepath"
	"testing"

	"mindfs/server/internal/auth"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/nodes"
)

// 这一组用例钉住的是「现在」的现状，不是目标行为：
// 每台机器的控制面各自为政，两份 configDir 之间没有任何同步。
// 2026-11-21 的离线优先方案（Scope: G-PF）落地后，这些用例仍应成立 ——
// 同步是显式通道，不是「共享同一个目录」；如果哪天它们红了，说明有人
// 把两份状态合成了同一份文件，那会直接破坏主节点/边缘的边界。
//
// 构造顺序是契约的一部分：B 必须在 A 写完**之后**再构造。
// 这三个 Store 都不是每次读盘（auth / nodes 有内存缓存），先构造的 B
// 看不见后来的写入 —— 那样即使两份共享目录，用例也会假绿。

func TestControlPlaneIsolationAuthStores(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	storeA, err := auth.EnsureStoreAt(filepath.Join(dirA, "users.json"))
	if err != nil {
		t.Fatalf("EnsureStoreAt(A): %v", err)
	}
	if _, err := storeA.Create("alice", "password-a", auth.RoleAdmin); err != nil {
		t.Fatalf("A.Create(): %v", err)
	}

	// 先写后建：共享目录时 B 会看到 alice，Create 直接撞 ErrUsernameTaken。
	storeB, err := auth.EnsureStoreAt(filepath.Join(dirB, "users.json"))
	if err != nil {
		t.Fatalf("EnsureStoreAt(B): %v", err)
	}
	if _, err := storeB.Create("alice", "password-b", auth.RoleAdmin); err != nil {
		t.Fatalf("B.Create(): %v", err)
	}

	// 同用户名、不同密码：对方的口令不能登进来。
	if _, err := storeA.Authenticate("alice", "password-b"); err == nil {
		t.Fatal("A 接受了 B 的口令：两份账户表不再是隔离的")
	}
	if _, err := storeB.Authenticate("alice", "password-a"); err == nil {
		t.Fatal("B 接受了 A 的口令：两份账户表不再是隔离的")
	}
	// 各自的口令仍然有效，排除「两边都登不上」这种假绿。
	if _, err := storeA.Authenticate("alice", "password-a"); err != nil {
		t.Fatalf("A 自己的口令登不上：%v", err)
	}
	if _, err := storeB.Authenticate("alice", "password-b"); err != nil {
		t.Fatalf("B 自己的口令登不上：%v", err)
	}
}

func TestControlPlaneIsolationNodeStores(t *testing.T) {
	storeA, err := nodes.NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatalf("NewStoreAt(A): %v", err)
	}
	if _, err := storeA.Upsert(nodes.NodeConnection{
		ID:   "home",
		Name: "home",
		URL:  "http://127.0.0.1:7331",
	}); err != nil {
		t.Fatalf("A.Upsert(): %v", err)
	}

	if got := storeA.List(); len(got) != 1 || got[0].ID != "home" {
		t.Fatalf("A.List() = %+v, want 只有 home", got)
	}

	// 先写后建：共享目录时 B 一加载就会看见 home。
	storeB, err := nodes.NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatalf("NewStoreAt(B): %v", err)
	}
	if got := storeB.List(); len(got) != 0 {
		t.Fatalf("B.List() = %+v, want 空：两份节点表不再是隔离的", got)
	}
}

func TestControlPlaneIsolationWorkflowStores(t *testing.T) {
	storeA := kanban.NewWorkflowStoreAt(t.TempDir())
	ctx := context.Background()
	graph := kanban.WorkflowDefinition{Nodes: []kanban.WorkflowNode{{
		ID:     "notify",
		Kind:   kanban.NodeKindNotify,
		Params: map[string]any{"message": "done"},
	}}}
	if _, err := storeA.CreateDraft(ctx, "wf-isolation", "", "tester", graph); err != nil {
		t.Fatalf("A.CreateDraft(): %v", err)
	}

	draftsA, err := storeA.ListDrafts(ctx)
	if err != nil {
		t.Fatalf("A.ListDrafts(): %v", err)
	}
	if len(draftsA) != 1 || draftsA[0].WorkflowID != "wf-isolation" {
		t.Fatalf("A.ListDrafts() = %+v, want 只有 wf-isolation", draftsA)
	}

	// 先写后建：共享目录时 B 能读到 wf-isolation。
	storeB := kanban.NewWorkflowStoreAt(t.TempDir())
	draftsB, err := storeB.ListDrafts(ctx)
	if err != nil {
		t.Fatalf("B.ListDrafts(): %v", err)
	}
	if len(draftsB) != 0 {
		t.Fatalf("B.ListDrafts() = %+v, want 空：两份工作流库不再是隔离的", draftsB)
	}
}
