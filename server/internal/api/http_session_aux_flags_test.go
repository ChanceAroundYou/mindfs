package api

import (
	"testing"

	"mindfs/server/internal/session"
)

// TestSessionListResponseCarriesAuxFlags 钉住列表行带 aux_flags（G-AD 定制防覆盖
// 测试：合上游时 payload 加列被冲掉，这里先红）。
func TestSessionListResponseCarriesAuxFlags(t *testing.T) {
	sess := &session.Session{
		Key:  "k1",
		Type: session.TypeChat,
		Name: "Chat",
		Aux: session.SessionAuxFlags{
			AskUserWaiting: true,
			HasPlan:        true,
			SessionError:   "boom",
		},
	}
	item := sessionListResponse(sess, "")
	raw, ok := item["aux_flags"].(map[string]any)
	if !ok {
		t.Fatalf("aux_flags missing or wrong shape: %#v", item["aux_flags"])
	}
	if raw["ask_user_waiting"] != true || raw["has_plan"] != true {
		t.Fatalf("aux_flags = %#v, want ask_user_waiting/has_plan true", raw)
	}
	if raw["session_error"] != "boom" {
		t.Fatalf("session_error = %#v, want boom", raw["session_error"])
	}
	if _, ok := raw["has_todos"]; !ok {
		t.Fatalf("aux_flags missing has_todos: %#v", raw)
	}
}
