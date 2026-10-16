package usecase

import (
	"context"
	"testing"

	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func TestDeleteSessionDeletesSubSessionTree(t *testing.T) {
	ctx := context.Background()
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := session.NewManager(root)
	registry := &commandTestRegistry{root: root, manager: manager}
	service := Service{Registry: registry}

	parent, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, ParentSessionKey: parent.Key, Name: "child"})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	grandchild, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, ParentSessionKey: child.Key, Name: "grandchild"})
	if err != nil {
		t.Fatalf("create grandchild: %v", err)
	}
	sibling, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "sibling"})
	if err != nil {
		t.Fatalf("create sibling: %v", err)
	}

	if _, err := service.DeleteSession(ctx, DeleteSessionInput{RootID: root.ID, Key: parent.Key}); err != nil {
		t.Fatalf("delete parent: %v", err)
	}
	for _, deleted := range []*session.Session{parent, child, grandchild} {
		if _, err := manager.Get(ctx, deleted.Key, 0); err == nil {
			t.Fatalf("session %s still exists", deleted.Key)
		}
	}
	if _, err := manager.Get(ctx, sibling.Key, 0); err != nil {
		t.Fatalf("sibling should remain: %v", err)
	}
}

func TestDeleteSessionKeepsForkSession(t *testing.T) {
	ctx := context.Background()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	service := Service{Registry: &commandTestRegistry{root: root, manager: manager}}

	parent, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	fork, err := manager.Create(ctx, session.CreateInput{
		Type:   session.TypeChat,
		Source: `{"type":"fork"}`,
		Name:   "fork",
	})
	if err != nil {
		t.Fatalf("create fork: %v", err)
	}
	subagent, err := manager.Create(ctx, session.CreateInput{
		Type:             session.TypeChat,
		ParentSessionKey: parent.Key,
		Name:             "subagent",
	})
	if err != nil {
		t.Fatalf("create subagent: %v", err)
	}

	if _, err := service.DeleteSession(ctx, DeleteSessionInput{RootID: root.ID, Key: parent.Key}); err != nil {
		t.Fatalf("delete parent: %v", err)
	}
	if _, err := manager.Get(ctx, parent.Key, 0); err == nil {
		t.Fatal("parent still exists")
	}
	if _, err := manager.Get(ctx, fork.Key, 0); err != nil {
		t.Fatalf("fork should remain: %v", err)
	}
	if _, err := manager.Get(ctx, subagent.Key, 0); err == nil {
		t.Fatal("subagent still exists")
	}
}

func TestDeleteSessionReturnsCascadeKeys(t *testing.T) {
	// 返回值给上层解绑任务引用用：必须含整棵子树，不能只有被点的那个。
	ctx := context.Background()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	service := Service{Registry: &commandTestRegistry{root: root, manager: manager}}

	parent, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, ParentSessionKey: parent.Key, Name: "child"})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	grandchild, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, ParentSessionKey: child.Key, Name: "grandchild"})
	if err != nil {
		t.Fatalf("create grandchild: %v", err)
	}
	sibling, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "sibling"})
	if err != nil {
		t.Fatalf("create sibling: %v", err)
	}

	keys, err := service.DeleteSession(ctx, DeleteSessionInput{RootID: root.ID, Key: parent.Key})
	if err != nil {
		t.Fatalf("delete parent: %v", err)
	}
	got := map[string]bool{}
	for _, key := range keys {
		got[key] = true
	}
	for _, want := range []string{parent.Key, child.Key, grandchild.Key} {
		if !got[want] {
			t.Fatalf("deleted keys %v missing %s", keys, want)
		}
	}
	if got[sibling.Key] {
		t.Fatalf("sibling must not be reported deleted: %v", keys)
	}
}

func TestArchiveSessionDeletesSubtreeButKeepsSelfContent(t *testing.T) {
	ctx := context.Background()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	service := Service{Registry: &commandTestRegistry{root: root, manager: manager}}

	parent, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, ParentSessionKey: parent.Key, Name: "child"})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	grandchild, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, ParentSessionKey: child.Key, Name: "grandchild"})
	if err != nil {
		t.Fatalf("create grandchild: %v", err)
	}
	sibling, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "sibling"})
	if err != nil {
		t.Fatalf("create sibling: %v", err)
	}
	if err := manager.AddExchangeForAgent(ctx, parent, "user", "hello", "", "", "", ""); err != nil {
		t.Fatalf("add exchange: %v", err)
	}

	out, err := service.ArchiveSession(ctx, ArchiveSessionInput{RootID: root.ID, Key: parent.Key, Archived: true})
	if err != nil {
		t.Fatalf("archive parent: %v", err)
	}
	if out.Session == nil || out.Session.ArchivedAt == nil {
		t.Fatal("archive should return the archived session")
	}
	// 父会话：只打归档标记，正文完好（深链接/搜索仍要能打开它）
	reloaded, err := manager.Get(ctx, parent.Key, 0)
	if err != nil {
		t.Fatalf("archived session must still be readable: %v", err)
	}
	if reloaded.ArchivedAt == nil {
		t.Fatal("parent should be archived")
	}
	if len(reloaded.Exchanges) == 0 {
		t.Fatal("archived session lost its exchanges")
	}
	// 子会话：是**删除**，不是归档 —— 两种不同的状态
	gotDeleted := make(map[string]bool)
	for _, key := range out.DeletedKeys {
		gotDeleted[key] = true
	}
	for _, item := range []*session.Session{child, grandchild} {
		if !gotDeleted[item.Key] {
			t.Fatalf("descendant %s should be reported deleted, got %v", item.Key, out.DeletedKeys)
		}
		if _, err := manager.Get(ctx, item.Key, 0); err == nil {
			t.Fatalf("descendant %s should be gone from the store", item.Key)
		}
	}
	if gotDeleted[parent.Key] {
		t.Fatal("the archived session itself must not be reported deleted")
	}
	if other, err := manager.Get(ctx, sibling.Key, 0); err != nil || other.ArchivedAt != nil {
		t.Fatalf("sibling must stay visible: err=%v archivedAt=%v", err, other.ArchivedAt)
	}
}

