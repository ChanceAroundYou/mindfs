package api

import (
	"context"
	"log"
	"net/http"
	"strings"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"
)

func (h *HTTPHandler) kanbanService(w http.ResponseWriter) (*kanban.Service, bool) {
	if h == nil || h.AppContext == nil {
		respondError(w, http.StatusServiceUnavailable, errInvalidRequest("app context unavailable"))
		return nil, false
	}
	svc, err := h.AppContext.GetKanbanService()
	if err != nil {
		respondError(w, http.StatusServiceUnavailable, err)
		return nil, false
	}
	return svc, true
}


func (h *HTTPHandler) bindTaskSessionNames(ctx context.Context, rootID string, detail kanban.TaskDetail) {
	if h == nil || h.AppContext == nil {
		return
	}
	name := kanban.TaskSessionName(detail.Task.Name, detail.Task.TaskNumber)
	if strings.TrimSpace(name) == "" {
		return
	}
	seen := map[string]bool{}
	keys := []string{}
	if key := strings.TrimSpace(detail.Task.MainSessionKey); key != "" {
		keys = append(keys, key)
		seen[key] = true
	}
	for _, run := range detail.StageRuns {
		key := strings.TrimSpace(run.SessionKey)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	uc := &usecase.Service{Registry: h.AppContext}
	for _, key := range keys {
		renamed, err := uc.RenameSession(ctx, usecase.RenameSessionInput{RootID: rootID, Key: key, Name: name})
		if err != nil {
			log.Printf("[task/rename] sync session name skipped root=%s session=%s err=%v", rootID, key, err)
			continue
		}
		h.AppContext.BroadcastSessionMetaUpdated(rootID, renamed)
	}
}


func (h *HTTPHandler) syncTaskNameFromSession(ctx context.Context, rootID, sessionKey, name string) {
	if h == nil || h.AppContext == nil || strings.TrimSpace(name) == "" {
		return
	}
	svc, err := h.AppContext.GetKanbanService()
	if err != nil {
		return
	}
	detail, changed := svc.TaskNameFromSession(ctx, rootID, sessionKey, name)
	if changed {
		h.broadcastTaskUpdated(rootID, detail)
	}
}


func (h *HTTPHandler) detachTaskFromSession(ctx context.Context, rootID string, keys []string) {
	if h == nil || h.AppContext == nil || len(keys) == 0 {
		return
	}
	svc, err := h.AppContext.GetKanbanService()
	if err != nil {
		return
	}
	detail, changed := svc.DetachFromSession(ctx, rootID, keys)
	if changed {
		h.broadcastTaskUpdated(rootID, detail)
	}
}


func (h *HTTPHandler) broadcastTaskUpdated(rootID string, detail kanban.TaskDetail) {
	if h == nil || h.AppContext == nil {
		return
	}
	// 转发给 AppContext.TaskUpdated 而不是就地拼 payload：同一个事件类型曾经有两种
	// 形状（这里只有 task，AppContext 那边还带 detail），前端因此要在两处分别兜。
	// 少带 detail 的那次推送会让详情面板拿不到 stage_runs/events。
	h.AppContext.TaskUpdated(rootID, detail)
}
