package controlsync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"mindfs/server/internal/kanban"
)

// 工作流域的记录划分（与设计 §5 一致）：
//
//	wf:<id>:meta        元数据，同时承担「这个工作流存在」的根记录
//	wf:<id>:draft       草稿（含 revision）
//	wf:<id>:version:<n> 已发布版本，只追加
//
// 删除一个工作流只在 meta 上发墓碑（Apply 走 RemoveForSync 连正文一起删）；
// 版本自己的墓碑一律拒绝 —— 版本不可变是 D6 的硬约束，唯一能表达删除的
// 是「整个工作流不存在了」。
const (
	workflowKeyPrefix     = "wf:"
	workflowMetaSuffix    = ":meta"
	workflowDraftSuffix   = ":draft"
	workflowVersionMarker = ":version:"
)

var errWorkflowKeyMismatch = errors.New("controlsync: workflow record key does not match payload")

// workflowStore 是工作流库需要的能力面。
type workflowStore interface {
	ExportForSync() (kanban.WorkflowSyncState, error)
	ApplyMetaForSync(meta kanban.WorkflowMeta) error
	ApplyDraftForSync(d kanban.WorkflowDraft) error
	ApplyVersionForSync(v kanban.WorkflowVersion) error
	RemoveForSync(id string) error
}

// WorkflowsDomain 同步共享工作流库。
type WorkflowsDomain struct {
	store workflowStore
}

func NewWorkflowsDomain(store *kanban.WorkflowStore) *WorkflowsDomain {
	if store == nil {
		return &WorkflowsDomain{}
	}
	return &WorkflowsDomain{store: store}
}

func (d *WorkflowsDomain) Name() string { return DomainWorkflows }

func (d *WorkflowsDomain) Snapshot(ctx context.Context) ([]Record, error) {
	if d == nil || d.store == nil {
		return nil, errors.New("controlsync: workflow store required")
	}
	state, err := d.store.ExportForSync()
	if err != nil {
		return nil, fmt.Errorf("controlsync: export workflows: %w", err)
	}
	out := make([]Record, 0, len(state.Metas)+len(state.Drafts)+len(state.Versions))
	for _, meta := range state.Metas {
		rec, err := workflowRecord(workflowKeyPrefix+meta.ID+workflowMetaSuffix, meta)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	for _, draft := range state.Drafts {
		rec, err := workflowRecord(workflowKeyPrefix+draft.WorkflowID+workflowDraftSuffix, draft)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	for _, version := range state.Versions {
		seq, err := kanban.WorkflowVersionSeq(version.WorkflowID, version.Version)
		if err != nil {
			return nil, fmt.Errorf("controlsync: %w", err)
		}
		key := workflowKeyPrefix + version.WorkflowID + workflowVersionMarker + strconv.Itoa(seq)
		rec, err := workflowRecord(key, version)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func workflowRecord(key string, payload any) (Record, error) {
	id, _, err := parseWorkflowKey(key)
	if err != nil {
		return Record{}, err
	}
	if id == "" || strings.Contains(id, ":") {
		return Record{}, fmt.Errorf("controlsync: unusable workflow id in key %q", key)
	}
	return newRecord(DomainWorkflows, key, payload)
}

// parseWorkflowKey 返回 (workflowID, kind)，kind 为 "meta" / "draft" / "version:<n>"。
func parseWorkflowKey(key string) (string, string, error) {
	if !strings.HasPrefix(key, workflowKeyPrefix) {
		return "", "", fmt.Errorf("controlsync: bad workflow key %q", key)
	}
	rest := key[len(workflowKeyPrefix):]
	metaSuffix, draftSuffix, versionMarker := workflowMetaSuffix[1:], workflowDraftSuffix[1:], workflowVersionMarker[1:]
	switch {
	case strings.HasSuffix(rest, ":"+metaSuffix):
		return strings.TrimSuffix(rest, ":"+metaSuffix), "meta", nil
	case strings.HasSuffix(rest, ":"+draftSuffix):
		return strings.TrimSuffix(rest, ":"+draftSuffix), "draft", nil
	case strings.Contains(rest, ":"+versionMarker):
		idx := strings.LastIndex(rest, ":"+versionMarker)
		return rest[:idx], versionMarker + rest[idx+len(":"+versionMarker):], nil
	}
	return "", "", fmt.Errorf("controlsync: bad workflow key %q", key)
}

func (d *WorkflowsDomain) Apply(ctx context.Context, rec Record) error {
	if d == nil || d.store == nil {
		return errors.New("controlsync: workflow store required")
	}
	if rec.Domain != DomainWorkflows {
		return fmt.Errorf("controlsync: workflows domain got %q record", rec.Domain)
	}
	id, kind, err := parseWorkflowKey(rec.Key)
	if err != nil {
		return err
	}
	if id == "" || strings.Contains(id, ":") {
		return fmt.Errorf("controlsync: unusable workflow id in key %q", rec.Key)
	}
	if rec.Deleted {
		if kind != "meta" {
			return fmt.Errorf("controlsync: workflow version/draft cannot be deleted (%q)", rec.Key)
		}
		return d.store.RemoveForSync(id)
	}
	switch {
	case kind == "meta":
		meta, err := decodePayload[kanban.WorkflowMeta](rec)
		if err != nil {
			return err
		}
		if meta.ID != id {
			return fmt.Errorf("%w: key %q, payload %q", errWorkflowKeyMismatch, id, meta.ID)
		}
		return d.store.ApplyMetaForSync(meta)
	case kind == "draft":
		draft, err := decodePayload[kanban.WorkflowDraft](rec)
		if err != nil {
			return err
		}
		if draft.WorkflowID != id {
			return fmt.Errorf("%w: key %q, payload %q", errWorkflowKeyMismatch, id, draft.WorkflowID)
		}
		return d.store.ApplyDraftForSync(draft)
	default:
		seq, err := strconv.Atoi(strings.TrimPrefix(kind, "version:"))
		if err != nil || seq <= 0 {
			return fmt.Errorf("controlsync: bad workflow version key %q", rec.Key)
		}
		version, err := decodePayload[kanban.WorkflowVersion](rec)
		if err != nil {
			return err
		}
		if version.WorkflowID != id {
			return fmt.Errorf("%w: key %q, payload %q", errWorkflowKeyMismatch, id, version.WorkflowID)
		}
		if got, err := kanban.WorkflowVersionSeq(id, version.Version); err != nil || got != seq {
			return fmt.Errorf("%w: key %q, payload version %q", errWorkflowKeyMismatch, rec.Key, version.Version)
		}
		return d.store.ApplyVersionForSync(version)
	}
}
