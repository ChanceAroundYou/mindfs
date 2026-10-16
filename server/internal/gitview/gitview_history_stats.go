package gitview

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// ReadRelatedFileStats 批量读取关联文件的增删统计，**不取 diff 正文**。
//
// 存在的理由：会话/任务视图要的只是每个关联文件的 +N −M 徽标，而逐文件调
// ReadRelatedFileDiff 时，一个 80 文件的列表会触发 80 次完整的 git diff —— 正文
// 占响应体积的绝大部分（实测 6473B 里 5073B 是 content），而调用方一个字节都不读。
//
// 逐字段语义与 ReadRelatedFileDiff 的 status/additions/deletions 一致，省在三处：
//  1. loadRepoContext 只做一次（原本每文件一次，含 EvalSymlinks + 两次 rev-parse）；
//  2. 同一 (base, nextHead) 区间的 name-status / numstat 只取一次 —— 一个任务的关联
//     文件通常共享同一个基线，原本每个文件各取一遍，这部分是全量重复；
//  3. 基线合法性（cat-file / merge-base）按基线记忆化，不再逐文件重问；
//  4. 不跑 git diff 取正文。
//
// ponytail: nextCommitTouching 仍是逐文件一条 rev-list —— 它便宜（工作树内）且
// 语义必须与 ReadRelatedFileDiff 完全一致，不拿 `git log --name-only` 一把算：
// 合并提交下 log 默认不列文件、而 rev-list 的 pathspec 限制会把合并算作触及该路径，
// 两者会分叉成静默错误的徽标。要再压这个天花板，先验证 80 个路径是否共享同一个
// nextHead（共享时整批可退化成一次 diff --numstat）。
func ReadRelatedFileStats(ctx context.Context, rootPath string, targets []RelatedFileStatTarget) ([]RelatedFileStat, error) {
	stats := make([]RelatedFileStat, len(targets))
	for i := range targets {
		stats[i] = RelatedFileStat{Path: targets[i].Path}
	}
	if len(targets) == 0 {
		return stats, nil
	}
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return nil, err
	}

	// 工作区源的统计整批共用一份：statusItems 内部本来就是一次批量 numstat。
	var worktreeItems map[string]StatusItem
	worktreeLoaded := false
	loadWorktree := func() map[string]StatusItem {
		if worktreeLoaded {
			return worktreeItems
		}
		worktreeLoaded = true
		items, err := repo.statusItems(ctx)
		if err != nil {
			return nil
		}
		worktreeItems = make(map[string]StatusItem, len(items))
		for _, item := range items {
			worktreeItems[item.Path] = item
		}
		return worktreeItems
	}

	// 同一基线的合法性只问一次（原本每个文件 cat-file + merge-base 各一次）。
	baseValid := make(map[string]bool, 2)
	validBase := func(base string) bool {
		if value, ok := baseValid[base]; ok {
			return value
		}
		value := repo.commitExists(ctx, base) && repo.commitInCurrentHistory(ctx, base)
		baseValid[base] = value
		return value
	}

	// 1) 逐个解析目标区间。分支顺序与 ReadRelatedFileDiff 一致：无基线 → 工作区；
	//    基线非法 → 留空（逐文件时是错误，前端同样不显示徽标）；区间里没有它 → 工作区。
	type commitRange struct{ base, target string }
	byRange := make(map[commitRange][]int)
	worktreeIndexes := make([]int, 0, len(targets))
	for i := range targets {
		base := strings.TrimSpace(targets[i].BaseHead)
		if base == "" || !validBase(base) {
			if base == "" {
				worktreeIndexes = append(worktreeIndexes, i)
			}
			continue
		}
		next, err := repo.nextCommitTouching(ctx, base, repo.toRepoPath(targets[i].Path))
		if err != nil || strings.TrimSpace(next) == "" {
			worktreeIndexes = append(worktreeIndexes, i)
			continue
		}
		key := commitRange{base: base, target: strings.TrimSpace(next)}
		byRange[key] = append(byRange[key], i)
	}

	// 2) 每个区间各取一次 name-status + numstat，覆盖该区间内的全部文件。
	//    匹配顺序与 diffBetweenCommits 相同：按 Path 或 OldPath 命中**第一个**。
	for key, indexes := range byRange {
		files, err := repo.nameStatusBetween(ctx, key.base, key.target)
		if err != nil {
			continue // 只让这个区间留空，不牵连其它区间
		}
		numstat, err := repo.numstatBetween(ctx, key.base, key.target)
		if err != nil {
			continue
		}
		for _, i := range indexes {
			repoPath := repo.toRepoPath(targets[i].Path)
			for fi := range files {
				if files[fi].Path != repoPath && files[fi].OldPath != repoPath {
					continue
				}
				stats[i].Status = files[fi].Status
				if value, ok := numstat[files[fi].Path]; ok {
					stats[i].Additions = value[0]
					stats[i].Deletions = value[1]
				}
				break
			}
		}
	}

	// 3) 工作区源的批量落值。
	if len(worktreeIndexes) > 0 {
		if items := loadWorktree(); items != nil {
			for _, i := range worktreeIndexes {
				item, ok := items[targets[i].Path]
				if !ok {
					continue
				}
				stats[i].Status = item.Status
				stats[i].Additions = item.Additions
				stats[i].Deletions = item.Deletions
			}
		}
	}
	return stats, nil
}

