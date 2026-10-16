package api

import (
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/session"
)

// WorkDir 要能一路走到前端，必须穿过三处**白名单** payload：会话列表、会话详情、
// WS session.meta.updated 广播。任何一处漏掉，换过目录的会话在标题栏里就显示不出
// 新目录（列表漏 → 侧栏与标题栏都对；详情漏 → 打开会话那一次错；广播漏 → 换完目录
// 不刷新）。三处各自独立、都能单独漏，所以三处都钉住。
func TestSessionPayloadsCarryWorkDir(t *testing.T) {
	const dir = "/home/xiaokubao/projects/mindfs/sub"
	sess := &session.Session{Key: "k1", Type: session.TypeChat, Name: "Chat", WorkDir: dir}

	if got := sessionListResponse(sess, "")["work_dir"]; got != dir {
		t.Fatalf("list payload work_dir = %#v, want %q", got, dir)
	}

	h := &HTTPHandler{}
	detail := h.sessionResponse(sess, agenttypes.ContextWindow{}, nil, nil, nil)
	if got := detail["work_dir"]; got != dir {
		t.Fatalf("detail payload work_dir = %#v, want %q", got, dir)
	}

	if got := sessionMetaUpdatedPayload(sess)["work_dir"]; got != dir {
		t.Fatalf("session.meta.updated work_dir = %#v, want %q", got, dir)
	}
}
