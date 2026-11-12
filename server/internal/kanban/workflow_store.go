package kanban

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	configpkg "mindfs/server/internal/config"
)

// ── Store (control-plane file store; versions immutable, drafts CAS) ───────

var (
	ErrDraftConflict   = errors.New("draft conflict: base_revision stale")
	ErrPublishStale    = errors.New("publish rejected: confirmed revision is stale")
	ErrRunConflict     = errors.New("run conflict: revision stale")
	ErrFrozenNodeEdit  = errors.New("cannot edit triggered (frozen) node")
	ErrVersionNotFound = errors.New("workflow version not found")
	ErrDraftNotFound   = errors.New("workflow draft not found")
	// ErrWorkflowMetaNotFound 表示 id 既没有元数据记录、也不是已存在的 workflow。
	ErrWorkflowMetaNotFound = errors.New("workflow meta not found")
)

// WorkflowMeta 是 workflow 的可读元数据，供自动选择（Task 2 的推荐器）使用。
// 它随控制面 workflows.json 一起持久化，与草稿/版本的正文分离：改元数据不推进
// 草稿 revision、也不产生新版本。
type WorkflowMeta struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	UseCases          []string `json:"use_cases"`
	Tags              []string `json:"tags"`
	AutoSelectEnabled bool     `json:"auto_select_enabled"`
}

type WorkflowStore struct {
	dir string // control-plane dir (main node); tests use temp dir
	mu  sync.Mutex
	now func() time.Time
}

// NewWorkflowStore returns a control-plane WorkflowStore. dir is the
// control-plane directory on the main node.
func NewWorkflowStore(dir string) *WorkflowStore {
	return &WorkflowStore{dir: dir, now: time.Now}
}

// NewWorkflowStoreDefault 按配置目录打开控制面 workflow 库
// （工作流属于共享控制面，只有主节点一份）。
func NewWorkflowStoreDefault() (*WorkflowStore, error) {
	dir, err := configpkg.MindFSConfigDir()
	if err != nil {
		return nil, err
	}
	return &WorkflowStore{dir: dir, now: time.Now}, nil
}

// NewWorkflowStoreAt is an alias for tests / explicit dir injection.
func NewWorkflowStoreAt(dir string) *WorkflowStore {
	return &WorkflowStore{dir: dir, now: time.Now}
}

func (s *WorkflowStore) pathLocked() string {
	if s.dir == "" {
		s.dir = filepath.Join(os.TempDir(), "mindfs-workflows")
	}
	return filepath.Join(s.dir, "workflows.json")
}

func (s *WorkflowStore) nowT() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

type workflowFile struct {
	Drafts    map[string]*WorkflowDraft           `json:"drafts"`
	Versions  map[string]*WorkflowVersion         `json:"versions"`
	Proposals map[string]*WorkflowPublishProposal `json:"publish_proposals,omitempty"`
	Meta      map[string]*WorkflowMeta            `json:"meta,omitempty"`
	NextSeq   map[string]int                      `json:"next_seq"`
}

func (s *WorkflowStore) loadLocked() (*workflowFile, error) {
	b, err := os.ReadFile(s.pathLocked())
	if err != nil {
		if os.IsNotExist(err) {
			return &workflowFile{Drafts: map[string]*WorkflowDraft{}, Versions: map[string]*WorkflowVersion{}, Proposals: map[string]*WorkflowPublishProposal{}, Meta: map[string]*WorkflowMeta{}, NextSeq: map[string]int{}}, nil
		}
		return nil, err
	}
	var f workflowFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	if f.Drafts == nil {
		f.Drafts = map[string]*WorkflowDraft{}
	}
	if f.Versions == nil {
		f.Versions = map[string]*WorkflowVersion{}
	}
	if f.Proposals == nil {
		f.Proposals = map[string]*WorkflowPublishProposal{}
	}
	if f.Meta == nil {
		f.Meta = map[string]*WorkflowMeta{}
	}
	if f.NextSeq == nil {
		f.NextSeq = map[string]int{}
	}
	return &f, nil
}

func (s *WorkflowStore) saveLocked(f *workflowFile) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.pathLocked()), 0o755); err != nil {
		return err
	}
	tmp := s.pathLocked() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.pathLocked())
}

func newRunID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "run-" + hex.EncodeToString(b)
}

