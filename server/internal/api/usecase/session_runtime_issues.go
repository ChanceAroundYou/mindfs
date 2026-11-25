package usecase

import (
	"context"
	"fmt"
	"os"
	"strings"

	"mindfs/server/internal/session"
)

// 巡检「会话还钉在已删除的工作目录上」—— skill 的 verify 等价物。
//
// 判据唯一来源是 session.RuntimeRootPath，与 repoint 共用：两边各写一份时会出现
// 「巡检说目录没了、repoint 说还在原地」的死循环。会话正在回复时一律跳过，
// repoint 第一件事是 pool.Close，此时动手会腰斩半句话。

type RuntimeIssue struct {
	SessionKey string `json:"session_key"`
	Agent      string `json:"agent"`
	Path       string `json:"path"`
	Class      string `json:"class"`
	RootID     string `json:"root_id"`
}

type RuntimeIssueReport struct {
	Scanned int            `json:"scanned"`
	Issues  []RuntimeIssue `json:"issues"`
}

type RuntimeIssueSweepReport struct {
	Scanned  int      `json:"scanned"`
	Repaired int      `json:"repaired"`
	Skipped  int      `json:"skipped"`
	Failed   int      `json:"failed"`
	Details  []string `json:"details"`
}

func (s *Service) listSessionsForSweep(ctx context.Context, rootID string) ([]*session.Session, error) {
	manager, err := s.Registry.GetSessionManager(rootID)
	if err != nil {
		return nil, err
	}
	active, err := manager.List(ctx, session.ListOptions{})
	if err != nil {
		return nil, err
	}
	archived, err := manager.List(ctx, session.ListOptions{ArchivedMode: "only"})
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(active)+len(archived))
	out := make([]*session.Session, 0, len(active)+len(archived))
	for _, item := range append(active, archived...) {
		if item == nil {
			continue
		}
		if _, dup := seen[item.Key]; dup {
			continue
		}
		seen[item.Key] = struct{}{}
		out = append(out, item)
	}
	return out, nil
}

func (s *Service) DiagnoseRuntimeIssues(ctx context.Context, rootID string) (RuntimeIssueReport, error) {
	out := RuntimeIssueReport{Issues: []RuntimeIssue{}}
	sessions, err := s.listSessionsForSweep(ctx, rootID)
	if err != nil {
		return out, err
	}
	out.Scanned = len(sessions)
	for _, item := range sessions {
		path := session.RuntimeRootPath(item)
		if path == "" {
			continue
		}
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			continue
		}
		class := "workdir-missing"
		if item.RelatedWorktree != nil && strings.TrimSpace(item.RelatedWorktree.Path) == path {
			class = "worktree-missing"
		}
		out.Issues = append(out.Issues, RuntimeIssue{
			SessionKey: item.Key, Agent: session.InferAgentFromSession(item), Path: path, Class: class, RootID: rootID,
		})
	}
	return out, nil
}

// SweepRuntimeIssues 修复巡检发现的问题。
func (s *Service) SweepRuntimeIssues(ctx context.Context, rootID string) (RuntimeIssueSweepReport, error) {
	out := RuntimeIssueSweepReport{Details: []string{}}
	report, err := s.DiagnoseRuntimeIssues(ctx, rootID)
	if err != nil {
		return out, err
	}
	out.Scanned = report.Scanned
	pool := s.Registry.GetAgentPool()
	for _, issue := range report.Issues {
		if pool != nil && sessionMaintenanceBusy(pool, issue.Agent, issue.SessionKey) {
			out.Skipped++
			out.Details = append(out.Details, fmt.Sprintf("skip %s: running", issue.SessionKey))
			continue
		}
		if _, err := s.RepointSession(ctx, RepointSessionInput{RootID: rootID, Key: issue.SessionKey}); err != nil {
			out.Failed++
			out.Details = append(out.Details, fmt.Sprintf("fail %s: %v", issue.SessionKey, err))
			continue
		}
		out.Repaired++
		out.Details = append(out.Details, fmt.Sprintf("repoint %s: %s", issue.SessionKey, issue.Path))
	}
	return out, nil
}
