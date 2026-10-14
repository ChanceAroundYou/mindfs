package gitview

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func ReadDiff(ctx context.Context, rootPath, relPath string) (DiffResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return DiffResult{}, err
	}
	items, err := repo.statusItems(ctx)
	if err != nil {
		return DiffResult{}, err
	}
	var matched *StatusItem
	for i := range items {
		if items[i].Path == relPath {
			matched = &items[i]
			break
		}
	}
	if matched == nil {
		return DiffResult{}, errDiffNotFound
	}
	content, err := repo.diffContent(ctx, *matched)
	if err != nil {
		return DiffResult{}, err
	}
	return DiffResult{
		Path:      matched.Path,
		Status:    matched.Status,
		Additions: matched.Additions,
		Deletions: matched.Deletions,
		Content:   content,
	}, nil
}

func ListHistory(ctx context.Context, rootPath string, limit int, beforeCommit, afterCommit string) (HistoryResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return HistoryResult{}, err
		}
		if isNotRepoError(err) {
			return HistoryResult{Available: false, Items: []HistoryItem{}}, nil
		}
		return HistoryResult{}, err
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	beforeCommit = strings.TrimSpace(beforeCommit)
	afterCommit = strings.TrimSpace(afterCommit)
	if beforeCommit != "" && !repo.commitExists(ctx, beforeCommit) {
		return HistoryResult{Available: true, Items: []HistoryItem{}, CommitMissing: true, RemoteHead: repo.remoteHistoryHead(ctx)}, nil
	}
	if afterCommit != "" && !repo.commitExists(ctx, afterCommit) {
		return HistoryResult{Available: true, Items: []HistoryItem{}, CommitMissing: true, RemoteHead: repo.remoteHistoryHead(ctx)}, nil
	}
	remoteHead := repo.remoteHistoryHead(ctx)

	args := []string{"log", "--format=%H%x00%s%x00%cI%x00", fmt.Sprintf("--max-count=%d", limit+1)}
	if afterCommit != "" {
		args = append(args, afterCommit+"..HEAD")
	} else if beforeCommit != "" {
		parents, err := repo.commitParents(ctx, beforeCommit)
		if err != nil {
			return HistoryResult{}, err
		}
		if len(parents) == 0 {
			return HistoryResult{Available: true, Items: []HistoryItem{}, HasMore: false, RemoteHead: remoteHead}, nil
		}
		args = append(args, parents...)
	}
	if repo.prefix != "" {
		args = append(args, "--", repo.prefix)
	}
	output, err := runGit(ctx, repo.repoRoot, args...)
	if err != nil {
		return HistoryResult{}, err
	}
	items := parseHistoryItems(output)
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	repo.markRemoteHistoryItems(ctx, items)
	return HistoryResult{Available: true, Items: items, HasMore: hasMore, RemoteHead: remoteHead}, nil
}

func ListCommitFiles(ctx context.Context, rootPath, commit string) (CommitFilesResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return CommitFilesResult{}, err
	}
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return CommitFilesResult{}, errors.New("commit required")
	}
	if !repo.commitExists(ctx, commit) {
		return CommitFilesResult{}, errors.New("commit not found")
	}
	statusItems, err := repo.commitNameStatus(ctx, commit)
	if err != nil {
		return CommitFilesResult{}, err
	}
	stats, err := repo.commitNumstat(ctx, commit)
	if err != nil {
		return CommitFilesResult{}, err
	}
	items := make([]StatusItem, 0, len(statusItems))
	for _, item := range statusItems {
		path := repo.fromRepoPath(item.Path)
		if path == "" {
			continue
		}
		oldPath := repo.fromRepoPath(item.OldPath)
		stat := stats[item.Path]
		items = append(items, StatusItem{
			Path:      strings.TrimSuffix(path, "/"),
			OldPath:   oldPath,
			Status:    item.Status,
			Additions: stat[0],
			Deletions: stat[1],
		})
	}
	return CommitFilesResult{Commit: commit, Items: items}, nil
}

