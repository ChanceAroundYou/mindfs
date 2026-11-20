package api

import (
	"testing"

	"mindfs/server/internal/session"
)

// 侧栏按任务分组靠 task_session_count 判断「这个任务是不是只有一条会话」，
// 而不是看当前这一页装了几条（多项目侧栏每根只取 6 条）。
func TestSessionListResponseCarriesTaskSessionCount(t *testing.T) {
	item := sessionListResponse(&session.Session{Key: "a", TaskID: "task_x", TaskSessionCount: 4}, "")
	if got := item["task_session_count"]; got != 4 {
		t.Fatalf("task_session_count = %v, want 4", got)
	}

	// 单条读取与广播路径算不出计数（恒 0）：这时**不下发**这个键。下发 0 会让前端
	// 把「不知道」当成「确认只有一条」，分组在增量合并后当场消失。
	bare := sessionListResponse(&session.Session{Key: "a", TaskID: "task_x"}, "")
	if _, ok := bare["task_session_count"]; ok {
		t.Fatalf("未统计计数时不应下发 task_session_count，得到 %v", bare["task_session_count"])
	}
}
