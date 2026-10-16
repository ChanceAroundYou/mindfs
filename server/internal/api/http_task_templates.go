package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"mindfs/server/internal/kanban"

	"github.com/go-chi/chi/v5"
)

func (h *HTTPHandler) handleTaskStageTemplatesList(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	items, err := svc.ListStageTemplates(r.Context())
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *HTTPHandler) handleTaskStageTemplateSave(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req kanban.StageTemplate
	if err := json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	item, err := svc.SaveStageTemplate(r.Context(), req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, item)
}

func (h *HTTPHandler) handleTaskStageTemplateDelete(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	if err := svc.DeleteStageTemplate(r.Context(), chi.URLParam(r, "id")); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) handleTaskTemplatesList(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	// root 缺省 = 只看全局模板。模板库只有主节点一份（docs/multi-node-control-plane.md），
	// 前端建任务时总带着当前项目，所以这里按项目过滤而不是全量返回。
	items, err := svc.ListTaskTemplatesForRoot(r.Context(), r.URL.Query().Get("root"))
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSONConditional(w, r, map[string]any{"items": items})
}

func (h *HTTPHandler) handleTaskTemplateSave(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req kanban.TaskTemplate
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	if id := strings.TrimSpace(chi.URLParam(r, "id")); id != "" {
		req.ID = id
	}
	item, err := svc.SaveTaskTemplate(r.Context(), req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	respondJSON(w, http.StatusOK, item)
}

func (h *HTTPHandler) handleTaskTemplateDelete(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	if err := svc.DeleteTaskTemplate(r.Context(), chi.URLParam(r, "id")); err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
