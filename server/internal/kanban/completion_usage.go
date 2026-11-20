package kanban

import (
	"context"
	"time"
)

// 回合用量差分（Plan 2026-11-20 Task 13 / B4）。
//
// 数字从 live-compact 的折叠账本来：每 fold 一次写一行，行内带该请求真实的上游
// 用量计数器。它们本身就是**增量**（一次请求一行），所以在 Run 时间窗内求和就是
// 这一轮的花费，不需要另落基线 —— 补一份基线反而会和行内数字重复计。
//
// 账本只覆盖「发生过折叠的请求」。一个从头到尾没折过的大会话在账本里是空的，
// 这时答案是「不知道」，不是「没花钱」：`completionUsage` 返回 ok=false，接口给
// null。把缺失写成 0 会让「这轮几乎没花钱」这种假象出现在最不该出现的地方。

// CompletionUsage 是一轮 Run 内、该 Run 绑定会话花掉的用量。
type CompletionUsage struct {
	Input   int `json:"input"`
	Output  int `json:"output"`
	Cached  int `json:"cached"`
	Samples int `json:"samples"`
}

// FoldUsageSample 是账本里一行的用量读数，只保留求和需要的三个计数器。
type FoldUsageSample struct {
	At     time.Time
	Input  int
	Output int
	Cached int
	// Missing 对应账本行的 usage_source=missing：这一轮没有上游用量样本，
	// 三个计数器不是 0 而是「没有」。求和时跳过，也不计入 Samples。
	Missing bool
}

// FoldUsageReader 读某个会话的折叠账本读数。由 api 层装配（它才知道 harness
// 路由与账本目录）；nil 时用量一律视为「读不到」。
type FoldUsageReader interface {
	SessionFoldUsage(ctx context.Context, rootID, sessionKey string) ([]FoldUsageSample, error)
}

// SetFoldUsageReader 在装配时注入账本读口。与其它端口一致：nil 表示「没有这个
// 能力」，不是「用量为 0」。
func (s *Service) SetFoldUsageReader(reader FoldUsageReader) {
	if s == nil {
		return
	}
	s.foldUsage = reader
}

// completionUsage 汇总这一轮 Run 的用量。run 为空、没有绑定会话、账本读不出来、
// 窗口内一条真实读数都没有 —— 全部返回 ok=false。
func (s *Service) completionUsage(ctx context.Context, task Task, run *WorkflowRun) (CompletionUsage, bool) {
	if s == nil || s.foldUsage == nil || run == nil {
		return CompletionUsage{}, false
	}
	store, err := s.taskStore(task.RootID)
	if err != nil {
		return CompletionUsage{}, false
	}
	keys, err := store.RunSessionKeys(ctx, run.ID)
	if err != nil || len(keys) == 0 {
		return CompletionUsage{}, false
	}

	start := run.CreatedAt
	if start.IsZero() {
		start = run.UpdatedAt
	}
	end := time.Now()
	// Run 已收尾时右界取它最后一次写入：之后不会有这一轮的行，用 now 只是把
	// 无关的后续回合算进来。Run 还开着时恰恰相反 —— 投影停在最后一次节点写入上，
	// 用 UpdatedAt 会把「这一轮刚开始花的钱」整段裁掉。
	if rows, err := store.ListWorkflowNodeRuns(ctx, run.ID); err == nil && !workflowRunHasOpenNodes(run, rows) {
		if !run.UpdatedAt.IsZero() {
			end = run.UpdatedAt
		}
	}

	var usage CompletionUsage
	for _, key := range keys {
		samples, err := s.foldUsage.SessionFoldUsage(ctx, task.RootID, key)
		if err != nil {
			// 一个会话读不出来就整体放弃：半份数字比没有数字更糟。
			return CompletionUsage{}, false
		}
		for _, sample := range samples {
			if sample.Missing {
				continue
			}
			if !start.IsZero() && sample.At.Before(start) {
				continue
			}
			if sample.At.After(end) {
				continue
			}
			usage.Input += sample.Input
			usage.Output += sample.Output
			usage.Cached += sample.Cached
			usage.Samples++
		}
	}
	if usage.Samples == 0 {
		return CompletionUsage{}, false
	}
	return usage, true
}
