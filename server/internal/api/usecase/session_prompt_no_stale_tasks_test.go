package usecase

import (
	"context"
	"strings"
	"testing"

	rootfs "mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// 任务组 / 普通模板任务 / orchestration CLI 已在 Phase 3 删除。会话提示里再提一次，
// agent 就会拿不存在的字段去打 POST /api/tasks，拿到 400 后误判成 root 路由故障。
func TestBuildPromptDoesNotRecommendRemovedTaskFeatures(t *testing.T) {
	manager := session.NewManager(rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir()))
	created, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeChat})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	got := (&Service{}).BuildPrompt(BuildPromptInput{
		Session:   created,
		Manager:   manager,
		Agent:     "claude",
		Message:   "hi",
		IsInitial: true,
	})
	for _, banned := range []string{"-orchestration", "task group", "template task", "task_template"} {
		if strings.Contains(got, banned) {
			t.Fatalf("prompt still recommends removed feature %q: %s", banned, got)
		}
	}
	for _, want := range []string{"root_id=mindfs", "session_key=" + created.Key} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt lost context %q: %s", want, got)
		}
	}
}