func ReadCommitDiff(ctx context.Context, rootPath, commit, relPath string) (DiffResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return DiffResult{}, err
	}
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return DiffResult{}, errors.New("commit required")
	}
	if !repo.commitExists(ctx, commit) {
		return DiffResult{}, errors.New("commit not found")
	}
	path := strings.TrimSpace(relPath)
	if path == "" {
		return DiffResult{}, errors.New("path required")
	}
	repoPath := repo.toRepoPath(path)
	files, err := repo.commitNameStatus(ctx, commit)
	if err != nil {
		return DiffResult{}, err
	}
	stats, err := repo.commitNumstat(ctx, commit)
	if err != nil {
		return DiffResult{}, err
	}
	var matched *porcelainItem
	for i := range files {
		if files[i].Path == repoPath {
			matched = &files[i]
			break
		}
	}
	if matched == nil {
		return DiffResult{}, errors.New("git commit diff not found for path")
	}
	contentBytes, err := runGitBytes(ctx, repo.repoRoot, "show", "--format=", "--no-ext-diff", "--find-renames", commit, "--", repoPath)
	if err != nil {
		return DiffResult{}, err
	}
	content := decodeGitDiffOutput(contentBytes, filepath.Ext(matched.Path))
	stat := stats[matched.Path]
	return DiffResult{
		Path:      path,
		OldPath:   repo.fromRepoPath(matched.OldPath),
		Status:    matched.Status,
		Additions: stat[0],
		Deletions: stat[1],
		Content:   content,
	}, nil
}

// emptyRelatedFileDiff 表示「这个文件自记录的基线以来没有任何变更可显示」：
// base 之后没有提交碰过它，工作区也没有改动（或被 git 忽略/已删除）。
// 这是空结果而非错误，回 200 空 diff 让前端渲染空面板，不要污染日志。
// Status 故意留空，前端据此判断「无可显示的增删统计」。
func emptyRelatedFileDiff(path string) RelatedFileDiffResult {
	return RelatedFileDiffResult{
		DiffResult: DiffResult{Path: path},
		Source:     "none",
	}
}

func ReadRelatedFileDiff(ctx context.Context, rootPath, baseHead, relPath string) (RelatedFileDiffResult, error) {
	repo, err := loadRepoContext(ctx, rootPath)
	if err != nil {
		return RelatedFileDiffResult{}, err
	}
	baseHead = strings.TrimSpace(baseHead)
	if baseHead == "" {
		diff, err := ReadDiff(ctx, rootPath, relPath)
		if errors.Is(err, errDiffNotFound) {
			return emptyRelatedFileDiff(relPath), nil
		}
		if err != nil {
			return RelatedFileDiffResult{}, err
		}
		return RelatedFileDiffResult{DiffResult: diff, Source: "worktree"}, nil
	}
	if !repo.commitExists(ctx, baseHead) {
		return RelatedFileDiffResult{}, errors.New("记录的提交不存在或不在当前分支历史中")
	}
	if !repo.commitInCurrentHistory(ctx, baseHead) {
		return RelatedFileDiffResult{}, errors.New("记录的提交不存在或不在当前分支历史中")
	}
	path := strings.TrimSpace(relPath)
	if path == "" {
		return RelatedFileDiffResult{}, errors.New("path required")
	}
	nextHead, err := repo.nextCommitTouching(ctx, baseHead, repo.toRepoPath(path))
	if err != nil {
		return RelatedFileDiffResult{}, err
	}
	if strings.TrimSpace(nextHead) == "" {
		diff, err := ReadDiff(ctx, rootPath, path)
		if errors.Is(err, errDiffNotFound) {
			return emptyRelatedFileDiff(path), nil
		}
		if err != nil {
			return RelatedFileDiffResult{}, err
		}
		return RelatedFileDiffResult{
			DiffResult: diff,
			BaseHead:   baseHead,
			Source:     "worktree",
		}, nil
	}
	diff, err := repo.diffBetweenCommits(ctx, baseHead, nextHead, path)
	if err != nil {
		return RelatedFileDiffResult{}, err
	}
	return RelatedFileDiffResult{
		DiffResult: diff,
		BaseHead:   baseHead,
		TargetHead: nextHead,
		Source:     "commit_range",
	}, nil
}

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