// CreateDraft creates a draft at revision r1 (or fails if draft exists).
func (s *WorkflowStore) CreateDraft(ctx context.Context, workflowID, rootID, author string, graph WorkflowDefinition) (WorkflowDraft, error) {
	if workflowID = strings.TrimSpace(workflowID); workflowID == "" {
		return WorkflowDraft{}, errors.New("workflow id required")
	}
	if err := ValidateWorkflowDefinition(graph); err != nil {
		return WorkflowDraft{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return WorkflowDraft{}, err
	}
	if _, ok := f.Drafts[workflowID]; ok {
		return WorkflowDraft{}, errors.New("draft already exists")
	}
	graph.RootID = rootID
	d := &WorkflowDraft{WorkflowID: workflowID, Revision: "r1", Graph: graph, Author: author, UpdatedAt: s.nowT()}
	f.Drafts[workflowID] = d
	f.NextSeq[workflowID] = 1
	if err := s.saveLocked(f); err != nil {
		return WorkflowDraft{}, err
	}
	return *d, nil
}

// UpdateDraft applies CAS: baseRevision must equal current revision.
func (s *WorkflowStore) UpdateDraft(ctx context.Context, workflowID, baseRevision, author string, graph WorkflowDefinition) (WorkflowDraft, error) {
	if err := ValidateWorkflowDefinition(graph); err != nil {
		return WorkflowDraft{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return WorkflowDraft{}, err
	}
	d, ok := f.Drafts[workflowID]
	if !ok {
		return WorkflowDraft{}, ErrDraftNotFound
	}
	if d.Revision != baseRevision {
		return *d, ErrDraftConflict
	}
	seq := f.NextSeq[workflowID] + 1
	d.Revision = fmt.Sprintf("r%d", seq)
	d.Graph = graph
	d.Author = author
	d.UpdatedAt = s.nowT()
	f.NextSeq[workflowID] = seq
	if err := s.saveLocked(f); err != nil {
		return WorkflowDraft{}, err
	}
	return *d, nil
}

// PublishDraft publishes only if confirmedRevision == current draft revision.
// Old confirmations cannot publish new content (they fail with ErrPublishStale).
func (s *WorkflowStore) PublishDraft(ctx context.Context, workflowID, confirmedRevision, publishedBy string) (WorkflowVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return WorkflowVersion{}, err
	}
	v, err := s.publishDraftLocked(f, workflowID, confirmedRevision, publishedBy)
	if err != nil {
		return WorkflowVersion{}, err
	}
	if err := s.saveLocked(f); err != nil {
		return WorkflowVersion{}, err
	}
	return v, nil
}

// publishDraftLocked creates the immutable version in f without saving. Callers
// hold s.mu and must persist f; proposal confirmation uses this so the version
// and the proposal status change commit in one write.
func (s *WorkflowStore) publishDraftLocked(f *workflowFile, workflowID, confirmedRevision, publishedBy string) (WorkflowVersion, error) {
	d, ok := f.Drafts[workflowID]
	if !ok {
		return WorkflowVersion{}, ErrDraftNotFound
	}
	if d.Revision != confirmedRevision {
		return WorkflowVersion{}, ErrPublishStale
	}
	seq := f.NextSeq[workflowID]
	verID := fmt.Sprintf("%s:v%d", workflowID, seq)
	// Versions are immutable. Re-publishing the same revision is idempotent;
	// a version id collision with different content fails closed instead of
	// silently replacing the published record.
	if existing, ok := f.Versions[verID]; ok {
		if existing.Revision != confirmedRevision {
			return WorkflowVersion{}, fmt.Errorf("workflow version %s already exists for revision %s, refusing to overwrite with %s", verID, existing.Revision, confirmedRevision)
		}
		return *existing, nil
	}
	// deep copy graph so later draft edits cannot mutate the published version
	graphCopy := cloneGraph(d.Graph)
	v := &WorkflowVersion{Version: verID, WorkflowID: workflowID, Revision: d.Revision, Graph: graphCopy, Author: d.Author, PublishedBy: publishedBy, PublishedAt: s.nowT()}
	f.Versions[verID] = v
	return *v, nil
}

func cloneGraph(g WorkflowDefinition) WorkflowDefinition {
	b, _ := json.Marshal(g)
	var out WorkflowDefinition
	json.Unmarshal(b, &out)
	return out
}

func (s *WorkflowStore) GetVersion(ctx context.Context, version string) (WorkflowVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return WorkflowVersion{}, err
	}
	v, ok := f.Versions[version]
	if !ok {
		return WorkflowVersion{}, ErrVersionNotFound
	}
	return *v, nil
}