func (r repoContext) commitExists(ctx context.Context, commit string) bool {
	if strings.TrimSpace(commit) == "" {
		return false
	}
	_, err := runGit(ctx, r.repoRoot, "cat-file", "-e", commit+"^{commit}")
	return err == nil
}

func (r repoContext) commitInCurrentHistory(ctx context.Context, commit string) bool {
	if strings.TrimSpace(commit) == "" {
		return false
	}
	_, err := runGit(ctx, r.repoRoot, "merge-base", "--is-ancestor", commit, "HEAD")
	return err == nil
}

// nextCommitTouching 返回 base..HEAD 中第一个改动了 repoRelPath 的提交。
// 记录的 base 是「文件被改动那一刻的 HEAD」，真正带上这笔改动的提交与它之间
// 常常夹着若干没碰过该文件的提交，所以不能取紧接着的那一个。
func (r repoContext) nextCommitTouching(ctx context.Context, commit, repoRelPath string) (string, error) {
	args := []string{"rev-list", "--reverse", "--ancestry-path", commit + "..HEAD"}
	if strings.TrimSpace(repoRelPath) != "" {
		args = append(args, "--", repoRelPath)
	}
	output, err := runGit(ctx, r.repoRoot, args...)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(output, "\n") {
		value := strings.TrimSpace(line)
		if value != "" {
			return value, nil
		}
	}
	return "", nil
}

func (r repoContext) diffBetweenCommits(ctx context.Context, base, target, relPath string) (DiffResult, error) {
	path := strings.TrimSpace(relPath)
	if path == "" {
		return DiffResult{}, errors.New("path required")
	}
	repoPath := r.toRepoPath(path)
	files, err := r.nameStatusBetween(ctx, base, target)
	if err != nil {
		return DiffResult{}, err
	}
	stats, err := r.numstatBetween(ctx, base, target)
	if err != nil {
		return DiffResult{}, err
	}
	var matched *porcelainItem
	for i := range files {
		if files[i].Path == repoPath || files[i].OldPath == repoPath {
			matched = &files[i]
			break
		}
	}
	if matched == nil {
		return DiffResult{}, errDiffNotFound
	}
	contentBytes, err := runGitBytes(ctx, r.repoRoot, "diff", "--no-ext-diff", "--find-renames", base, target, "--", repoPath)
	if err != nil {
		return DiffResult{}, err
	}
	content := decodeGitDiffOutput(contentBytes, filepath.Ext(matched.Path))
	stat := stats[matched.Path]
	return DiffResult{
		Path:      r.fromRepoPath(matched.Path),
		OldPath:   r.fromRepoPath(matched.OldPath),
		Status:    matched.Status,
		Additions: stat[0],
		Deletions: stat[1],
		Content:   content,
	}, nil
}

func (r repoContext) commitParents(ctx context.Context, commit string) ([]string, error) {
	output, err := runGit(ctx, r.repoRoot, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(output)
	if len(fields) <= 1 {
		return []string{}, nil
	}
	return fields[1:], nil
}

func (r repoContext) markRemoteHistoryItems(ctx context.Context, items []HistoryItem) {
	for i := range items {
		output, err := runGit(ctx, r.repoRoot, "branch", "-r", "--contains", items[i].Hash, "--format=%(refname:short)")
		if err != nil {
			continue
		}
		items[i].Remote = strings.TrimSpace(output) != ""
	}
}

func (r repoContext) remoteHistoryHead(ctx context.Context) string {
	upstream, err := runGit(ctx, r.repoRoot, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if err != nil {
		return ""
	}
	upstream = strings.TrimSpace(upstream)
	if upstream == "" {
		return ""
	}
	mergeBase, err := runGit(ctx, r.repoRoot, "merge-base", "HEAD", upstream)
	if err != nil {
		return ""
	}
	mergeBase = strings.TrimSpace(mergeBase)
	if mergeBase == "" {
		return ""
	}
	if r.prefix == "" {
		return mergeBase
	}
	output, err := runGit(ctx, r.repoRoot, "log", "--format=%H", "--max-count=1", mergeBase, "--", r.prefix)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}
