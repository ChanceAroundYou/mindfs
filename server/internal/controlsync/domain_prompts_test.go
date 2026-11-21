package controlsync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"mindfs/server/internal/api/usecase"
)

func newTestPromptsDomain(t *testing.T, configDir string) *PromptsDomain {
	t.Helper()
	domain, err := NewPromptsDomain(configDir)
	if err != nil {
		t.Fatalf("NewPromptsDomain: %v", err)
	}
	return domain
}

func TestPromptsDomainSnapshotKeysByAccountAndText(t *testing.T) {
	dir := t.TempDir()
	domain := newTestPromptsDomain(t, dir)
	mainStore, err := usecase.NewPromptStoreAt(dir)
	if err != nil {
		t.Fatalf("NewPromptStoreAt: %v", err)
	}
	if _, err := mainStore.Append("总结这次改动"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	otherStore, err := usecase.NewPromptStoreAt(dir + "/users/u_abc")
	if err != nil {
		t.Fatalf("NewPromptStoreAt(other): %v", err)
	}
	if _, err := otherStore.Append("总结这次改动"); err != nil {
		t.Fatalf("Append(other): %v", err)
	}

	recs, err := domain.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2（同名账户里的同一条提示词必须分成两条记录）", len(recs))
	}
	keys := map[string]bool{}
	for _, rec := range recs {
		keys[rec.Key] = true
	}
	for _, want := range []string{"main/sha256:", "u_abc/sha256:"} {
		found := false
		for k := range keys {
			if len(k) > len(want) && k[:len(want)] == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("缺少 key 前缀 %q：%v", want, keys)
		}
	}
}

func TestPromptsDomainApplyRoundTripAndTombstone(t *testing.T) {
	sourceDir, targetDir := t.TempDir(), t.TempDir()
	sourceStore, err := usecase.NewPromptStoreAt(sourceDir + "/users/u_abc")
	if err != nil {
		t.Fatalf("NewPromptStoreAt: %v", err)
	}
	if _, err := sourceStore.Append("继续跑门禁"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	recs, err := newTestPromptsDomain(t, sourceDir).Snapshot(context.Background())
	if err != nil || len(recs) != 1 {
		t.Fatalf("Snapshot = %v, %v", recs, err)
	}

	domain := newTestPromptsDomain(t, targetDir)
	if err := domain.Apply(context.Background(), recs[0]); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := domain.Apply(context.Background(), recs[0]); err != nil {
		t.Fatalf("Apply 幂等: %v", err)
	}
	targetStore, err := usecase.NewPromptStoreAt(targetDir + "/users/u_abc")
	if err != nil {
		t.Fatalf("NewPromptStoreAt(target): %v", err)
	}
	items, err := targetStore.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(items) != 1 || items[0] != "继续跑门禁" {
		t.Fatalf("目标提示词库 = %v", items)
	}

	dead := recs[0]
	dead.Deleted = true
	dead.Payload = nil
	if err := domain.Apply(context.Background(), dead); err != nil {
		t.Fatalf("Apply tombstone: %v", err)
	}
	if items, err := targetStore.Load(); err != nil || len(items) != 0 {
		t.Fatalf("墓碑之后仍有 %v（err=%v）", items, err)
	}
}

func TestPromptsDomainRejectsPathTraversalAccount(t *testing.T) {
	dir := t.TempDir()
	domain := newTestPromptsDomain(t, dir)
	// key 与 payload 一致，唯一能拦下它的就是账户名白名单。
	key := PromptRecordKey("../../escape", "x")
	rec, err := newRecord(DomainPrompts, key, promptPayload{Account: "../../escape", Text: "x"})
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	if err := domain.Apply(context.Background(), rec); err == nil {
		t.Fatalf("账户名里的 ../ 没被拒绝：远端记录能把文件写到配置目录外面")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape", "prompts.json")); err == nil {
		t.Fatalf("配置目录外面真的被写了一个 prompts.json")
	}
}

func TestPromptsDomainRejectsKeyPayloadMismatch(t *testing.T) {
	dir := t.TempDir()
	domain := newTestPromptsDomain(t, dir)
	rec, err := newRecord(DomainPrompts, "main/sha256:deadbeef", promptPayload{Account: "main", Text: "另一个文本"})
	if err != nil {
		t.Fatalf("newRecord: %v", err)
	}
	if err := domain.Apply(context.Background(), rec); err == nil {
		t.Fatalf("key 与 payload 文本不一致居然被接受")
	}
	if items, err := usecase.NewPromptStoreAt(dir); err != nil {
		t.Fatalf("NewPromptStoreAt: %v", err)
	} else if loaded, err := items.Load(); err != nil || len(loaded) != 0 {
		t.Fatalf("被拒的记录仍然写进了提示词库：%v (err=%v)", loaded, err)
	}
}
