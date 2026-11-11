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
)

type WorkflowStore struct {
	dir string // control-plane dir (main node); tests use temp dir
	mu  sync.Mutex
	now func() time.Time
}

// NewWorkflowStore returns a control-plane WorkflowStore. dir is the
// control-plane directory on the main node (TemplateStore dir equivalent).
func NewWorkflowStore(dir string) *WorkflowStore {
	return &WorkflowStore{dir: dir, now: time.Now}
}

// NewWorkflowStoreDefault 按配置目录打开控制面 workflow 库，与 TemplateStore 同目录
// （工作流模板与任务模板同属共享控制面，只有主节点一份）。
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
	NextSeq   map[string]int                      `json:"next_seq"`
}

func (s *WorkflowStore) loadLocked() (*workflowFile, error) {
	b, err := os.ReadFile(s.pathLocked())
	if err != nil {
		if os.IsNotExist(err) {
			return &workflowFile{Drafts: map[string]*WorkflowDraft{}, Versions: map[string]*WorkflowVersion{}, Proposals: map[string]*WorkflowPublishProposal{}, NextSeq: map[string]int{}}, nil
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
