package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"mindfs/server/internal/api/usecase"

	"github.com/go-chi/chi/v5"
)

// handleSessionWorktreeOwnershipClear 是 POST /api/sessions/{key}/worktree-ownership
// 与 POST /api/sessions/worktree-ownership 共用的入口。
//
// 为什么需要这个端点：repoint 是「fork 新转录 + 换 id + 搬文件」，而在
// **归属指向的目录已经没了、运行时 cwd 早已不在那儿**的会话上，它会在
// 「会话已经在这个目录里」那一步直接 400，且那一步排在所有副作用之前 ——
// 所以那批会话既搬不动、也没有任何清归属的入口，界面上只能一直挂着
// 「目录不存在」徽标。这里补的就是那个缺的入口。
//
// 与 repoint 同构：path 里的 key 可选，脚本手里只有 CLAUDE_CODE_SESSION_ID 时
// 允许在 body 里给 agent_session_id 让服务端反查。
func (h *HTTPHandler) handleSessionWorktreeOwnershipClear(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(chi.URLParam(r, "key"))
	var req struct {
		RootID         string `json:"root_id"`
		AgentSessionID string `json:"agent_session_id"`
		// Force 允许在 worktree 目录仍然存在时也解除归属。默认拒绝：那是一个活着的
		// 工作树，把会话从里面踢出去不该是「修一个坏掉的行」的副作用。
		Force bool `json:"force"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, errInvalidRequest("invalid json body"))
		return
	}
	rootID := strings.TrimSpace(req.RootID)
	agentSessionID := strings.TrimSpace(req.AgentSessionID)
	if rootID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id is required"))
		return
	}
	if key == "" && agentSessionID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("session key or agent_session_id required"))
		return
	}
	out, err := h.service().ClearSessionWorktreeOwnership(r.Context(), usecase.ClearSessionWorktreeOwnershipInput{
		RootID:         rootID,
		Key:            key,
		AgentSessionID: agentSessionID,
		Force:          req.Force,
	})
	if err != nil {
		respondError(w, http.StatusBadRequest, err)
		return
	}
	// 前端手里还是旧视图（worktree 标签、worktree_missing）。没有这条广播，
	// 清完之后徽标要等下一次整表重拉才消失 —— 而用户刚点的就是「让它消失」。
	if h.AppContext != nil && out.Session != nil {
		h.AppContext.BroadcastSessionMetaUpdated(rootID, out.Session)
	}
	respondJSON(w, http.StatusOK, out)
}
