package kanban

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// 完成证据清单的可读形态（Plan 2026-11-20 Task 8）：界面用它回答「还差什么」。
// 它是只读投影：报告取自落库的 CompletionReport，复核结论原样透出并标明来源，
// 不重新求值 —— 清单要和当时被驳回的那一轮一致，不能在读的时候换一份事实。
type CompletionItemView struct {
	Index  int    `json:"index"`
	ID     string `json:"id"`
	Label  string `json:"label"`
	Kind   string `json:"kind"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}

type TaskCompletionView struct {
	Present         bool                 `json:"present"`
	Status          string               `json:"status"`
	Items           []CompletionItemView `json:"items"`
	Passed          bool                 `json:"passed"`
	RepairsUsed     int                  `json:"repairs_used"`
	RepairsMax      int                  `json:"repairs_max"`
	VerifierVerdict string               `json:"verifier_verdict"`
	UpdatedAt       string               `json:"updated_at"`
}

// TaskCompletionStatus 读任务当前的完成记录。任务不存在返回错误；没有记录时
// found=false（HTTP 层回 completion: null，与「有记录但一条都不通过」区分开）。
func (s *Service) TaskCompletionStatus(ctx context.Context, rootID, taskID string) (TaskCompletionView, bool, error) {
	store, err := s.taskStore(rootID)
	if err != nil {
		return TaskCompletionView{}, false, err
	}
	if _, err := store.GetTask(ctx, taskID); err != nil {
		return TaskCompletionView{}, false, err
	}
	row, found, err := store.GetTaskCompletion(ctx, taskID)
	if err != nil || !found {
		return TaskCompletionView{}, false, err
	}
	view := TaskCompletionView{
		Present:         true,
		Status:          row.Status,
		RepairsUsed:     row.RepairsUsed,
		RepairsMax:      defaultCompletionMaxRepairs,
		VerifierVerdict: row.VerifierVerdict,
		UpdatedAt:       row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	var report CompletionReport
	if json.Unmarshal([]byte(row.ReportJSON), &report) == nil {
		view.Passed = report.Passed
		for i, item := range report.Items {
			view.Items = append(view.Items, CompletionItemView{
				Index:  i,
				ID:     item.ID,
				Label:  item.Label,
				Kind:   item.Kind,
				Pass:   item.Pass,
				Detail: item.Detail,
			})
		}
	}
	// 预算上限跟着那一轮 Run 的契约走；Run 读不到时退回默认值，界面才不会显示 0/0。
	if runID := strings.TrimSpace(row.RunID); runID != "" {
		if run, err := store.WorkflowRunByID(ctx, runID); err == nil && run != nil {
			view.RepairsMax = completionRepairsMaxFor(run)
		}
	}
	return view, true, nil
}
