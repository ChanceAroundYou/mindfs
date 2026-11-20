package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// handleTaskCompletionGet 是完成证据清单的读口（Plan 2026-11-20 Task 8）。
// 任务不存在 → 404；没有任何完成记录 → {"completion": null}；否则交出报告条目、
// 补做预算与复核结论。清单只读：读的时候不重新求值，避免和当时被驳回的那一轮错位。
func (h *HTTPHandler) handleTaskCompletionGet(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	rootID := taskActivityRootID(r)
	if taskID == "" || rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("task id and root_id required"))
		return
	}
	completion, found, err := svc.TaskCompletionStatus(r.Context(), rootID, taskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(w, http.StatusNotFound, errInvalidRequest("task not found"))
			return
		}
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	if !found {
		respondJSON(w, http.StatusOK, map[string]any{"completion": nil})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"completion": completion})
}
