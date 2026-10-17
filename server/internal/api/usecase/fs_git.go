package usecase

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"mindfs/server/internal/fs"
	"mindfs/server/internal/gitview"
)

type GitStatusInput struct {
	RootID   string
	RootPath string
}

type GitStatusOutput struct {
	Status gitview.StatusResult
}

type GitDiffInput struct {
	RootID   string
	RepoPath string
	Path     string
}

type GitDiffOutput struct {
	Diff gitview.DiffResult
}

type GitHistoryInput struct {
	RootID       string
	Limit        int
	BeforeCommit string
	AfterCommit  string
}

type GitHistoryOutput struct {
	History gitview.HistoryResult
}

type GitCommitFilesInput struct {
	RootID string
	Commit string
}

type GitCommitFilesOutput struct {
	Files gitview.CommitFilesResult
}

type GitCommitDiffInput struct {
	RootID string
	Commit string
	Path   string
}

type GitCommitDiffOutput struct {
	Diff gitview.DiffResult
}

type GitRelatedFileDiffInput struct {
	RootID   string
	RepoPath string
	RepoKind string
	Head     string
	Path     string
}

type GitRelatedFileDiffOutput struct {
	Diff gitview.RelatedFileDiffResult
}

// GitRelatedFileStatTargetInput 是批量统计里的单个文件。
// ID 是调用方生成的不透明键，原样回传 —— 有条目被跳过时前端才不会错位。
type GitRelatedFileStatTargetInput struct {
	ID       string
	Path     string
	RepoPath string
	RepoKind string
	Head     string
}

type GitRelatedFileStatsInput struct {
	RootID  string
	Targets []GitRelatedFileStatTargetInput
}

type GitRelatedFileStatOutput struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type GitRelatedFileStatsOutput struct {
	Stats []GitRelatedFileStatOutput `json:"stats"`
}

// GetGitRelatedFileStats 批量取关联文件的增删统计，不取 diff 正文。
//
// 与逐文件调 GetGitRelatedFileDiff 的区别只在成本，不在语义：每个文件仍走同一个
// resolveGitRelatedFileDiffTarget 解析（worktree 回落、基线补齐都在里面），
// 只是按解析出的仓库分组，每组一次性交给 gitview.ReadRelatedFileStats。
// 也不做 FileMeta 填充 —— 徽标用不到它，而那是每文件一次 root.GetFileMeta。
func (s *Service) GetGitRelatedFileStats(ctx context.Context, in GitRelatedFileStatsInput) (GitRelatedFileStatsOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return GitRelatedFileStatsOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return GitRelatedFileStatsOutput{}, err
	}

	// 按解析出的仓库分组，保持输入顺序，结果才好按 ID 对齐。
	type group struct {
		rootPath string
		targets  []gitview.RelatedFileStatTarget
		slots    []int // 每个 target 对应 stats 里的下标
	}
	stats := make([]GitRelatedFileStatOutput, 0, len(in.Targets))
	groups := make([]*group, 0, 2)
	groupByRepo := make(map[string]*group, 2)

	for _, target := range in.Targets {
		stats = append(stats, GitRelatedFileStatOutput{ID: target.ID, Path: target.Path})
		slot := len(stats) - 1

		path := strings.TrimSpace(target.Path)
		if path == "" || strings.TrimSpace(target.RepoKind) == "plain" {
			continue
		}
		rootPath, _, resolvedPath, head, err := resolveGitRelatedFileDiffTarget(ctx, root, GitRelatedFileDiffInput{
			RootID:   in.RootID,
			RepoPath: target.RepoPath,
			RepoKind: target.RepoKind,
			Head:     target.Head,
			Path:     path,
		})
		if err != nil {
			// 单文件解析失败只让它自己留空，不牵连整批。
			continue
		}
		current, ok := groupByRepo[rootPath]
		if !ok {
			current = &group{rootPath: rootPath}
			groupByRepo[rootPath] = current
			groups = append(groups, current)
		}
		current.targets = append(current.targets, gitview.RelatedFileStatTarget{Path: resolvedPath, BaseHead: head})
		current.slots = append(current.slots, slot)
	}

	for _, current := range groups {
		results, err := gitview.ReadRelatedFileStats(ctx, current.rootPath, current.targets)
		if err != nil {
			continue // 该仓库整体留空，其它仓库不受影响
		}
		for i, result := range results {
			if i >= len(current.slots) {
				break
			}
			slot := current.slots[i]
			stats[slot].Status = result.Status
			stats[slot].Additions = result.Additions
			stats[slot].Deletions = result.Deletions
		}
	}

	return GitRelatedFileStatsOutput{Stats: stats}, nil
}

