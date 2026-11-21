package kanban

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// W3 T2：把 Router 起草的 workflow **固化进目录**。
//
// 用户裁定：「发新的 / 改已有的 workflow 需要显式批准，项目级别、临时使用的可以
// 自由生成，但是固化、提升到全局的 workflow 需要批准」。所以分两档：
//
//   - 项目级**新**条目 → 直接落目录（草稿 + 发布），不需要人批准；
//   - 全局条目、或**修改**目录里已有的条目 → 先落一张待批准卡，人在任务详情里
//     批准后才真正写控制面。
//
// 批准必须是「批的就是看过的这一版」：请求里记下当时的草稿 revision，批准时按
// 同一个 revision 做 CAS；期间草稿被人改过就拒绝（ErrWorkflowPublishStale），
// 让人重新看一遍，而不是把旧内容盖到新草稿上。

var ErrWorkflowPublishStale = errors.New("workflow draft changed since the request; ask again")

// WorkflowPublishRequest 是「把这张图固化进目录」的完整请求。它同时是待批准卡
// 的载荷 —— 卡片上给人看的就是这些字段。
type WorkflowPublishRequest struct {
	WorkflowID  string             `json:"workflow_id"`
	Name        string             `json:"name,omitempty"`
	Scope       string             `json:"scope"` // project | global
	Graph       WorkflowDefinition `json:"graph"`
	DerivedFrom string             `json:"derived_from,omitempty"`
	Reason      string             `json:"reason,omitempty"`
	// BaseDraftRevision 是提出请求时该 workflow 草稿的 revision；空 = 当时目录里
	// 没有这个 workflow。批准/直接发布时按它做 CAS。
	BaseDraftRevision string `json:"base_draft_revision,omitempty"`
}

// Normalize 补齐默认值并校验基本形状。scope 只认 project / global。
func (r WorkflowPublishRequest) Normalize() (WorkflowPublishRequest, error) {
	r.WorkflowID = strings.TrimSpace(r.WorkflowID)
	r.Name = strings.TrimSpace(r.Name)
	r.Scope = strings.TrimSpace(r.Scope)
	r.DerivedFrom = strings.TrimSpace(r.DerivedFrom)
	r.Reason = strings.TrimSpace(r.Reason)
	if r.WorkflowID == "" {
		return r, errors.New("workflow_id required")
	}
	switch r.Scope {
	case "":
		r.Scope = WorkflowScopeProject
	case WorkflowScopeProject, WorkflowScopeGlobal:
	default:
		return r, fmt.Errorf("scope must be %q or %q", WorkflowScopeProject, WorkflowScopeGlobal)
	}
	if r.Name == "" {
		r.Name = r.WorkflowID
	}
	return r, nil
}

const (
	WorkflowScopeProject = "project"
	WorkflowScopeGlobal  = "global"
)

// RootIDForScope 把作用域翻译成图上的 root：空 = 全局。
func RootIDForScope(taskRootID, scope string) string {
	if scope == WorkflowScopeGlobal {
		return ""
	}
	return taskRootID
}

// WorkflowExists 报告目录里是否已经有这个 id（草稿或任一已发布版本）。
func (s *WorkflowStore) WorkflowExists(ctx context.Context, workflowID string) (bool, error) {
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		return false, errors.New("workflow id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return false, err
	}
	return workflowKnownLocked(f, workflowID), nil
}

// workflowPublishNeedsApproval 是两档判据的唯一实现：目录里已有的条目（改已有）
// 与全局条目都要人批准；只有「项目级 + 新条目」可以自由固化。
func workflowPublishNeedsApproval(ctx context.Context, store *WorkflowStore, req WorkflowPublishRequest) (bool, error) {
	if store == nil {
		return false, errors.New("workflow control-plane store unavailable")
	}
	if req.Scope == WorkflowScopeGlobal {
		return true, nil
	}
	return store.WorkflowExists(ctx, req.WorkflowID)
}

// commitWorkflowPublish 真正写控制面：CAS 住请求时看到的草稿 revision，写图，
// 再发布成一个新版本。目录里没有这个 workflow 时先建 r1。
//
// 它同时服务两条路：项目级新条目在 Router 回合里直接调；需要批准的那一档在
// 人点「批准」时调。两条路走同一份实现，避免「批准后的写入」和「免批写入」
// 出现两套语义。
func commitWorkflowPublish(ctx context.Context, store *WorkflowStore, req WorkflowPublishRequest, taskRootID, publishedBy string) (WorkflowVersion, error) {
	if store == nil {
		return WorkflowVersion{}, errors.New("workflow control-plane store unavailable")
	}
	graph := req.Graph
	graph.RootID = RootIDForScope(taskRootID, req.Scope)

	revision := ""
	draft, err := store.GetDraft(ctx, req.WorkflowID)
	switch {
	case err == nil:
		if req.BaseDraftRevision != "" && draft.Revision != req.BaseDraftRevision {
			return WorkflowVersion{}, ErrWorkflowPublishStale
		}
		updated, err := store.UpdateDraft(ctx, req.WorkflowID, draft.Revision, publishedBy, graph)
		if err != nil {
			if errors.Is(err, ErrDraftConflict) {
				return WorkflowVersion{}, ErrWorkflowPublishStale
			}
			return WorkflowVersion{}, err
		}
		revision = updated.Revision
	case errors.Is(err, ErrDraftNotFound):
		if req.BaseDraftRevision != "" {
			return WorkflowVersion{}, ErrWorkflowPublishStale
		}
		created, err := store.CreateDraft(ctx, req.WorkflowID, graph.RootID, publishedBy, graph)
		if err != nil {
			if strings.Contains(err.Error(), "draft already exists") {
				return WorkflowVersion{}, ErrWorkflowPublishStale
			}
			return WorkflowVersion{}, err
		}
		revision = created.Revision
	default:
		return WorkflowVersion{}, err
	}

	version, err := store.PublishDraft(ctx, req.WorkflowID, revision, publishedBy)
	if err != nil {
		if errors.Is(err, ErrPublishStale) || errors.Is(err, ErrDraftConflict) {
			return WorkflowVersion{}, ErrWorkflowPublishStale
		}
		return WorkflowVersion{}, err
	}
	if req.Name != "" {
		meta, err := store.GetMeta(ctx, req.WorkflowID)
		if err != nil && !errors.Is(err, ErrWorkflowMetaNotFound) {
			return WorkflowVersion{}, err
		}
		if meta == nil {
			meta = defaultWorkflowMeta(req.WorkflowID)
		}
		meta.Name = req.Name
		if req.DerivedFrom != "" && len(meta.Tags) == 0 {
			meta.Tags = []string{"derived:" + req.DerivedFrom}
		}
		if err := store.UpdateMeta(ctx, req.WorkflowID, meta); err != nil {
			return WorkflowVersion{}, err
		}
	}
	return version, nil
}
