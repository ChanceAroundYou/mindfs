package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"mindfs/server/internal/kanban"
	"mindfs/server/internal/session"

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

// taskSessionArchiveBackend 是归档级联真正需要的能力：拿到本账户的会话管理器
// （枚举/标记会话）和流中心（把归档结果推给侧栏）。刻意只依赖这两样，而不是整个
// usecase.Registry —— 测试替身因此不必实现一长串无关方法。
type taskSessionArchiveBackend interface {
	GetSessionManager(rootID string) (*session.Manager, error)
	GetSessionStreamHub() *StreamHub
}

// taskSessionArchiver 把任务归档的级联落到本账户的 session manager 上。
//
// 归档范围 = 「这个任务名下的全部会话」+ 它们的整棵子会话子树：
//   - 种子 key 来自 kanban（任务主会话、节点会话、活动会话、router 会话），
//     兜住老数据 —— 老会话的 task_id 列可能还是空的；
//   - task_id 命中的会话（新数据都带 task_id）；
//   - 再从这些种子按 parent_session_key 递归收全部子会话（subagent 派生的会话
//     task_id 为空，只能靠父子链找到）。
//
// 只调 SetArchived：JSONL 一行不动、深链接仍能打开，**绝不删除**任何会话
// （用户裁定 A）。与 usecase.ArchiveSession（删子树）是两条刻意的不同路径。
type taskSessionArchiver struct {
	backend taskSessionArchiveBackend
}

// NewTaskSessionArchiver 供 workspace 装配使用（传 AppContext 即可，它同时实现
// GetSessionManager 与 GetSessionStreamHub）。
func NewTaskSessionArchiver(backend taskSessionArchiveBackend) kanban.SessionArchiver {
	return taskSessionArchiver{backend: backend}
}

func (a taskSessionArchiver) ArchiveSessions(ctx context.Context, rootID, taskID string, sessionKeys []string) error {
	if a.backend == nil {
		return errors.New("session registry unavailable")
	}
	manager, err := a.backend.GetSessionManager(rootID)
	if err != nil {
		return err
	}
	metas, err := manager.ListMetas(ctx)
	if err != nil {
		return err
	}
	var archived []*session.Session
	for _, key := range taskArchiveTargetKeys(metas, taskID, sessionKeys) {
		// 不走「元数据里存在才标记」的过滤：ListMetas 排除 router 会话，而 router
		// 会话是靠种子 key 进来的，直接 SetArchived 才能像老路径一样把它也标上
		// （router 不出现在任何列表里，标记它无害）。会话早被删掉是常态（任务跑完
		// 用户清过会话）：级联尽力而为，单个 key 失败跳过，不能让任务归档失败。
		updated, err := manager.SetArchived(ctx, key, true)
		if err != nil {
			continue
		}
		archived = append(archived, updated)
	}
	a.broadcastArchived(rootID, archived)
	return nil
}

// broadcastArchived 把归档结果推给所有客户端，让侧栏把这些会话从主视图摘掉。
//
// 为什么必须广播：ArchiveTask 只推 task.updated，前端那一下只重拉看板、不动会话
// 列表；不补这条，库里已经 archived_at，侧栏却还挂着「看板归档 / #86」——
// 用户看到的就是「归档了任务，会话没归档」。
func (a taskSessionArchiver) broadcastArchived(rootID string, archived []*session.Session) {
	hub := a.backend.GetSessionStreamHub()
	if hub == nil {
		return
	}
	for _, sess := range archived {
		if sess == nil {
			continue
		}
		hub.BroadcastAll(WSResponse{
			Type: "session.meta.updated",
			Payload: map[string]any{
				"root_id": rootID,
				"session": sessionMetaUpdatedPayload(sess),
			},
		})
	}
}

// taskArchiveTargetKeys 计算「这个任务名下、应当一起归档的全部会话 key」。
//
// 纯函数，便于直接测：输入本账户全部会话 meta + 任务 id + 种子 key，输出去重后的
// 目标 key（种子 ∪ task_id 命中 ∪ 按 parent_session_key 递归到的全部后代）。
// 顺序无关（调用方按存在与否逐条打标记）。
func taskArchiveTargetKeys(metas []*session.Session, taskID string, seeds []string) []string {
	taskID = strings.TrimSpace(taskID)
	childrenByParent := make(map[string][]string)
	for _, m := range metas {
		if m == nil {
			continue
		}
		key := strings.TrimSpace(m.Key)
		if key == "" {
			continue
		}
		if parent := strings.TrimSpace(m.ParentSessionKey); parent != "" {
			childrenByParent[parent] = append(childrenByParent[parent], key)
		}
	}
	seen := make(map[string]bool)
	queue := make([]string, 0, len(seeds)+len(metas))
	enqueue := func(key string) {
		key = strings.TrimSpace(key)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		queue = append(queue, key)
	}
	for _, seed := range seeds {
		enqueue(seed)
	}
	if taskID != "" {
		for _, m := range metas {
			if m != nil && strings.TrimSpace(m.TaskID) == taskID {
				enqueue(m.Key)
			}
		}
	}
	// BFS：种子的子会话、子会话的子会话…… 直到叶子（subagent 派生的会话 task_id
	// 为空，父节点被归档时必须靠这条链一起收进来）。
	for head := 0; head < len(queue); head++ {
		for _, childKey := range childrenByParent[queue[head]] {
			enqueue(childKey)
		}
	}
	return queue
}