func (s *Service) GetGitStatus(ctx context.Context, in GitStatusInput) (GitStatusOutput, error) {
	if strings.TrimSpace(in.RootPath) != "" {
		rootPath := filepath.Clean(strings.TrimSpace(in.RootPath))
		if !filepath.IsAbs(rootPath) {
			return GitStatusOutput{}, errors.New("path must be absolute")
		}
		status, err := gitview.InspectStatus(ctx, rootPath)
		if err != nil {
			return GitStatusOutput{}, err
		}
		return GitStatusOutput{Status: status}, nil
	}
	if err := s.ensureRegistry(); err != nil {
		return GitStatusOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return GitStatusOutput{}, err
	}
	status, err := gitview.InspectStatus(ctx, root.RootPath)
	if err != nil {
		return GitStatusOutput{}, err
	}
	return GitStatusOutput{Status: status}, nil
}

func (s *Service) GetGitDiff(ctx context.Context, in GitDiffInput) (GitDiffOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return GitDiffOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return GitDiffOutput{}, err
	}
	if strings.TrimSpace(in.Path) == "" {
		return GitDiffOutput{}, errors.New("path required")
	}
	path := strings.TrimSpace(in.Path)
	repoPath := strings.TrimSpace(in.RepoPath)
	rootPath := root.RootPath
	if repoPath != "" {
		if !filepath.IsAbs(repoPath) {
			return GitDiffOutput{}, errors.New("repo path must be absolute")
		}
		rootPath = filepath.Clean(repoPath)
	} else {
		var err error
		path, err = root.NormalizePath(path)
		if err != nil {
			return GitDiffOutput{}, err
		}
	}
	diff, err := gitview.ReadDiff(ctx, rootPath, path)
	if err != nil {
		return GitDiffOutput{}, err
	}
	var meta []fs.FileMetaEntry
	if repoPath == "" || sameManagedDirPath(repoPath, root.RootPath) {
		if value, err := root.GetFileMeta(path); err == nil {
			meta = value
		}
	}
	diff.FileMeta = fillFileMetaSessionInfo(ctx, s, in.RootID, meta)
	return GitDiffOutput{Diff: diff}, nil
}

func (s *Service) GetGitHistory(ctx context.Context, in GitHistoryInput) (GitHistoryOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return GitHistoryOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return GitHistoryOutput{}, err
	}
	history, err := gitview.ListHistory(ctx, root.RootPath, in.Limit, in.BeforeCommit, in.AfterCommit)
	if err != nil {
		return GitHistoryOutput{}, err
	}
	return GitHistoryOutput{History: history}, nil
}

func (s *Service) GetGitCommitFiles(ctx context.Context, in GitCommitFilesInput) (GitCommitFilesOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return GitCommitFilesOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return GitCommitFilesOutput{}, err
	}
	if strings.TrimSpace(in.Commit) == "" {
		return GitCommitFilesOutput{}, errors.New("commit required")
	}
	files, err := gitview.ListCommitFiles(ctx, root.RootPath, in.Commit)
	if err != nil {
		return GitCommitFilesOutput{}, err
	}
	return GitCommitFilesOutput{Files: files}, nil
}

func (s *Service) GetGitCommitDiff(ctx context.Context, in GitCommitDiffInput) (GitCommitDiffOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return GitCommitDiffOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return GitCommitDiffOutput{}, err
	}
	if strings.TrimSpace(in.Commit) == "" {
		return GitCommitDiffOutput{}, errors.New("commit required")
	}
	if strings.TrimSpace(in.Path) == "" {
		return GitCommitDiffOutput{}, errors.New("path required")
	}
	path, err := root.NormalizePath(in.Path)
	if err != nil {
		return GitCommitDiffOutput{}, err
	}
	diff, err := gitview.ReadCommitDiff(ctx, root.RootPath, in.Commit, path)
	if err != nil {
		return GitCommitDiffOutput{}, err
	}
	meta, err := root.GetFileMeta(path)
	if err != nil {
		meta = nil
	}
	diff.FileMeta = fillFileMetaSessionInfo(ctx, s, in.RootID, meta)
	return GitCommitDiffOutput{Diff: diff}, nil
}

func (s *Service) GetGitRelatedFileDiff(ctx context.Context, in GitRelatedFileDiffInput) (GitRelatedFileDiffOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return GitRelatedFileDiffOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return GitRelatedFileDiffOutput{}, err
	}
	if strings.TrimSpace(in.Path) == "" {
		return GitRelatedFileDiffOutput{}, errors.New("path required")
	}
	rootPath, repoPath, path, head, err := resolveGitRelatedFileDiffTarget(ctx, root, in)
	if err != nil {
		return GitRelatedFileDiffOutput{}, err
	}
	diff, err := gitview.ReadRelatedFileDiff(ctx, rootPath, head, path)
	if err != nil {
		return GitRelatedFileDiffOutput{}, err
	}
	var meta []fs.FileMetaEntry
	if repoPath == "" || sameManagedDirPath(repoPath, root.RootPath) {
		if value, err := root.GetFileMeta(path); err == nil {
			meta = value
		}
	}
	diff.FileMeta = fillFileMetaSessionInfo(ctx, s, in.RootID, meta)
	return GitRelatedFileDiffOutput{Diff: diff}, nil
}
