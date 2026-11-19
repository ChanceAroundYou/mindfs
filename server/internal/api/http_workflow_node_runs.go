package api

import (
	"net/http"

	"mindfs/server/internal/kanban"

	"github.com/go-chi/chi/v5"
)

// runWithNodeRuns 是运行视图里的一轮：Run 快照（含 graph/node_states）+ 这一轮
// 的节点执行行。任务每发一条留言就可能新建一条 Run，只回当前绑定 Run 会让运行
// 视图每轮把上一轮整块覆盖掉。
type runWithNodeRuns struct {
	*kanban.WorkflowRun
	NodeRuns []kanban.NodeRun `json:"node_runs"`
}

func (h *HTTPHandler) handleTaskWorkflowNodeRunsGet(w http.ResponseWriter, r *http.Request) {
	_, store, _, ok := h.taskWorkflowStore(w, r)
	if !ok {
		return
	}
	runs, err := store.ListWorkflowRunsForTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusInternalServerError, err)
		return
	}
	if len(runs) == 0 {
		respondError(w, http.StatusNotFound, kanban.ErrWorkflowRunNotFound)
		return
	}
	// ListWorkflowRunsForTask 是新的在前；运行视图按对话顺序展示，翻成旧的在前，
	// 前端据此把「第 N 轮」标成 1..N。
	groups := make([]runWithNodeRuns, 0, len(runs))
	flat := make([]kanban.NodeRun, 0)
	for i := len(runs) - 1; i >= 0; i-- {
		nodes, err := store.ListWorkflowNodeRuns(r.Context(), runs[i].ID)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err)
			return
		}
		groups = append(groups, runWithNodeRuns{WorkflowRun: runs[i], NodeRuns: nodes})
		flat = append(flat, nodes...)
	}
	// node_runs 仍保留平铺（旧的取数形状）；runs 是给历史运行视图用的分组。
	respondJSON(w, http.StatusOK, map[string]any{"node_runs": flat, "runs": groups})
}
