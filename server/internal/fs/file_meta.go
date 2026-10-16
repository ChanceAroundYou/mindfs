package fs

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"
)

// State captures cursor/position info for a managed directory.
type State struct {
	Cursor   string `json:"cursor,omitempty"`
	Position int    `json:"position,omitempty"`
}

func (r RootInfo) LoadState() (State, error) {
	payload, err := r.ReadMetaFile(stateFileName)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(payload, &state); err != nil {
		return State{}, err
	}
	return state, nil
}

type FileMetaEntry struct {
	SourceSession string    `json:"source_session"`
	SessionName   string    `json:"session_name,omitempty"`
	Agent         string    `json:"agent,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
	CreatedBy     string    `json:"created_by"`
}

type FileMeta map[string][]FileMetaEntry

func (r RootInfo) LoadFileMeta() (FileMeta, error) {
	payload, err := r.ReadMetaFile("file-meta.json")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileMeta{}, nil
		}
		return nil, err
	}
	var meta FileMeta
	if err := json.Unmarshal(payload, &meta); err == nil {
		if meta == nil {
			meta = FileMeta{}
		}
		return meta, nil
	}

	// Backward compatibility: old format was map[path]FileMetaEntry.
	var legacy map[string]FileMetaEntry
	if err := json.Unmarshal(payload, &legacy); err != nil {
		return nil, err
	}
	meta = FileMeta{}
	for path, entry := range legacy {
		meta[path] = []FileMetaEntry{entry}
	}
	return meta, nil
}

func (r RootInfo) SaveFileMeta(meta FileMeta) error {
	if meta == nil {
		meta = FileMeta{}
	}
	payload, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return r.WriteMetaFile("file-meta.json", payload)
}

func (r RootInfo) UpdateFileMeta(relativePath, sessionKey, createdBy string) error {
	if relativePath == "" {
		return errors.New("path required")
	}
	lockPath, err := r.resolveMetaPath("file-meta.json")
	if err != nil {
		return err
	}
	lock := metaFileLock(lockPath)
	lock.Lock()
	defer lock.Unlock()
	meta, err := r.LoadFileMeta()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	entries := meta[relativePath]
	updated := false
	for i := range entries {
		if entries[i].SourceSession == sessionKey {
			entries[i].UpdatedAt = now
			if entries[i].CreatedBy == "" {
				entries[i].CreatedBy = createdBy
			}
			updated = true
			break
		}
	}
	if !updated {
		entries = append(entries, FileMetaEntry{
			SourceSession: sessionKey,
			CreatedAt:     now,
			UpdatedAt:     now,
			CreatedBy:     createdBy,
		})
	}
	meta[relativePath] = entries
	return r.SaveFileMeta(meta)
}

func (r RootInfo) GetFileMeta(relativePath string) ([]FileMetaEntry, error) {
	meta, err := r.LoadFileMeta()
	if err != nil {
		return nil, err
	}
	if entry, ok := meta[relativePath]; ok {
		return entry, nil
	}
	return nil, nil
}

func (r RootInfo) RemoveSessionFileMeta(sessionKey string) error {
	if strings.TrimSpace(sessionKey) == "" {
		return errors.New("session key required")
	}
	lockPath, err := r.resolveMetaPath("file-meta.json")
	if err != nil {
		return err
	}
	lock := metaFileLock(lockPath)
	lock.Lock()
	defer lock.Unlock()
	meta, err := r.LoadFileMeta()
	if err != nil {
		return err
	}
	changed := false
	for path, entries := range meta {
		filtered := entries[:0]
		for _, entry := range entries {
			if entry.SourceSession == sessionKey {
				changed = true
				continue
			}
			filtered = append(filtered, entry)
		}
		if len(filtered) == 0 {
			delete(meta, path)
			continue
		}
		meta[path] = filtered
	}
	if !changed {
		return nil
	}
	return r.SaveFileMeta(meta)
}
