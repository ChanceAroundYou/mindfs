package kanban

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Publish proposals keep Agent-authored workflow changes reviewable: proposing
// records the workflow and its exact revision but creates no version. A human
// confirmation calls the same publish helper, pinned to that revision, so a
// draft that moved on cannot be published by a stale proposal.
const (
	PublishProposalPending   = "pending"
	PublishProposalPublished = "published"
	PublishProposalStale     = "stale"
)

var ErrPublishProposalNotFound = errors.New("workflow publish proposal not found")

type WorkflowPublishProposal struct {
	ID          string     `json:"id"`
	WorkflowID  string     `json:"workflow_id"`
	Revision    string     `json:"revision"`
	Proposer    string     `json:"proposer"`
	Status      string     `json:"status"`
	Version     string     `json:"version,omitempty"`
	ConfirmedBy string     `json:"confirmed_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	ConfirmedAt *time.Time `json:"confirmed_at,omitempty"`
}

func newPublishProposalID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate publish proposal id: %w", err)
	}
	return "prop-" + hex.EncodeToString(b), nil
}

func (s *WorkflowStore) GetDraft(ctx context.Context, workflowID string) (WorkflowDraft, error) {
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
	return *d, nil
}

// ApplyWorkflowDraftPatch applies typed operations against the draft's current
// revision. Validation failure writes nothing; success advances the draft
// revision exactly once and returns the deterministic diff.
func (s *WorkflowStore) ApplyWorkflowDraftPatch(ctx context.Context, workflowID string, patch WorkflowPatch) (WorkflowDraft, WorkflowDiff, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return WorkflowDraft{}, WorkflowDiff{}, err
	}
	d, ok := f.Drafts[workflowID]
	if !ok {
		return WorkflowDraft{}, WorkflowDiff{}, ErrDraftNotFound
	}
	if strings.TrimSpace(patch.BaseRevision) != d.Revision {
		return *d, WorkflowDiff{}, ErrDraftConflict
	}
	graph, diff, issues := ApplyWorkflowPatchToGraph(d.Graph, patch, nil)
	if len(issues) > 0 {
		return *d, diff, &WorkflowPatchError{Issues: issues}
	}
	seq := f.NextSeq[workflowID] + 1
	author := strings.TrimSpace(patch.Author)
	if author == "" {
		author = d.Author
	}
	d.Revision = fmt.Sprintf("r%d", seq)
	d.Graph = graph
	d.Author = author
	d.UpdatedAt = s.nowT()
	f.NextSeq[workflowID] = seq
	if err := s.saveLocked(f); err != nil {
		return WorkflowDraft{}, WorkflowDiff{}, err
	}
	return *d, diff, nil
}

func (s *WorkflowStore) ProposeWorkflowPublish(ctx context.Context, workflowID, revision, proposer string) (WorkflowPublishProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return WorkflowPublishProposal{}, err
	}
	d, ok := f.Drafts[workflowID]
	if !ok {
		return WorkflowPublishProposal{}, ErrDraftNotFound
	}
	if d.Revision != revision {
		return WorkflowPublishProposal{}, ErrPublishStale
	}
	// One proposal per (workflow, revision). Two pending proposals pinned at the
	// same revision would both confirm into the same version id; the second
	// confirmation would overwrite the first version record instead of being
	// idempotent. Return the existing reviewable record instead.
	for _, existing := range f.Proposals {
		if existing.WorkflowID == workflowID && existing.Revision == revision && existing.Status == PublishProposalPending {
			return *existing, nil
		}
	}
	for _, existing := range f.Proposals {
		if existing.WorkflowID == workflowID && existing.Revision == revision && existing.Status == PublishProposalPublished {
			return *existing, nil
		}
	}
	id, err := newPublishProposalID()
	if err != nil {
		return WorkflowPublishProposal{}, err
	}
	p := &WorkflowPublishProposal{
		ID:         id,
		WorkflowID: workflowID,
		Revision:   revision,
		Proposer:   proposer,
		Status:     PublishProposalPending,
		CreatedAt:  s.nowT(),
	}
	f.Proposals[p.ID] = p
	if err := s.saveLocked(f); err != nil {
		return WorkflowPublishProposal{}, err
	}
	return *p, nil
}

func (s *WorkflowStore) GetPublishProposal(ctx context.Context, proposalID string) (WorkflowPublishProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return WorkflowPublishProposal{}, err
	}
	p, ok := f.Proposals[proposalID]
	if !ok {
		return WorkflowPublishProposal{}, ErrPublishProposalNotFound
	}
	return *p, nil
}

// ListPublishProposals returns the reviewable proposals for one workflow,
// oldest first. A human needs this to discover proposals an Agent created;
// without it the proposal id would exist only inside the Agent's response.
func (s *WorkflowStore) ListPublishProposals(ctx context.Context, workflowID string) ([]WorkflowPublishProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	if _, ok := f.Drafts[workflowID]; !ok {
		return nil, ErrDraftNotFound
	}
	out := make([]WorkflowPublishProposal, 0, len(f.Proposals))
	for _, p := range f.Proposals {
		if p.WorkflowID == workflowID {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// ConfirmWorkflowPublishProposal publishes the exact revision the proposal was
// made against. If the draft advanced, it marks the proposal stale and fails
// closed without creating a version. Confirming an already-published proposal
// is idempotent and returns the recorded version.
func (s *WorkflowStore) ConfirmWorkflowPublishProposal(ctx context.Context, proposalID, publishedBy string) (WorkflowVersion, WorkflowPublishProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.loadLocked()
	if err != nil {
		return WorkflowVersion{}, WorkflowPublishProposal{}, err
	}
	p, ok := f.Proposals[proposalID]
	if !ok {
		return WorkflowVersion{}, WorkflowPublishProposal{}, ErrPublishProposalNotFound
	}
	switch p.Status {
	case PublishProposalPublished:
		v, ok := f.Versions[p.Version]
		if !ok {
			return WorkflowVersion{}, *p, ErrVersionNotFound
		}
		return *v, *p, nil
	case PublishProposalStale:
		return WorkflowVersion{}, *p, ErrPublishStale
	case PublishProposalPending:
	default:
		return WorkflowVersion{}, *p, fmt.Errorf("workflow publish proposal has unknown status %q", p.Status)
	}
	d, ok := f.Drafts[p.WorkflowID]
	if !ok {
		return WorkflowVersion{}, *p, ErrDraftNotFound
	}
	if d.Revision != p.Revision {
		p.Status = PublishProposalStale
		if err := s.saveLocked(f); err != nil {
			return WorkflowVersion{}, *p, err
		}
		return WorkflowVersion{}, *p, ErrPublishStale
	}
	v, err := s.publishDraftLocked(f, p.WorkflowID, p.Revision, publishedBy)
	if err != nil {
		return WorkflowVersion{}, *p, err
	}
	now := s.nowT()
	p.Status = PublishProposalPublished
	p.Version = v.Version
	p.ConfirmedBy = publishedBy
	p.ConfirmedAt = &now
	if err := s.saveLocked(f); err != nil {
		return WorkflowVersion{}, *p, err
	}
	return v, *p, nil
}
