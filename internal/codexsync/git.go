package codexsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func syncRepository(ctx context.Context, repoRoot string) (Result, error) {
	if err := ensureRepository(repoRoot); err != nil {
		return Result{State: "unavailable", Message: "configured .agents Git checkout is unavailable"}, err
	}
	if inProgress, err := rebaseInProgress(ctx, repoRoot); err != nil {
		return Result{State: "pending", Message: "could not inspect Git operation state"}, err
	} else if inProgress {
		return Result{State: "conflict", Message: "a Git rebase or merge is already in progress"}, errors.New("resolve the existing Git operation before syncing")
	}
	dirtyPaths, err := worktreePaths(ctx, repoRoot)
	if err != nil {
		return Result{State: "pending", Message: "could not inspect Git worktree"}, err
	}
	if err := ensureAllowedPaths(dirtyPaths); err != nil {
		return Result{State: "pending", Message: "unrelated changes are present in the .agents worktree"}, err
	}
	remote, branch, err := configuredUpstream(ctx, repoRoot)
	if err != nil {
		return Result{State: "unavailable", Message: "configure an upstream branch for this .agents checkout"}, err
	}
	upstreamRef := "refs/remotes/" + remote + "/" + branch
	branchRef := "refs/heads/" + branch
	if err := fetchUpstream(ctx, repoRoot, remote, branchRef, upstreamRef); err != nil {
		return Result{State: "pending", Message: "fetch failed; local shared changes remain available for retry"}, err
	}
	if err := ensureCommitsAllowed(ctx, repoRoot, upstreamRef); err != nil {
		return Result{State: "pending", Message: "local commits include paths outside the shared-config allowlist"}, err
	}
	if len(dirtyPaths) > 0 {
		if err := ensureCommitIdentity(ctx, repoRoot); err != nil {
			return Result{State: "pending", Message: "configure Git commit identity before syncing"}, err
		}
		addArgs := []string{"add", "--all", "--"}
		addArgs = append(addArgs, dirtyPaths...)
		if _, _, err := runGit(ctx, repoRoot, addArgs...); err != nil {
			return Result{State: "pending", Message: "could not stage allowlisted shared files"}, err
		}
		staged, err := stagedPaths(ctx, repoRoot)
		if err != nil {
			return Result{State: "pending", Message: "could not inspect staged Git changes"}, err
		}
		if err := ensureAllowedPaths(staged); err != nil {
			return Result{State: "pending", Message: "staging contains paths outside the shared-config allowlist"}, err
		}
		if len(staged) > 0 {
			if _, _, err := runGit(ctx, repoRoot, "commit", "-m", "chore: sync Codex configuration"); err != nil {
				return Result{State: "pending", Message: "could not commit shared configuration"}, err
			}
		}
	}

	commitIDs := make([]string, 0, 1)
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if err := fetchUpstream(ctx, repoRoot, remote, branchRef, upstreamRef); err != nil {
				return Result{State: "pending", CommitIDs: commitIDs, Message: "fetch failed during push retry; local commits remain available"}, err
			}
		}
		if err := integrateUpstream(ctx, repoRoot, upstreamRef); err != nil {
			state, message := "pending", "could not integrate the configured upstream"
			if conflict, conflictErr := unresolvedConflictPaths(ctx, repoRoot); conflictErr == nil && len(conflict) > 0 {
				state = "conflict"
				message = "shared files conflict; resolve these paths before syncing: " + strings.Join(conflict, ", ")
			}
			return Result{State: state, CommitIDs: commitIDs, Message: message}, err
		}
		if err := ensureCommitsAllowed(ctx, repoRoot, upstreamRef); err != nil {
			return Result{State: "pending", CommitIDs: commitIDs, Message: "integrated commits include paths outside the shared-config allowlist"}, err
		}
		if _, _, err := runGit(ctx, repoRoot, "push", remote, "HEAD:"+branchRef); err != nil {
			if isPushRace(err) && attempt < 2 {
				continue
			}
			return Result{State: "pending", CommitIDs: commitIDs, Message: "push failed; local shared commits remain available for retry"}, err
		}
		if head, _, headErr := runGit(ctx, repoRoot, "rev-parse", "HEAD"); headErr == nil {
			commitIDs = append(commitIDs, strings.TrimSpace(head))
		}
		return Result{State: "synced", CommitIDs: commitIDs, Message: "shared Codex configuration is synchronized"}, nil
	}
	return Result{State: "pending", CommitIDs: commitIDs, Message: "upstream changed during each push attempt; retry later"}, errors.New("push retry limit reached")
}

func ensureRepository(repoRoot string) error {
	if strings.TrimSpace(repoRoot) == "" {
		return errors.New("repository path is empty")
	}
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--show-toplevel")
	if err := cmd.Run(); err != nil {
		return errors.New("path is not a Git repository")
	}
	return nil
}

func configuredUpstream(ctx context.Context, repoRoot string) (remote, branch string, err error) {
	branchName, _, err := runGit(ctx, repoRoot, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", "", errors.New("detached HEAD cannot sync")
	}
	branchName = strings.TrimSpace(branchName)
	remote, _, err = runGit(ctx, repoRoot, "config", "--get", "branch."+branchName+".remote")
	if err != nil {
		return "", "", errors.New("branch has no configured upstream remote")
	}
	remote = strings.TrimSpace(remote)
	if remote == "" || remote == "." {
		return "", "", errors.New("branch upstream must name a remote")
	}
	mergeRef, _, err := runGit(ctx, repoRoot, "config", "--get", "branch."+branchName+".merge")
	if err != nil {
		return "", "", errors.New("branch has no configured upstream branch")
	}
	mergeRef = strings.TrimSpace(mergeRef)
	if !strings.HasPrefix(mergeRef, "refs/heads/") {
		return "", "", errors.New("configured upstream is not a remote branch")
	}
	branch = strings.TrimPrefix(mergeRef, "refs/heads/")
	return remote, branch, nil
}

