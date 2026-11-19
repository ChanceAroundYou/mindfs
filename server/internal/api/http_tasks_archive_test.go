package api

import (
	"context"
	"sort"
	"testing"
	"time"

	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// 任务归档的会话级联范围（2026-11-20 用户裁定「全部会话，含子会话」）：
// 目标 = 种子 ∪ task_id 命中的会话 ∪ 按 parent_session_key 递归到的全部后代。
func TestTaskArchiveTargetKeysCollectsTaskOwnedAndDescendants(t *testing.T) {
	metas := []*session.Session{
		// 该任务名下的两个顶层会话（新数据都带 task_id）。
		{Key: "sibling-a", TaskID: "t1"},
		{Key: "sibling-b", TaskID: "t1"},
		// sibling-a 的子会话：subagent 派生，task_id 为空，只能靠父子链找到。
		{Key: "child-a", ParentSessionKey: "sibling-a"},
		// 孙会话，验证递归而不是只收一层。
		{Key: "grandchild-a", ParentSessionKey: "child-a"},
		// 老会话：task_id 为空，但作为种子被显式传入，且有自己的子会话。
		{Key: "legacy-main", ParentSessionKey: ""},
		{Key: "legacy-child", ParentSessionKey: "legacy-main"},
		// 无关会话，绝不能进目标集。
		{Key: "other-task", TaskID: "t2", ParentSessionKey: ""},
	}

	got := taskArchiveTargetKeys(metas, "t1", []string{"legacy-main"})
	sort.Strings(got)

	want := []string{"child-a", "grandchild-a", "legacy-child", "legacy-main", "sibling-a", "sibling-b"}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets = %v, want %v", got, want)
		}
	}
	for _, key := range got {
		if key == "other-task" {
			t.Fatal("unrelated task's session leaked into archive targets")
		}
	}
}

// 种子 key 重复、或种子本身已被 task_id 命中时，目标集不能出现重复项。
func TestTaskArchiveTargetKeysDedupes(t *testing.T) {
	metas := []*session.Session{
		{Key: "s1", TaskID: "t1"},
	}
	got := taskArchiveTargetKeys(metas, "t1", []string{"s1", "s1", "  "})
	if len(got) != 1 || got[0] != "s1" {
		t.Fatalf("targets = %v, want [s1]", got)
	}
}

// 空任务 id + 空种子 = 空目标集（不能因为 taskID 为空就去匹配 task_id=” 的会话）。
func TestTaskArchiveTargetKeysEmptyInputs(t *testing.T) {
	metas := []*session.Session{{Key: "loose", TaskID: ""}}
	if got := taskArchiveTargetKeys(metas, "", nil); len(got) != 0 {
		t.Fatalf("targets = %v, want empty (empty taskID must not match unowned sessions)", got)
	}
}

// 级联真正落库：用真实 session.Manager 归档一遍，被 task_id 命中/种子/子会话的
// 全部打上 archived_at，无关任务的会话原样不动。
//
// 这条替换掉「只测纯函数」的缺口：用户报的症状是「任务归档了、会话还在侧栏」，
// 而当时的真实缺陷正是级联只覆盖了种子 key、没有按 task_id/父子链收齐。
type fixedArchiveBackend struct {
	manager *session.Manager
	hub     *StreamHub
}

func (b fixedArchiveBackend) GetSessionManager(string) (*session.Manager, error) {
	return b.manager, nil
}

func (b fixedArchiveBackend) GetSessionStreamHub() *StreamHub { return b.hub }

