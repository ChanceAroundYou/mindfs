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

// RemoveRelatedFilesUnder 清掉落在 dir 之下（含 dir 自身）的 related 条目。
//
// 用途只有一个：repoint 换目录之后，旧工作树里那批文件已经不在会话干活的地方了
// （目录多半已被删），留着它们会让「相关文件」面板指向一堆不存在的路径。按前缀删，
// 不按 identity 逐条删 —— 调用方手里只有旧目录，没有那批条目的 head/kind。
// 判前缀时两侧都先 Clean 并补上分隔符，避免 /a/bc 被 /a/b 误伤。
func (m *Manager) RemoveRelatedFilesUnder(_ context.Context, key, dir string) (int, error) {
	key = strings.TrimSpace(key)
	dir = strings.TrimSpace(dir)
	if key == "" {
		return 0, errors.New("session key required")
	}
	if dir == "" || !filepath.IsAbs(dir) {
		return 0, errors.New("absolute directory required")
	}
	prefix := filepath.Clean(dir) + string(filepath.Separator)
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionUnsafe(key, 0)
	if err != nil {
		return 0, err
	}
	next := make([]RelatedFile, 0, len(session.RelatedFiles))
	removed := 0
	for _, item := range session.RelatedFiles {
		if relatedFileUnderDir(item, prefix) {
			removed++
			continue
		}
		next = append(next, item)
	}
	if removed == 0 {
		return 0, nil
	}
	session.RelatedFiles = next
	if err := m.upsertSessionMetaUnsafe(session); err != nil {
		return 0, err
	}
	return removed, nil
}

// relatedFileUnderDir：Path 或 RepoPath 任一落在 dir 下就算命中。工作树里记的条目
// 两个字段都是工作树路径（RepoPath = 那个工作树的仓库根），任一个命中都该清。
func relatedFileUnderDir(file RelatedFile, prefix string) bool {
	for _, candidate := range []string{file.Path, file.RepoPath} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || !filepath.IsAbs(candidate) {
			continue
		}
		if strings.HasPrefix(filepath.Clean(candidate)+string(filepath.Separator), prefix) {
			return true
		}
	}
	return false
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
