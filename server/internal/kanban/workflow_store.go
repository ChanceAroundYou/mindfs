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
	Drafts   map[string]*WorkflowDraft   `json:"drafts"`
	Versions map[string]*WorkflowVersion `json:"versions"`
	NextSeq  map[string]int              `json:"next_seq"`
	Runs     map[string]*WorkflowRun     `json:"runs"`
}

func (s *WorkflowStore) loadLocked() (*workflowFile, error) {
	b, err := os.ReadFile(s.pathLocked())
	if err != nil {
		if os.IsNotExist(err) {
			return &workflowFile{Drafts: map[string]*WorkflowDraft{}, Versions: map[string]*WorkflowVersion{}, NextSeq: map[string]int{}, Runs: map[string]*WorkflowRun{}}, nil
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
	if f.NextSeq == nil {
		f.NextSeq = map[string]int{}
	}
	if f.Runs == nil {
		f.Runs = map[string]*WorkflowRun{}
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
	d, ok := f.Drafts[workflowID]
	if !ok {
		return WorkflowVersion{}, ErrDraftNotFound
	}
	if d.Revision != confirmedRevision {
		return WorkflowVersion{}, ErrPublishStale
	}
	seq := f.NextSeq[workflowID]
	verID := fmt.Sprintf("%s:v%d", workflowID, seq)
	// deep copy graph so later draft edits cannot mutate the published version
	graphCopy := cloneGraph(d.Graph)
	v := &WorkflowVersion{Version: verID, WorkflowID: workflowID, Revision: d.Revision, Graph: graphCopy, Author: d.Author, PublishedBy: publishedBy, PublishedAt: s.nowT()}
	f.Versions[verID] = v
	if err := s.saveLocked(f); err != nil {
		return WorkflowVersion{}, err
	}
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

// CreateRun snapshots the graph inline (data-plane copy; no control lookup later).
func (s *WorkflowStore) CreateRun(ctx context.Context, taskID, version string, graph WorkflowDefinition) (*WorkflowRun, error) {
	if err := ValidateWorkflowDefinition(graph); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	now := s.nowT()
	run := &WorkflowRun{ID: newRunID(), TaskID: taskID, Version: version, Revision: 1, Graph: cloneGraph(graph),
		NodeStates: map[string]string{}, NodeOutputs: map[string]map[string]any{}, SelectedRoutes: map[string]string{},
		LoopState: map[string]*LoopState{}, LoopExitExpr: map[string]string{}, CreatedAt: now, UpdatedAt: now}
	f.Runs[run.ID] = run
	if err := s.saveLocked(f); err != nil {
		return nil, err
	}
	return run, nil
}

// SaveRun persists state changes with CAS on the stored revision: the
// caller must pass the revision it last read; a concurrent EditRunGraph
// that bumped the revision makes this a conflict (ErrRunConflict) instead
// of a last-writer-wins overwrite. On success it bumps Revision so the
// next writer CASes against the new value and keeps UpdatedAt fresh.
func (s *WorkflowStore) SaveRun(ctx context.Context, run *WorkflowRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return err
	}
	stored, ok := f.Runs[run.ID]
	if !ok {
		return errors.New("run not found")
	}
	if stored.Revision != run.Revision {
		return ErrRunConflict
	}
	run.Revision++
	run.UpdatedAt = s.nowT()
	f.Runs[run.ID] = run
	return s.saveLocked(f)
}

// GetRun returns a copy of the stored run for CAS readers.
func (s *WorkflowStore) GetRun(ctx context.Context, runID string) (*WorkflowRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	run, ok := f.Runs[runID]
	if !ok {
		return nil, errors.New("run not found")
	}
	cp := *run
	cp.NodeStates = make(map[string]string, len(run.NodeStates))
	for k, v := range run.NodeStates {
		cp.NodeStates[k] = v
	}
	return &cp, nil
}

// EditRunGraph edits only untriggered nodes; triggered nodes/edges frozen.
func (s *WorkflowStore) EditRunGraph(ctx context.Context, runID string, baseRevision int, graph WorkflowDefinition) error {
	if err := ValidateWorkflowDefinition(graph); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return err
	}
	run, ok := f.Runs[runID]
	if !ok {
		return errors.New("run not found")
	}
	if run.Revision != baseRevision {
		return ErrRunConflict
	}
	frozen := run.FrozenNodeIDs()
	// any triggered node must still exist unchanged in the new graph
	for id := range frozen {
		old, ok := run.Graph.node(id)
		if !ok {
			continue
		}
		nw, ok := graph.node(id)
		if !ok {
			return fmt.Errorf("%w: %s", ErrFrozenNodeEdit, id)
		}
		ob, _ := json.Marshal(old)
		nb, _ := json.Marshal(nw)
		if string(ob) != string(nb) {
			return fmt.Errorf("%w: %s", ErrFrozenNodeEdit, id)
		}
	}
	run.Graph = cloneGraph(graph)
	run.Revision++
	run.UpdatedAt = s.nowT()
	return s.saveLocked(f)
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