func TestArchiveSessionUnarchiveRestoresVisibility(t *testing.T) {
	ctx := context.Background()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	service := Service{Registry: &commandTestRegistry{root: root, manager: manager}}

	created, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "s"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.ArchiveSession(ctx, ArchiveSessionInput{RootID: root.ID, Key: created.Key, Archived: true}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := service.ArchiveSession(ctx, ArchiveSessionInput{RootID: root.ID, Key: created.Key, Archived: false}); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	got, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.ArchivedAt != nil {
		t.Fatalf("ArchivedAt = %v, want nil", got.ArchivedAt)
	}
}

func TestArchiveSessionKeepsForkSession(t *testing.T) {
	// 与 TestDeleteSessionKeepsForkSession 对称：fork 只记来源，不是子会话，
	// 所以既不会被归档连带删掉，也不会被取消归档波及。
	ctx := context.Background()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	service := Service{Registry: &commandTestRegistry{root: root, manager: manager}}

	parent, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	fork, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Source: `{"type":"fork"}`, Name: "fork"})
	if err != nil {
		t.Fatalf("create fork: %v", err)
	}
	subagent, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, ParentSessionKey: parent.Key, Name: "subagent"})
	if err != nil {
		t.Fatalf("create subagent: %v", err)
	}

	out, err := service.ArchiveSession(ctx, ArchiveSessionInput{RootID: root.ID, Key: parent.Key, Archived: true})
	if err != nil {
		t.Fatalf("archive parent: %v", err)
	}
	// 子会话是被删掉的（不是被归档）
	if _, err := manager.Get(ctx, subagent.Key, 0); err == nil {
		t.Fatal("subagent should be deleted along with its parent")
	}
	if len(out.DeletedKeys) != 1 || out.DeletedKeys[0] != subagent.Key {
		t.Fatalf("DeletedKeys = %v, want [%s]", out.DeletedKeys, subagent.Key)
	}
	forkGot, err := manager.Get(ctx, fork.Key, 0)
	if err != nil {
		t.Fatalf("fork must remain: %v", err)
	}
	if forkGot.ArchivedAt != nil {
		t.Fatal("fork must not be archived")
	}
}

func TestListSessionsArchivedOnlyViaUsecase(t *testing.T) {
	ctx := context.Background()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	service := Service{Registry: &commandTestRegistry{root: root, manager: manager}}

	visible, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "visible"})
	if err != nil {
		t.Fatalf("create visible: %v", err)
	}
	hidden, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "hidden"})
	if err != nil {
		t.Fatalf("create hidden: %v", err)
	}
	if _, err := service.ArchiveSession(ctx, ArchiveSessionInput{RootID: root.ID, Key: hidden.Key, Archived: true}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	out, err := service.ListSessions(ctx, ListSessionsInput{RootID: root.ID})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out.Sessions) != 1 || out.Sessions[0].Key != visible.Key {
		t.Fatalf("default list = %v, want only %s", keysOfSessions(out.Sessions), visible.Key)
	}
	if out.TotalCount != 1 {
		t.Fatalf("TotalCount = %d, want 1", out.TotalCount)
	}

	archived, err := service.ListSessions(ctx, ListSessionsInput{RootID: root.ID, ArchivedOnly: true})
	if err != nil {
		t.Fatalf("list archived: %v", err)
	}
	if len(archived.Sessions) != 1 || archived.Sessions[0].Key != hidden.Key {
		t.Fatalf("archived list = %v, want only %s", keysOfSessions(archived.Sessions), hidden.Key)
	}
}

func TestArchivePanelListsTopLevelOnly(t *testing.T) {
	// 归档面板按项目平铺，已归档的子会话不能再占一行（它们会在归档时被删掉，
	// 这条只防历史脏数据：老版本把整棵子树都标了归档）。
	ctx := context.Background()
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	service := Service{Registry: &commandTestRegistry{root: root, manager: manager}}

	parent, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Name: "parent"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, ParentSessionKey: parent.Key, Name: "child"})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	for _, item := range []*session.Session{parent, child} {
		if _, err := manager.SetArchived(ctx, item.Key, true); err != nil {
			t.Fatalf("mark archived %s: %v", item.Key, err)
		}
	}

	out, err := service.ListSessions(ctx, ListSessionsInput{
		RootID:       root.ID,
		ArchivedOnly: true,
		TopLevelOnly: true,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out.Sessions) != 1 || out.Sessions[0].Key != parent.Key {
		t.Fatalf("archived top-level list = %v, want only %s", keysOfSessions(out.Sessions), parent.Key)
	}
}
