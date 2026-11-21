package api

import (
	"net/http"
	"testing"
)

// W3 T2 的传输边界：/workflow-publish 是唯一批准入口；未知卡片 404、非法决定 400。
// 普通留言端点不能批准它 —— 那条规矩由服务层按活动类型校验。
func TestTaskWorkflowPublishRoute(t *testing.T) {
	handler, _, root, taskID := kanbanHTTPFixture(t)
	path := "/api/tasks/" + taskID + "/workflow-publish"

	code, body := postJSON(t, handler, path, `{"root_id":"`+root.ID+`","activity_id":"nope","decision":"approve"}`)
	if code != http.StatusNotFound {
		t.Fatalf("unknown card status=%d body=%s, want 404", code, body)
	}

	code, body = postJSON(t, handler, path, `{"root_id":"`+root.ID+`","activity_id":"nope","decision":"maybe"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("invalid decision status=%d body=%s, want 400", code, body)
	}
}
