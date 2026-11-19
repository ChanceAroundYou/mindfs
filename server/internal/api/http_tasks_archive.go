package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"mindfs/server/internal/api/usecase"
	"mindfs/server/internal/kanban"

	"github.com/go-chi/chi/v5"
)

// handleKanbanTaskArchive 归档/取消归档一张任务卡（#86）。
//
// 归档只打标记：卡片从工作台隐藏、留在看板「已结束 → 归档」子组；相关会话只被
// 打归档标记，**不删**任何后代会话或 JSONL（用户裁定 A，见 usecase.ArchiveSession
// 的破坏性路径 —— 这里刻意不走它）。
//
// root_id 放 body 而不是 query：与其它 POST 任务动作一致，前端统一走 moveTask 形状。
func (h *HTTPHandler) handleKanbanTaskArchive(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID   string `json:"root_id"`
		Archived bool   `json:"archived"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req)
	detail, err := svc.ArchiveTask(r.Context(), kanban.ArchiveTaskInput{
		RootID:   strings.TrimSpace(req.RootID),
		TaskID:   strings.TrimSpace(chi.URLParam(r, "id")),
		Archived: req.Archived,
	})
	if err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, kanban.ErrTaskNotArchivable):
			status = http.StatusConflict
		case errors.Is(err, sql.ErrNoRows):
			status = http.StatusNotFound
		}
		respondError(w, status, err)
		return
	}
	h.broadcastTaskUpdated(req.RootID, detail)
	respondJSON(w, http.StatusOK, detail)
}

// taskSessionArchiver 把任务归档的级联落到本账户的 session manager 上。
//
// 只调 SetArchived：JSONL 一行不动、深链接仍能打开，后代会话**不删**。
// 与 usecase.ArchiveSession（删子树）是两条刻意的不同路径。
type taskSessionArchiver struct {
	registry usecase.Registry
}

// NewTaskSessionArchiver 供 workspace 装配使用（传 AppContext 即可，它就是
// usecase.Registry 的实现）。
func NewTaskSessionArchiver(registry usecase.Registry) kanban.SessionArchiver {
	return taskSessionArchiver{registry: registry}
}

func (a taskSessionArchiver) ArchiveSessions(ctx context.Context, rootID string, sessionKeys []string) error {
	if a.registry == nil {
		return errors.New("session registry unavailable")
	}
	manager, err := a.registry.GetSessionManager(rootID)
	if err != nil {
		return err
	}
	for _, key := range sessionKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, err := manager.SetArchived(ctx, key, true); err != nil {
			// 会话早被删掉是常态（任务跑完了用户清过会话）：级联是尽力而为，
			// 不能因为一个 key 找不到就让任务归档失败。
			continue
		}
	}
	return nil
}
