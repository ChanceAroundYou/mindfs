package kanban

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// 同步内核专用入口：整份控制面投影 + 按实体精确落库。
//
// 与 CreateDraft / UpdateDraft / PublishDraft 的差别：不推进 revision、不做 CAS、
// 不盖时间戳 —— 同步的是「这条记录的内容」，任何本地重算都会让两边 hash 永远对不上，
// 于是每次对账都以为对方又改了。
//
// 版本只追加：同一条版本记录重复应用是空操作，内容不同则显式失败（fail closed）。

// ErrWorkflowVersionImmutable 表示同一版本 id 已存在且内容不同。
var ErrWorkflowVersionImmutable = errors.New("workflow version immutable")

// WorkflowSyncState 是控制面工作流库的可同步投影：每个存在的 workflow 恰好一条 meta，
// 有草稿的带一条草稿，每个版本一条。发布提案不进同步（见计划「不做」）。
type WorkflowSyncState struct {
	Metas    []*WorkflowMeta
	Drafts   []WorkflowDraft
	Versions []WorkflowVersion
}

// ExportForSync 导出全部工作流实体，顺序稳定（按 id / 版本号排序）。
//
// 「存在」的判定与 ListAvailable 一致：有草稿或已发布版本才算工作流；
// 只挂在 Meta 里、没有正文的孤儿条目不算 —— 否则同步会把一条空壳带到对方机器上。
func (s *WorkflowStore) ExportForSync() (WorkflowSyncState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return WorkflowSyncState{}, err
	}
	ids := map[string]bool{}
	for id := range f.Drafts {
		ids[id] = true
	}
	for _, v := range f.Versions {
		ids[v.WorkflowID] = true
	}
	out := WorkflowSyncState{
		Metas:    make([]*WorkflowMeta, 0, len(ids)),
		Drafts:   make([]WorkflowDraft, 0, len(f.Drafts)),
		Versions: make([]WorkflowVersion, 0, len(f.Versions)),
	}
	for id := range ids {
		meta := defaultWorkflowMeta(id)
		if stored, ok := f.Meta[id]; ok && stored != nil {
			meta = copyWorkflowMeta(stored)
			meta.ID = id
		}
		out.Metas = append(out.Metas, meta)
	}
	for _, d := range f.Drafts {
		if d != nil {
			out.Drafts = append(out.Drafts, *d)
		}
	}
	for _, v := range f.Versions {
		if v != nil {
			out.Versions = append(out.Versions, *v)
		}
	}
	sort.Slice(out.Metas, func(i, j int) bool { return out.Metas[i].ID < out.Metas[j].ID })
	sort.Slice(out.Drafts, func(i, j int) bool { return out.Drafts[i].WorkflowID < out.Drafts[j].WorkflowID })
	sort.Slice(out.Versions, func(i, j int) bool { return out.Versions[i].Version < out.Versions[j].Version })
	return out, nil
}

// ApplyMetaForSync 原样落一条元数据（nil 集合不归一化，避免本地与远端载荷不一致）。
func (s *WorkflowStore) ApplyMetaForSync(meta WorkflowMeta) error {
	id := strings.TrimSpace(meta.ID)
	if id == "" {
		return errors.New("workflow id required")
	}
	cp := meta
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return err
	}
	f.Meta[id] = &cp
	return s.saveLocked(f)
}

// ApplyDraftForSync 原样落一条草稿，并把 seq 游标推到不小于该 revision。
//
// 比本地现有草稿更旧的记录直接忽略：应用顺序由 rev 决定，但断点续拉与
// 重放都可能把旧记录送到，回退草稿等于丢掉对方已经看到的新内容。
func (s *WorkflowStore) ApplyDraftForSync(d WorkflowDraft) error {
	id := strings.TrimSpace(d.WorkflowID)
	if id == "" {
		return errors.New("workflow id required")
	}
	seq, err := workflowRevisionSeq(d.Revision)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return err
	}
	if cur, ok := f.Drafts[id]; ok && cur != nil {
		if curSeq, curErr := workflowRevisionSeq(cur.Revision); curErr == nil && curSeq > seq {
			return nil
		}
	}
	cp := d
	f.Drafts[id] = &cp
	if f.NextSeq[id] < seq {
		f.NextSeq[id] = seq
	}
	return s.saveLocked(f)
}

// ApplyVersionForSync 追加一条版本；同一条重复应用是空操作，内容不同则失败。
func (s *WorkflowStore) ApplyVersionForSync(v WorkflowVersion) error {
	id := strings.TrimSpace(v.WorkflowID)
	version := strings.TrimSpace(v.Version)
	if id == "" || version == "" {
		return errors.New("workflow id and version required")
	}
	seq, err := parseWorkflowVersionSeq(id, version)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return err
	}
	if cur, ok := f.Versions[version]; ok && cur != nil {
		same, err := sameWorkflowJSON(*cur, v)
		if err != nil {
			return err
		}
		if same {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrWorkflowVersionImmutable, version)
	}
	cp := v
	f.Versions[version] = &cp
	if f.NextSeq[id] < seq {
		f.NextSeq[id] = seq
	}
	return s.saveLocked(f)
}

// RemoveForSync 删掉一个工作流及其全部正文（同步墓碑）。不存在即成功。
func (s *WorkflowStore) RemoveForSync(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("workflow id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return err
	}
	for version, v := range f.Versions {
		if v != nil && v.WorkflowID == id {
			delete(f.Versions, version)
		}
	}
	for proposal, p := range f.Proposals {
		if p != nil && p.WorkflowID == id {
			delete(f.Proposals, proposal)
		}
	}
	delete(f.Drafts, id)
	delete(f.Meta, id)
	delete(f.NextSeq, id)
	return s.saveLocked(f)
}

// workflowRevisionSeq 解析 "rN"。
func workflowRevisionSeq(revision string) (int, error) {
	revision = strings.TrimSpace(revision)
	if !strings.HasPrefix(revision, "r") {
		return 0, fmt.Errorf("invalid workflow revision %q", revision)
	}
	seq, err := strconv.Atoi(revision[1:])
	if err != nil || seq <= 0 {
		return 0, fmt.Errorf("invalid workflow revision %q", revision)
	}
	return seq, nil
}

// WorkflowVersionSeq 解析 "<workflowID>:vN" 并校验 id 归属。
func WorkflowVersionSeq(workflowID, version string) (int, error) {
	return parseWorkflowVersionSeq(workflowID, version)
}

func parseWorkflowVersionSeq(workflowID, version string) (int, error) {
	prefix := workflowID + ":v"
	if !strings.HasPrefix(version, prefix) {
		return 0, fmt.Errorf("invalid workflow version %q for %q", version, workflowID)
	}
	seq, err := strconv.Atoi(version[len(prefix):])
	if err != nil || seq <= 0 {
		return 0, fmt.Errorf("invalid workflow version %q", version)
	}
	return seq, nil
}

// sameWorkflowJSON 用规范化 JSON 比较两个实体（time.Time 的 Location 不同不该算不同）。
func sameWorkflowJSON(a, b any) (bool, error) {
	ab, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ab, bb), nil
}