// EditRunGraph on the control-plane store is intentionally unsupported. Runtime graph
// snapshots are data-plane records and must be edited through the task's TaskStore.
func (s *WorkflowStore) EditRunGraph(ctx context.Context, runID string, baseRevision int, graph WorkflowDefinition) error {
	return errors.New("workflow runs are task-node data-plane records; use TaskStore.EditWorkflowRunGraph")
}

// ListDrafts returns all drafts sorted by workflow id.
func (s *WorkflowStore) ListDrafts(ctx context.Context) ([]WorkflowDraft, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	out := make([]WorkflowDraft, 0, len(f.Drafts))
	for _, d := range f.Drafts {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkflowID < out[j].WorkflowID })
	return out, nil
}

// ListVersions returns all published workflow versions sorted by version id.
func (s *WorkflowStore) ListVersions(ctx context.Context) ([]WorkflowVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	out := make([]WorkflowVersion, 0, len(f.Versions))
	for _, v := range f.Versions {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// ── Workflow metadata (Phase 1, Task 1) ───────────────────────────────────
//
// 元数据与草稿/版本正文解耦：GetMeta/UpdateMeta 不推进 draft revision、
// 不产生版本，因此自动选择所需的信息可以独立演进。存量 workflows.json
// 没有 meta 段时按默认值读取（可用、名字取 id），与旧的「所有 workflow 都可选」
// 行为一致，零回填。

func defaultWorkflowMeta(id string) *WorkflowMeta {
	return &WorkflowMeta{
		ID:                id,
		Name:              id,
		UseCases:          []string{},
		Tags:              []string{},
		AutoSelectEnabled: true,
	}
}

func copyWorkflowMeta(m *WorkflowMeta) *WorkflowMeta {
	cp := *m
	cp.UseCases = append([]string{}, m.UseCases...)
	cp.Tags = append([]string{}, m.Tags...)
	return &cp
}

// workflowKnownLocked reports whether id names a draft or a published workflow.
// 调用方须持有 s.mu。
func workflowKnownLocked(f *workflowFile, id string) bool {
	if _, ok := f.Drafts[id]; ok {
		return true
	}
	for _, v := range f.Versions {
		if v.WorkflowID == id {
			return true
		}
	}
	return false
}

// GetMeta returns metadata for one workflow. Without a stored record, a known
// workflow falls back to defaults; an unknown id fails closed.
func (s *WorkflowStore) GetMeta(ctx context.Context, id string) (*WorkflowMeta, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("workflow id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	if meta, ok := f.Meta[id]; ok && meta != nil {
		return copyWorkflowMeta(meta), nil
	}
	if !workflowKnownLocked(f, id) {
		return nil, ErrWorkflowMetaNotFound
	}
	return defaultWorkflowMeta(id), nil
}

// ListAvailable returns auto-select eligible workflows sorted by id. It only
// enumerates workflows that actually exist (draft or published version); a
// metadata row for a non-existent id is not "available".
func (s *WorkflowStore) ListAvailable(ctx context.Context) ([]*WorkflowMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for id := range f.Drafts {
		ids[id] = true
	}
	for _, v := range f.Versions {
		ids[v.WorkflowID] = true
	}
	out := make([]*WorkflowMeta, 0, len(ids))
	for id := range ids {
		meta := defaultWorkflowMeta(id)
		if stored, ok := f.Meta[id]; ok && stored != nil {
			meta = copyWorkflowMeta(stored)
			meta.ID = id
		}
		if !meta.AutoSelectEnabled {
			continue
		}
		out = append(out, meta)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// UpdateMeta upserts metadata for a workflow. The id argument is authoritative;
// nil collections are normalized to empty arrays for a stable JSON shape.
func (s *WorkflowStore) UpdateMeta(ctx context.Context, id string, meta *WorkflowMeta) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("workflow id required")
	}
	if meta == nil {
		return errors.New("workflow meta required")
	}
	stored := *meta
	stored.ID = id
	if stored.UseCases == nil {
		stored.UseCases = []string{}
	}
	if stored.Tags == nil {
		stored.Tags = []string{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return err
	}
	f.Meta[id] = &stored
	return s.saveLocked(f)
}
