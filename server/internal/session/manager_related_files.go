package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) AddRelatedFile(_ context.Context, key string, file RelatedFile) error {
	if strings.TrimSpace(file.Path) == "" {
		return errors.New("file path required")
	}
	file.Path = strings.TrimSpace(file.Path)
	file.RootID = strings.TrimSpace(file.RootID)
	file.RepoPath = cleanRelatedRepoPath(file.RepoPath)
	file.RepoName = strings.TrimSpace(file.RepoName)
	file.RepoKind = strings.TrimSpace(file.RepoKind)
	file.Head = strings.TrimSpace(file.Head)
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return err
	}
	sessionChanged := addRelatedFileToSession(session, file)
	parentChanged := false
	var parent *Session
	parentKey := strings.TrimSpace(session.ParentSessionKey)
	if parentKey != "" && parentKey != session.Key {
		parent, err = m.getSessionUnsafe(parentKey, 0)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && parent != nil {
			parentChanged = addRelatedFileToSession(parent, file)
		}
	}
	if !sessionChanged && !parentChanged {
		return nil
	}
	if sessionChanged {
		if err := m.upsertSessionMetaUnsafe(session); err != nil {
			return err
		}
	}
	if parentChanged {
		if err := m.upsertSessionMetaUnsafe(parent); err != nil {
			return err
		}
	}
	return nil
}

func addRelatedFileToSession(session *Session, file RelatedFile) bool {
	if session == nil {
		return false
	}
	for _, existing := range session.RelatedFiles {
		if relatedFileIdentity(existing) == relatedFileIdentity(file) {
			return false
		}
	}
	session.RelatedFiles = append(session.RelatedFiles, file)
	return true
}

func (m *Manager) RemoveRelatedFile(ctx context.Context, key, path string) error {
	return m.RemoveRelatedFileAtHead(ctx, key, path, "", "", "")
}

func (m *Manager) RemoveRelatedFileAtHead(_ context.Context, key, path, head, repoPath, repoKind string) error {
	path = strings.TrimSpace(path)
	head = strings.TrimSpace(head)
	repoPath = strings.TrimSpace(repoPath)
	repoKind = strings.TrimSpace(repoKind)
	if path == "" {
		return errors.New("file path required")
	}
	target := relatedFileIdentity(RelatedFile{Path: path, Head: head, RepoPath: repoPath, RepoKind: repoKind})
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return err
	}
	next := make([]RelatedFile, 0, len(session.RelatedFiles))
	removed := false
	for _, item := range session.RelatedFiles {
		if relatedFileIdentity(item) == target {
			removed = true
			continue
		}
		next = append(next, item)
	}
	if !removed {
		return nil
	}
	session.RelatedFiles = next
	return m.upsertSessionMetaUnsafe(session)
}

func (m *Manager) RecordOutputFile(ctx context.Context, key, path string) error {
	return m.RecordOutputFileAtHead(ctx, key, path, "")
}

func (m *Manager) RecordOutputFileAtHead(ctx context.Context, key, path, head string) error {
	return m.RecordRelatedOutputFile(ctx, key, RelatedFile{Path: path, Head: head})
}

func (m *Manager) RecordOutputFileInRepo(ctx context.Context, key, rootID, repoKind, repoPath, repoName, path, head string) error {
	return m.RecordRelatedOutputFile(ctx, key, RelatedFile{
		RootID:   rootID,
		RepoKind: repoKind,
		RepoPath: repoPath,
		RepoName: repoName,
		Path:     path,
		Head:     head,
	})
}

func (m *Manager) RecordRelatedOutputFile(ctx context.Context, key string, file RelatedFile) error {
	if strings.TrimSpace(file.Path) == "" {
		return errors.New("file path required")
	}
	file.Relation = "output"
	file.CreatedBySession = true
	return m.AddRelatedFile(ctx, key, RelatedFile{
		RootID:           strings.TrimSpace(file.RootID),
		RepoPath:         strings.TrimSpace(file.RepoPath),
		RepoName:         strings.TrimSpace(file.RepoName),
		RepoKind:         strings.TrimSpace(file.RepoKind),
		Path:             strings.TrimSpace(file.Path),
		Head:             strings.TrimSpace(file.Head),
		Relation:         "output",
		CreatedBySession: true,
	})
}

func relatedFileIdentity(file RelatedFile) string {
	return strings.Join([]string{
		strings.TrimSpace(file.RepoKind),
		cleanRelatedRepoPath(file.RepoPath),
		strings.TrimSpace(file.Head),
		strings.TrimSpace(file.Path),
	}, "\x00")
}

func cleanRelatedRepoPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}
