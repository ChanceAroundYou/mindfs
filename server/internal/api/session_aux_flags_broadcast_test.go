package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

// 问号徽标（会话行 aux_flags.ask_user_waiting）必须**亮灭都实时**。
//
// 症状（2026-10-20 用户实测）：提问后徽标亮，但「我明明已经回答了，徽标没立刻消失，
// 要会话结束才更新」。根因是 aux 写入路径不广播：列表项里的 aux_flags 是取列表那一刻
// 的快照，不推的话前端一直拿着旧值。
//
// 这条测试钉住两件事：
//  1. aux 写入（置位/清位）后必须广播 session.meta.updated，且 payload 里带最新 aux_flags；
//  2. 答题路径（clearSessionAskUserWaiting）必须把会话行清位 —— 库里的真相要立刻改，
//     否则任何一次后续列表重拉都会把徽标点亮回来。

func auxFlagFromBroadcast(t *testing.T, resp WSResponse, wantKey string) map[string]any {
	t.Helper()
	payload := resp.Payload
	if payload == nil {
		t.Fatalf("broadcast carried no payload: %#v", resp)
	}
	raw, _ := json.Marshal(payload["session"])
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("decode session payload: %v", err)
	}
	if got, _ := item["key"].(string); got != wantKey {
		t.Fatalf("session.key = %q, want %q", got, wantKey)
	}
	flags, _ := item["aux_flags"].(map[string]any)
	if flags == nil {
		t.Fatalf("broadcast session payload has no aux_flags: %#v", item)
	}
	return flags
}

func TestSessionAuxFlagsWriteBroadcastsMetaUpdated(t *testing.T) {
	registry := fs.NewRegistry(filepath.Join(t.TempDir(), "registry.json"))
	root, err := registry.Upsert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app := &AppContext{Dirs: registry}
	manager, err := app.GetSessionManager(root.ID)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := manager.Create(context.Background(), session.CreateInput{Type: session.TypeChat})
	if err != nil {
		t.Fatal(err)
	}
	conn := dialHub(t, app.GetSessionStreamHub())

	yes := true
	app.updateSessionAuxFlags(root.ID, chat.Key, session.SessionAuxFlagsPatch{AskUserWaiting: &yes})
	flags := auxFlagFromBroadcast(t, readBroadcast(t, conn, "session.meta.updated"), chat.Key)
	if flags["ask_user_waiting"] != true {
		t.Fatalf("ask_user_waiting = %#v, want true（置位必须广播）", flags["ask_user_waiting"])
	}

	// 答题路径：清位 + 广播。
	app.clearSessionAskUserWaiting(root.ID, chat.Key)
	flags = auxFlagFromBroadcast(t, readBroadcast(t, conn, "session.meta.updated"), chat.Key)
	if flags["ask_user_waiting"] != false {
		t.Fatalf("ask_user_waiting = %#v, want false（答题必须立刻清位并广播）", flags["ask_user_waiting"])
	}

	// 库里的真相也要立刻是「不在等」—— 否则下一次列表重拉会把徽标点亮回来。
	reloaded, err := manager.GetMeta(context.Background(), chat.Key)
	if err != nil || reloaded == nil {
		t.Fatalf("get meta: %v", err)
	}
	if reloaded.Aux.AskUserWaiting {
		t.Fatalf("会话行仍写着 ask_user_waiting=true：徽标会在下次列表重拉时复活")
	}
}

// 答案提交的那条路（WS session.answer_question）必须调用上面那个清位入口。
// 这里用源码守卫钉住：改路径时忘了清会话行，徽标就会退回「亮到回合结束」。
//
// 为什么是源码守卫而不是行为测试：这条路的成功分支要求**活着的 agent 会话**
// （usecase.AnswerQuestion → pool.Get），测试里造不出来；而漏掉清理的形状恰恰是
// 「回答成功了但徽标不灭」，只能靠守卫盯住那一句调用。
func TestAnswerQuestionHandlerClearsSessionAskUserWaiting(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "ws.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	start := strings.Index(text, "func (h *WSHandler) handleSessionAnswerQuestion(")
	if start < 0 {
		t.Fatal("handleSessionAnswerQuestion not found in ws.go")
	}
	end := strings.Index(text[start:], "\nfunc ")
	if end < 0 {
		end = len(text) - start
	}
	body := text[start : start+end]
	if !strings.Contains(body, "clearSessionAskUserWaiting(") {
		t.Fatalf("handleSessionAnswerQuestion 没有清会话行的 ask_user 徽标：\n%s", body)
	}
}
