package usecase

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mindfs/server/internal/session"
)

// 巡检的判据必须是「目录真的不在了」，不能只看 RelatedWorktree 字段非空 —— 那个字段
// 在手工删目录后仍留着，但仍在的目录不该被误搬（会 fork 出新 id、打断恢复）。
func TestDiagnoseRuntimeIssuesOnlyFlagsMissingDirs(t *testing.T) {
	service, manager := newRepointService(t)
	ctx := context.Background()
	root, _ := rootfsRoot(t, service)
	live := filepath.Join(root, "live")
	missing := filepath.Join(root, "gone")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, dir string }{{"live", live}, {"gone", missing}} {
		s, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "codex", Source: "worktree", Name: tc.name})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.RecordRelatedWorktree(ctx, s.Key, "mindfs", tc.dir, "task-"+tc.name, "head"); err != nil {
			t.Fatal(err)
		}
	}
	report, err := service.DiagnoseRuntimeIssues(ctx, "mindfs")
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 2 {
		t.Fatalf("scanned = %d, want 2", report.Scanned)
	}
	if len(report.Issues) != 1 || report.Issues[0].Path != missing {
		t.Fatalf("issues = %+v, want only the missing dir", report.Issues)
	}
}

func TestSweepRuntimeIssuesRepointsMissingDirsAndIsIdempotent(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	service, manager := newRepointService(t)
	ctx := context.Background()
	root, _ := rootfsRoot(t, service)
	missing := filepath.Join(root, "gone")
	s, err := manager.Create(ctx, session.CreateInput{Type: session.TypeChat, Agent: "codex", Source: "worktree", Name: "C"})
	if err != nil {
		t.Fatal(err)
	}
	const threadID = "sweep-thread-1"
	if err := manager.UpsertAgentBinding(ctx, session.AgentBinding{SessionKey: s.Key, Agent: "codex", AgentSessionID: threadID}); err != nil {
		t.Fatal(err)
	}
	if err := manager.AddExchangeForAgent(ctx, s, "assistant", "hello", "codex", "", threadID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RecordRelatedWorktree(ctx, s.Key, "mindfs", missing, "task-c", "head"); err != nil {
		t.Fatal(err)
	}
	first, err := service.SweepRuntimeIssues(ctx, "mindfs")
	if err != nil {
		t.Fatal(err)
	}
	if first.Repaired != 1 || !strings.Contains(strings.Join(first.Details, "\n"), s.Key) {
		t.Fatalf("sweep = %+v, want one repair of %s", first, s.Key)
	}
	second, err := service.SweepRuntimeIssues(ctx, "mindfs")
	if err != nil {
		t.Fatal(err)
	}
	if second.Repaired != 0 {
		t.Fatalf("second sweep repaired %d, want 0 (idempotent)", second.Repaired)
	}
}
