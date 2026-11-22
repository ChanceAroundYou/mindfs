package feishu

import (
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

// ActiveTarget is one chat's confirmed destination: the node and project every
// later message in that chat lands in by default, plus the task when the owner
// confirmed an existing one (TaskID empty = messages create new tasks there).
// Keyed by chat, not by message: this is context, not deduplication.
type ActiveTarget struct {
	ChatID     string    `json:"chat_id"`
	NodeID     string    `json:"node_id"`
	RootID     string    `json:"root_id"`
	TaskID     string    `json:"task_id,omitempty"`
	TaskNumber int       `json:"task_number,omitempty"`
	TaskTitle  string    `json:"task_title,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type intakeStoreFile struct {
	Version int            `json:"version"`
	Records []intakeRecord `json:"records"`
	Active  []ActiveTarget `json:"active,omitempty"`
}

// IntakeStore remembers which inbound Feishu messages were already handled,
// and where each chat's messages currently go. One JSON file, rewritten via
// temp+rename: a crash during a write must not leave a half-written dedup
// ledger (nor a memory-only entry that a restart would forget).
type IntakeStore struct {
	mu      sync.Mutex
	path    string
	records map[string]intakeRecord
	active  map[string]ActiveTarget
	now     func() time.Time
}

// NewIntakeStore opens (or creates) the dedup ledger at path.
func NewIntakeStore(path string, now func() time.Time) (*IntakeStore, error) {
	if now == nil {
		now = time.Now
	}
	s := &IntakeStore{
		path: path, records: map[string]intakeRecord{}, active: map[string]ActiveTarget{}, now: now,
	}
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
	for _, t := range f.Active {
		if t.ChatID != "" {
			s.active[t.ChatID] = t
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

// Claim reserves a message id before any side effect. Exactly one concurrent
// caller can win; every terminal outcome then calls SetStatus. A failed write
// removes the in-memory reservation so a later redelivery can try again.
func (s *IntakeStore) Claim(messageID string) (bool, error) {
	if messageID == "" {
		return false, errors.New("feishu intake: message id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	if _, ok := s.records[messageID]; ok {
		return false, nil
	}
	s.records[messageID] = intakeRecord{
		MessageID: messageID, Status: "creating", CreatedAt: s.now(),
	}
	if err := s.saveLocked(); err != nil {
		delete(s.records, messageID)
		return false, err
	}
	return true, nil
}

// Release drops a claim whose side effect never became visible to the owner
// (for example the picker card could not be sent). Only a record still in the
// "creating" state may be released: anything that advanced to picker/created
// belongs to a real card or task and must keep deduplicating.
func (s *IntakeStore) Release(messageID string) error {
	if messageID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[messageID]
	if !ok || rec.Status != "creating" {
		return nil
	}
	delete(s.records, messageID)
	if err := s.saveLocked(); err != nil {
		s.records[messageID] = rec
		return err
	}
	return nil
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

// SetActiveTarget makes chatID's confirmed destination the default for later
// messages. Node and project are required: a target without them could not
// route anything. TaskID empty means confirmed 「新建」 — messages create tasks
// in that project instead of appending to one. Same chat overwrites; other
// chats are untouched.
func (s *IntakeStore) SetActiveTarget(t ActiveTarget) error {
	t.ChatID = strings.TrimSpace(t.ChatID)
	if t.ChatID == "" {
		return errors.New("feishu intake: chat id required")
	}
	if strings.TrimSpace(t.NodeID) == "" || strings.TrimSpace(t.RootID) == "" {
		return errors.New("feishu intake: target node and project required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	before, had := s.active[t.ChatID]
	t.UpdatedAt = s.now()
	s.active[t.ChatID] = t
	if err := s.saveLocked(); err != nil {
		if had {
			s.active[t.ChatID] = before
		} else {
			delete(s.active, t.ChatID)
		}
		return err
	}
	return nil
}

// ActiveTarget returns the chat's confirmed destination. Expired entries read
// as absent and are dropped lazily, so a chat that went quiet for a week does
// not silently drop messages into a finished task.
func (s *IntakeStore) ActiveTarget(chatID string) (ActiveTarget, bool) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return ActiveTarget{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	t, ok := s.active[chatID]
	return t, ok
}

// ClearActiveTarget forgets the chat's destination (owner pressed 切换目标 后
// 取消, or the task was deleted). Missing entries are a no-op.
func (s *IntakeStore) ClearActiveTarget(chatID string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	before, ok := s.active[chatID]
	if !ok {
		return nil
	}
	delete(s.active, chatID)
	if err := s.saveLocked(); err != nil {
		s.active[chatID] = before
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

// pruneLocked removes expired records and active targets from memory and
// reports whether anything changed. Both windows are the same seven days: a
// chat whose card was never confirmed is as stale as an unhandled message.
// Callers must hold s.mu.
func (s *IntakeStore) pruneLocked() bool {
	cutoff := s.now().Add(-intakeDedupTTL)
	changed := false
	for id, rec := range s.records {
		if rec.CreatedAt.Before(cutoff) {
			delete(s.records, id)
			changed = true
		}
	}
	for chatID, t := range s.active {
		if t.UpdatedAt.Before(cutoff) {
			delete(s.active, chatID)
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
	active := make([]ActiveTarget, 0, len(s.active))
	for _, t := range s.active {
		active = append(active, t)
	}
	// 排序只为了让落盘内容可比（测试与人工排障），语义与顺序无关。
	sort.Slice(active, func(i, j int) bool { return active[i].ChatID < active[j].ChatID })
	raw, err := json.Marshal(intakeStoreFile{Version: 1, Records: records, Active: active})
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
