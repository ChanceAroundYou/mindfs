package session

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	rootfs "mindfs/server/internal/fs"
)

func TestManagerUsesSessionDBLink(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("mindfs", "mindfs", rootDir)
	manager := NewManager(root)

	linkedDB := filepath.Join(t.TempDir(), "session-list.db")
	linkFile := filepath.Join(root.MetaDir(), "sessions", "session-list.db.link")
	if err := writeSessionDBLink(linkFile, linkedDB); err != nil {
		t.Fatalf("write link: %v", err)
	}

	if _, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Linked"}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root.MetaDir(), "sessions", "session-list.db")); err == nil {
		t.Fatalf("legacy session-list.db should not be created when link exists")
	}
	if _, err := os.Stat(linkedDB); err != nil {
		t.Fatalf("stat linked db: %v", err)
	}
}

func TestManagerExternalCursorSurvivesAgentStateUpdate(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)
	created, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Agent: "codex", Name: "Cursor"})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.UpsertAgentBinding(context.Background(), AgentBinding{SessionKey: created.Key, Agent: "codex", AgentSessionID: "external-1"}); err != nil {
		t.Fatal(err)
	}
	cursor := agenttypes.ExternalSessionCursor{SourcePath: "/tmp/session.jsonl", Offset: 1234, ModTimeUnixNano: 5678}
	if err := manager.UpdateExternalSessionCursor(context.Background(), created.Key, "codex", cursor); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateAgentState(context.Background(), created, "codex", 3, "external-1"); err != nil {
		t.Fatal(err)
	}
	binding, err := manager.GetAgentBinding(context.Background(), created.Key, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if binding.ExternalCursor() != cursor {
		t.Fatalf("cursor = %#v, want %#v", binding.ExternalCursor(), cursor)
	}
}

func TestManagerFallsBackToUserDataSessionDBOnSQLitePanic(t *testing.T) {
	rootDir := t.TempDir()
	root := rootfs.NewRootInfo("panic-root", "panic-root", rootDir)
	manager := NewManager(root)

	originalOpen := openSQLiteDB
	originalConfigDir := mindFSConfigDir
	defer func() {
		openSQLiteDB = originalOpen
		mindFSConfigDir = originalConfigDir
	}()
	configDir := t.TempDir()
	mindFSConfigDir = func() (string, error) {
		return configDir, nil
	}

	var opened []string
	openSQLiteDB = func(path string) (*sql.DB, error) {
		opened = append(opened, path)
		if strings.Contains(path, rootDir) {
			panic("sqlite legacy panic")
		}
		return originalOpen(path)
	}

	if _, err := manager.Create(context.Background(), CreateInput{Type: TypeChat, Name: "Fallback"}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if len(opened) < 2 {
		t.Fatalf("opened paths = %#v, want legacy then fallback", opened)
	}
	linkFile := filepath.Join(root.MetaDir(), "sessions", "session-list.db.link")
	payload, err := root.ReadMetaFile("sessions/session-list.db.link")
	if err != nil {
		t.Fatalf("read link: %v", err)
	}
	linked := strings.TrimSpace(string(payload))
	if linked == "" || strings.Contains(linked, rootDir) {
		t.Fatalf("link target = %q, want user-data path", linked)
	}
	if got, ok, err := readSessionDBLink(linkFile); err != nil || !ok || got != linked {
		t.Fatalf("readSessionDBLink = %q, %v, %v; want %q, true, nil", got, ok, err, linked)
	}
}

func TestManagerPersistsParentSessionMetadata(t *testing.T) {
	root := rootfs.NewRootInfo("mindfs", "mindfs", t.TempDir())
	manager := NewManager(root)

	created, err := manager.Create(context.Background(), CreateInput{
		Type:             TypeChat,
		ParentSessionKey: "parent-session",
		ParentToolCallID: "tool-call-1",
		Agent:            "codex",
		Model:            "gpt-test",
		Name:             "Subagent",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	loaded, err := manager.Get(context.Background(), created.Key, 0)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if loaded.ParentSessionKey != "parent-session" {
		t.Fatalf("ParentSessionKey = %q", loaded.ParentSessionKey)
	}
	if loaded.ParentToolCallID != "tool-call-1" {
		t.Fatalf("ParentToolCallID = %q", loaded.ParentToolCallID)
	}
}

func TestSessionIsLiveOwned(t *testing.T) {
	cases := []struct {
		name      string
		exchanges []Exchange
		want      bool
	}{
		{"空会话", nil, false},
		{"只有导入行", []Exchange{{Source: ExchangeSourceImport, LegacyContent: "x"}, {Source: ExchangeSourceImport, LegacyContent: "y"}}, false},
		{"有实时行", []Exchange{{Source: ExchangeSourceImport, LegacyContent: "x"}, {Source: ExchangeSourceLive, LegacyContent: "y"}}, true},
		// 老数据（无 Source 标注）用实时路径独有签名兜底
		{"老数据带 model_display_name", []Exchange{{ModelDisplayName: "Sonnet 4.5", LegacyContent: "x"}}, true},
		{"老数据带 token_usage", []Exchange{{TokenUsage: &agenttypes.TokenUsage{InputTokens: 1}, LegacyContent: "x"}}, true},
		{"老数据无签名", []Exchange{{LegacyContent: "x"}, {LegacyContent: "y"}}, false},
		// 明确标了 import 的行不参与签名兜底（避免把导入行误判成实时行）
		{"导入行带残留字段", []Exchange{{Source: ExchangeSourceImport, ModelDisplayName: "Sonnet 4.5", LegacyContent: "x"}}, false},
	}
	for _, tc := range cases {
		if got := SessionIsLiveOwned(tc.exchanges); got != tc.want {
			t.Fatalf("%s: SessionIsLiveOwned = %v, want %v", tc.name, got, tc.want)
		}
	}
}
