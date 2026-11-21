package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"

	types "mindfs/server/internal/agent/types"
)

func TestClaudeCompactNoticeCarriesPostTokensFromTranscript(t *testing.T) {
	configDir := t.TempDir()
	rootPath := filepath.Join(t.TempDir(), "project")
	sessionID := "claude-session-post-tokens"
	boundaryID := "compact-post-tokens-1"
	projectDir := filepath.Join(configDir, "projects", claudeProjectDirName(rootPath))
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := fmt.Sprintf(
		`{"type":"system","subtype":"compact_boundary","uuid":%q,"session_id":%q,"compactMetadata":{"trigger":"auto","preTokens":1200,"postTokens":4400}}`+"\n",
		boundaryID, sessionID,
	)
	if err := os.WriteFile(filepath.Join(projectDir, sessionID+".jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	var got types.Event
	s := &session{
		sessionID: sessionID,
		rootPath:  rootPath,
		baseEnv:   map[string]string{"CLAUDE_CONFIG_DIR": configDir},
		onUpdate:  func(event types.Event) { got = event },
	}
	s.handleCompactBoundaryMessage(claudeagent.CompactBoundaryMessage{
		UUID:      boundaryID,
		SessionID: sessionID,
		CompactMetadata: claudeagent.CompactMetadata{
			Trigger:   "auto",
			PreTokens: 1200,
		},
	})

	if got.Type != types.EventTypeCompact {
		t.Fatalf("event type = %q, want compact", got.Type)
	}
	notice, ok := got.Data.(types.CompactNotice)
	if !ok {
		t.Fatalf("event data = %T, want CompactNotice", got.Data)
	}
	if notice.PostTokens != 4400 {
		t.Fatalf("notice.PostTokens = %d, want 4400: %#v", notice.PostTokens, notice)
	}
}

func TestClaudeCompactNoticeIgnoresOlderTranscriptBoundary(t *testing.T) {
	configDir := t.TempDir()
	rootPath := filepath.Join(t.TempDir(), "project")
	sessionID := "claude-session-post-tokens-old"
	projectDir := filepath.Join(configDir, "projects", claudeProjectDirName(rootPath))
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := strings.Join([]string{
		`{"type":"system","subtype":"compact_boundary","uuid":"old","session_id":"` + sessionID + `","compactMetadata":{"postTokens":9999}}`,
		`{"type":"system","subtype":"compact_boundary","uuid":"new","session_id":"` + sessionID + `","compactMetadata":{"postTokens":4200}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, sessionID+".jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	var got types.Event
	s := &session{
		sessionID: sessionID,
		rootPath:  rootPath,
		baseEnv:   map[string]string{"CLAUDE_CONFIG_DIR": configDir},
		onUpdate:  func(event types.Event) { got = event },
	}
	s.handleCompactBoundaryMessage(claudeagent.CompactBoundaryMessage{
		UUID:            "new",
		SessionID:       sessionID,
		CompactMetadata: claudeagent.CompactMetadata{Trigger: "auto", PreTokens: 1200},
	})

	notice := got.Data.(types.CompactNotice)
	if notice.PostTokens != 4200 {
		t.Fatalf("notice.PostTokens = %d, want 4200 (the boundary's own line)", notice.PostTokens)
	}
}