func TestTaskSessionArchiverMarksTaskSessionsAndDescendants(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	ctx := context.Background()
	create := func(in session.CreateInput) *session.Session {
		t.Helper()
		sess, err := manager.Create(ctx, in)
		if err != nil {
			t.Fatalf("create %s: %v", in.Key, err)
		}
		return sess
	}
	create(session.CreateInput{Key: "owner", Type: session.TypeChat, TaskID: "task-1", Name: "owner"})
	create(session.CreateInput{Key: "child", Type: session.TypeChat, ParentSessionKey: "owner", Name: "child"})
	create(session.CreateInput{Key: "legacy", Type: session.TypeChat, Name: "legacy"})
	create(session.CreateInput{Key: "legacy-child", Type: session.TypeChat, ParentSessionKey: "legacy", Name: "legacy-child"})
	create(session.CreateInput{Key: "other", Type: session.TypeChat, TaskID: "task-2", Name: "other"})

	archiver := NewTaskSessionArchiver(fixedArchiveBackend{manager: manager, hub: NewStreamHub()})
	if err := archiver.ArchiveSessions(ctx, root.ID, "task-1", []string{"legacy"}); err != nil {
		t.Fatalf("archive sessions: %v", err)
	}

	checker := session.NewManager(root)
	for _, key := range []string{"owner", "child", "legacy", "legacy-child"} {
		sess, err := checker.Get(ctx, key, 0)
		if err != nil {
			t.Fatalf("reload %s: %v", key, err)
		}
		if sess.ArchivedAt == nil {
			t.Fatalf("%s should be archived by task cascade", key)
		}
	}
	other, err := checker.Get(ctx, "other", 0)
	if err != nil {
		t.Fatalf("reload other: %v", err)
	}
	if other.ArchivedAt != nil {
		t.Fatal("session of another task must not be archived")
	}
}

// 广播载荷必须带 archived_at：前端就是靠它把会话从侧栏摘掉的（缺了这条，
// 库里已归档、界面还挂着，正是用户报的那个样子）。
func TestSessionMetaUpdatedPayloadCarriesArchivedAt(t *testing.T) {
	payload := sessionMetaUpdatedPayload(&session.Session{Key: "s1"})
	// map[string]any 里存的是 **typed nil**（*time.Time），直接和 nil 比会得到 false，
	// 必须按类型断言再判空。JSON 序列化后仍是 null，前端看到的就是「未归档」。
	if v, ok := payload["archived_at"].(*time.Time); !ok || v != nil {
		t.Fatalf("archived_at key missing or non-nil on live session: %#v", payload["archived_at"])
	}
	now := time.Now().UTC()
	archived := sessionMetaUpdatedPayload(&session.Session{Key: "s1", ArchivedAt: &now})
	if archived["archived_at"] == nil {
		t.Fatal("archived session payload must carry archived_at, otherwise the sidebar never prunes it")
	}
}

// 端到端（进程内）验证收敛通道：真的挂一条 WebSocket 到 StreamHub 上，再跑一次
// 任务归档级联，客户端必须收到 session.meta.updated，且 payload.session.archived_at
// 非空 —— 前端就靠这一帧把会话从侧栏摘掉。
//
// 走真连接而不是直接摸 hub 内部：BroadcastAll → SendToClient → WriteJSON 这条链
// 上任何一处没把 archived_at 带出去，这条都会红（与 session_created_broadcast_test.go
// 同一套 dialHub/readBroadcast 帮手）。
func TestArchiveSessionsBroadcastsArchivedMeta(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := session.NewManager(root)
	ctx := context.Background()
	if _, err := manager.Create(ctx, session.CreateInput{Key: "s1", Type: session.TypeChat, TaskID: "task-1", Name: "会话"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	hub := NewStreamHub()
	conn := dialHub(t, hub)
	archiver := NewTaskSessionArchiver(fixedArchiveBackend{manager: manager, hub: hub})
	if err := archiver.ArchiveSessions(ctx, root.ID, "task-1", nil); err != nil {
		t.Fatalf("archive sessions: %v", err)
	}

	resp := readBroadcast(t, conn, "session.meta.updated")
	sessPayload, ok := resp.Payload["session"].(map[string]any)
	if !ok {
		t.Fatalf("payload.session missing: %#v", resp.Payload)
	}
	if sessPayload["key"] != "s1" {
		t.Fatalf("broadcast session key = %v, want s1", sessPayload["key"])
	}
	if sessPayload["archived_at"] == nil {
		t.Fatalf("archived_at missing in broadcast payload: %#v", sessPayload)
	}
}