func fetchUpstream(ctx context.Context, repoRoot, remote, branchRef, trackingRef string) error {
	refspec := "+" + branchRef + ":" + trackingRef
	if _, _, err := runGit(ctx, repoRoot, "fetch", "--no-tags", remote, refspec); err != nil {
		return err
	}
	if _, _, err := runGit(ctx, repoRoot, "rev-parse", "--verify", trackingRef); err != nil {
		return errors.New("configured upstream branch was not found")
	}
	return nil
}

func worktreePaths(ctx context.Context, repoRoot string) ([]string, error) {
	output, _, err := runGit(ctx, repoRoot, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	fields := strings.Split(output, "\x00")
	paths := make([]string, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if entry == "" {
			continue
		}
		if len(entry) < 4 || entry[2] != ' ' {
			return nil, errors.New("could not parse Git worktree status")
		}
		paths = append(paths, filepath.ToSlash(entry[3:]))
		if strings.ContainsAny(entry[:2], "RC") && i+1 < len(fields) {
			i++
			paths = append(paths, filepath.ToSlash(fields[i]))
		}
	}
	return paths, nil
}

func stagedPaths(ctx context.Context, repoRoot string) ([]string, error) {
	output, _, err := runGit(ctx, repoRoot, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil, err
	}
	return splitGitPaths(output), nil
}

func ensureAllowedPaths(paths []string) error {
	for _, repoPath := range paths {
		if !IsAllowedRepoPath(repoPath) {
			return fmt.Errorf("path outside shared-config allowlist: %q", repoPath)
		}
	}
	return nil
}

func ensureCommitsAllowed(ctx context.Context, repoRoot, upstreamRef string) error {
	output, _, err := runGit(ctx, repoRoot, "log", "--format=", "--name-only", "-z", upstreamRef+"..HEAD")
	if err != nil {
		return err
	}
	return ensureAllowedPaths(splitGitPaths(output))
}

func integrateUpstream(ctx context.Context, repoRoot, upstreamRef string) error {
	counts, _, err := runGit(ctx, repoRoot, "rev-list", "--left-right", "--count", upstreamRef+"...HEAD")
	if err != nil {
		return err
	}
	parts := strings.Fields(counts)
	if len(parts) != 2 {
		return errors.New("could not compare local branch with upstream")
	}
	upstreamOnly, err := strconv.Atoi(parts[0])
	if err != nil {
		return errors.New("could not read upstream commit count")
	}
	localOnly, err := strconv.Atoi(parts[1])
	if err != nil {
		return errors.New("could not read local commit count")
	}
	if upstreamOnly == 0 || (upstreamOnly > 0 && localOnly == 0) {
		if upstreamOnly > 0 {
			_, _, err = runGit(ctx, repoRoot, "merge", "--ff-only", upstreamRef)
		}
		return err
	}
	_, _, err = runGit(ctx, repoRoot, "rebase", upstreamRef)
	return err
}

func unresolvedConflictPaths(ctx context.Context, repoRoot string) ([]string, error) {
	output, _, err := runGit(ctx, repoRoot, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	return splitGitPaths(output), nil
}

func rebaseInProgress(ctx context.Context, repoRoot string) (bool, error) {
	for _, name := range []string{"rebase-merge", "rebase-apply", "MERGE_HEAD"} {
		path, _, err := runGit(ctx, repoRoot, "rev-parse", "--git-path", name)
		if err != nil {
			return false, err
		}
		gitPath := strings.TrimSpace(path)
		if !filepath.IsAbs(gitPath) {
			gitPath = filepath.Join(repoRoot, gitPath)
		}
		if _, err := os.Stat(gitPath); err == nil {
			return true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

func ensureCommitIdentity(ctx context.Context, repoRoot string) error {
	for _, key := range []string{"GIT_AUTHOR_IDENT", "GIT_COMMITTER_IDENT"} {
		if _, _, err := runGit(ctx, repoRoot, "var", key); err != nil {
			return errors.New("Git commit identity is not configured")
		}
	}
	return nil
}

func isPushRace(err error) bool {
	var commandErr *gitCommandError
	if !errors.As(err, &commandErr) {
		return false
	}
	stderr := strings.ToLower(commandErr.stderr)
	return strings.Contains(stderr, "non-fast-forward") || strings.Contains(stderr, "fetch first") || strings.Contains(stderr, "rejected")
}

type gitCommandError struct {
	command string
	stderr  string
	err     error
}

func (e *gitCommandError) Error() string { return "git " + e.command + " failed" }
func (e *gitCommandError) Unwrap() error { return e.err }

func runGit(ctx context.Context, repoRoot string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoRoot
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if runErr := cmd.Run(); runErr != nil {
		command := "command"
		if len(args) > 0 {
			command = args[0]
		}
		return out.String(), errOut.String(), &gitCommandError{command: command, stderr: errOut.String(), err: runErr}
	}
	return out.String(), errOut.String(), nil
}

func splitGitPaths(value string) []string {
	fields := strings.Split(value, "\x00")
	paths := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			paths = append(paths, filepath.ToSlash(field))
		}
	}
	return paths
}
