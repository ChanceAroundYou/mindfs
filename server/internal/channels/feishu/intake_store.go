package feishu

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// intakeDedupTTL is how long a received message id is remembered. Feishu can
// redeliver an event after a restart; keeping a week covers any replay window
// without letting the file grow forever.
const intakeDedupTTL = 7 * 24 * time.Hour

type intakeRecord struct {
	MessageID string    `json:"message_id"`
	TaskID    string    `json:"task_id,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type intakeStoreFile struct {
	Version int            `json:"version"`
	Records []intakeRecord `json:"records"`
}

// IntakeStore remembers which inbound Feishu messages were already handled.
// One JSON file, rewritten via temp+rename: a crash during a write must not
// leave a half-written dedup ledger (nor a memory-only entry that a restart
// would forget).
type IntakeStore struct {
	mu      sync.Mutex
	path    string
	records map[string]intakeRecord
	now     func() time.Time
}

// NewIntakeStore opens (or creates) the dedup ledger at path.
func NewIntakeStore(path string, now func() time.Time) (*IntakeStore, error) {
	if now == nil {
		now = time.Now
	}
	s := &IntakeStore{path: path, records: map[string]intakeRecord{}, now: now}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("feishu intake store %s: %w", path, err)
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("feishu intake store %s: %w", path, err)
	}
	var f intakeStoreFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("feishu intake store %s: %w", path, err)
	}
	for _, rec := range f.Records {
		if rec.MessageID != "" {
			s.records[rec.MessageID] = rec
		}
	}
	if s.pruneLocked() {
		if err := s.saveLocked(); err != nil {
			return nil, fmt.Errorf("feishu intake store %s: %w", path, err)
		}
	}
	return s, nil
}

// Seen reports whether a message id has been recorded within the dedup window.
func (s *IntakeStore) Seen(messageID string) bool {
	if messageID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// A process can stay up for weeks: expire lazily here so a replayed id
	// older than the window is treated as new. Disk cleanup happens on the next
	// Record/SetStatus (or the next startup), both of which save the pruned map.
	s.pruneLocked()
	_, ok := s.records[messageID]
	return ok
}

// Record remembers a handled message once. The first status wins: a later
// picker click must use SetStatus to turn a recorded picker into created.
func (s *IntakeStore) Record(messageID, taskID, status string) error {
	if messageID == "" {
		return errors.New("feishu intake: message id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if _, ok := s.records[messageID]; ok {
		return nil
	}
	s.records[messageID] = intakeRecord{
		MessageID: messageID, TaskID: taskID, Status: status, CreatedAt: s.now(),
	}
	if err := s.saveLocked(); err != nil {
		delete(s.records, messageID)
		return err
	}
	return nil
}

// SetStatus updates the status (and task id) of an already recorded message.
func (s *IntakeStore) SetStatus(messageID, taskID, status string) error {
	if messageID == "" {
		return errors.New("feishu intake: message id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	rec, ok := s.records[messageID]
	if !ok {
		rec = intakeRecord{MessageID: messageID, CreatedAt: s.now()}
	}
	before := rec
	rec.TaskID, rec.Status = taskID, status
	s.records[messageID] = rec
	if err := s.saveLocked(); err != nil {
		if ok {
			s.records[messageID] = before
		} else {
			delete(s.records, messageID)
		}
		return err
	}
	return nil
}

// Prune drops records older than the dedup window and persists the result.
// Production paths expire lazily inside Seen/Record/SetStatus; this explicit
// entry point remains for callers that want to force a disk rewrite.
func (s *IntakeStore) Prune() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pruneLocked() {
		return nil
	}
	return s.saveLocked()
}

// pruneLocked removes expired records from memory and reports whether anything
// changed. Callers must hold s.mu.
func (s *IntakeStore) pruneLocked() bool {
	cutoff := s.now().Add(-intakeDedupTTL)
	changed := false
	for id, rec := range s.records {
		if rec.CreatedAt.Before(cutoff) {
			delete(s.records, id)
			changed = true
		}
	}
	return changed
}

func (s *IntakeStore) saveLocked() error {
	records := make([]intakeRecord, 0, len(s.records))
	for _, rec := range s.records {
		records = append(records, rec)
	}
	raw, err := json.Marshal(intakeStoreFile{Version: 1, Records: records})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".intake-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}