func (r repoContext) commitNameStatus(ctx context.Context, commit string) ([]porcelainItem, error) {
	output, err := runGit(ctx, r.repoRoot, "diff-tree", "--root", "--no-commit-id", "--name-status", "-r", "-z", "-M", commit)
	if err != nil {
		return nil, err
	}
	return parseNameStatusZ(output), nil
}

func (r repoContext) nameStatusBetween(ctx context.Context, base, target string) ([]porcelainItem, error) {
	output, err := runGit(ctx, r.repoRoot, "diff", "--name-status", "-z", "-M", base, target)
	if err != nil {
		return nil, err
	}
	return parseNameStatusZ(output), nil
}

func parseNameStatusZ(output string) []porcelainItem {
	parts := strings.Split(output, "\x00")
	items := make([]porcelainItem, 0)
	for i := 0; i < len(parts); {
		status := strings.TrimSpace(parts[i])
		i++
		if status == "" {
			continue
		}
		code := status
		if len(code) > 1 {
			code = code[:1]
		}
		normalized := "M"
		switch code {
		case "A":
			normalized = "A"
		case "D":
			normalized = "D"
		case "R", "C":
			normalized = "R"
		}
		if normalized == "R" {
			if i+1 >= len(parts) {
				break
			}
			oldPath := strings.TrimSpace(parts[i])
			newPath := strings.TrimSpace(parts[i+1])
			i += 2
			if newPath != "" {
				items = append(items, porcelainItem{Path: newPath, OldPath: oldPath, Status: normalized})
			}
			continue
		}
		if i >= len(parts) {
			break
		}
		path := strings.TrimSpace(parts[i])
		i++
		if path != "" {
			items = append(items, porcelainItem{Path: path, Status: normalized})
		}
	}
	return items
}

func (r repoContext) commitNumstat(ctx context.Context, commit string) (map[string][2]int, error) {
	output, err := runGit(ctx, r.repoRoot, "diff-tree", "--root", "--no-commit-id", "--numstat", "-r", "-M", commit)
	if err != nil {
		return nil, err
	}
	return parseNumstat(output)
}

func (r repoContext) numstatBetween(ctx context.Context, base, target string) (map[string][2]int, error) {
	output, err := runGit(ctx, r.repoRoot, "diff", "--numstat", "-M", base, target)
	if err != nil {
		return nil, err
	}
	return parseNumstat(output)
}

func parseNumstat(output string) (map[string][2]int, error) {
	result := make(map[string][2]int)
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 3 {
			continue
		}
		var add, del int
		if fields[0] != "-" {
			fmt.Sscanf(fields[0], "%d", &add)
		}
		if fields[1] != "-" {
			fmt.Sscanf(fields[1], "%d", &del)
		}
		path := fields[len(fields)-1]
		result[path] = [2]int{add, del}
	}
	return result, scanner.Err()
}

func (r repoContext) toRepoPath(rootRelativePath string) string {
	if r.prefix == "" {
		return filepath.ToSlash(rootRelativePath)
	}
	if rootRelativePath == "" || rootRelativePath == "." {
		return r.prefix
	}
	return filepath.ToSlash(pathJoinSlash(r.prefix, rootRelativePath))
}

func (r repoContext) fromRepoPath(repoRelativePath string) string {
	value := filepath.ToSlash(strings.TrimSpace(repoRelativePath))
	if value == "" {
		return ""
	}
	if r.prefix == "" {
		return value
	}
	if value == r.prefix {
		return "."
	}
	prefix := r.prefix + "/"
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	return strings.TrimPrefix(value, prefix)
}

func parseHistoryItems(output string) []HistoryItem {
	parts := strings.Split(output, "\x00")
	items := make([]HistoryItem, 0, len(parts)/3)
	for i := 0; i+2 < len(parts); i += 3 {
		hash := strings.TrimSpace(parts[i])
		if hash == "" {
			continue
		}
		items = append(items, HistoryItem{
			Hash:       hash,
			Message:    strings.TrimSpace(parts[i+1]),
			CommitTime: strings.TrimSpace(parts[i+2]),
		})
	}
	return items
}
