package kanban

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 完成契约（Plan 2026-11-20 Task 3，设计见
// docs/superpowers/specs/2026-11-20-evidence-gated-autonomous-completion-design.md §6）。
//
// 它回答一个问题：这一轮任务凭什么算做完了？答案不是「节点都跑完了」，而是
// 一组可以逐条核对的证据。求值器只读任务库、NodeRun 行和磁盘，不读转录 —— 证据
// 必须能重算，不能靠叙述。
const (
	CompletionKindNodeDone         = "node_done"
	CompletionKindGateApproved     = "gate_approved"
	CompletionKindAllNodesTerminal = "all_nodes_terminal"
	CompletionKindFileExists       = "file_exists"
)

// defaultCompletionMaxRepairs 是契约没写 MaxRepairs 时的自动补做轮数。
const defaultCompletionMaxRepairs = 2

type CompletionContract struct {
	Requirements []CompletionRequirement `json:"requirements,omitempty"`
	// VerifyWhenUnverifiable 表示契约里有求值不了的项时，允许退到独立复核回合
	// （默认 true）。nil 与 true 等价，指针只为区分「显式关掉」。
	VerifyWhenUnverifiable *bool `json:"verify_when_unverifiable,omitempty"`
	MaxRepairs             int   `json:"max_repairs,omitempty"` // 默认 2，夹 [0,3]
}

type CompletionRequirement struct {
	ID       string   `json:"id"` // 稳定短 id，进系统行与事件 payload
	Label    string   `json:"label,omitempty"`
	Kind     string   `json:"kind"`
	NodeID   string   `json:"node_id,omitempty"`
	Keys     []string `json:"keys,omitempty"`     // node_done：output 必须有的键
	Path     string   `json:"path,omitempty"`     // file_exists：工作区内的相对路径
	Decision string   `json:"decision,omitempty"` // gate_approved：期望的决定，默认 approve
}

type CompletionItemStatus struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Kind  string `json:"kind,omitempty"`
	// NodeID 是这条证据归属的节点（只有 node_done / gate_approved 有）。补做
	// 要拿它去定位最新 attempt，所以必须进落库的报告。
	NodeID string `json:"node_id,omitempty"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

type CompletionReport struct {
	RunID    string                 `json:"run_id,omitempty"`
	Contract bool                   `json:"contract"`
	Items    []CompletionItemStatus `json:"items"`
	Passed   bool                   `json:"passed"`
	Hash     string                 `json:"hash,omitempty"`
}

// completionMaxRepairs 把契约里的自动补做轮数归一化：没写按 2，写了夹到 [0,3]。
func completionMaxRepairs(c *CompletionContract) int {
	if c == nil || c.MaxRepairs == 0 {
		return defaultCompletionMaxRepairs
	}
	if c.MaxRepairs < 0 {
		return 0
	}
	if c.MaxRepairs > 3 {
		return 3
	}
	return c.MaxRepairs
}

// completionContractHash 给契约一个稳定的身份，用于运行期冻结守卫与事件去重。
// nil（无契约）与「有空契约」必须区分开，所以 nil 返回空串。
func completionContractHash(c *CompletionContract) string {
	if c == nil {
		return ""
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "invalid"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// completionContractOfGraphJSON 从库里存的 graph_json 取回契约，供保存路径的
// 冻结守卫比较。读不懂的旧图按「无契约」处理 —— 那种行本来也存不下契约。
func completionContractOfGraphJSON(raw string) *CompletionContract {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var graph WorkflowDefinition
	if err := json.Unmarshal([]byte(raw), &graph); err != nil {
		return nil
	}
	return graph.Completion
}

// EvaluateCompletion 逐条核对完成契约。run 为 nil 时按直连任务求值。
//
// 读库失败之类的求值错误一律落到对应 item 的 Pass=false + Detail：收尾判定
// 必须 fail-closed，而且要把「为什么判不了」写给用户看，不能静默通过。
func (s *Service) EvaluateCompletion(ctx context.Context, store *TaskStore, task Task, run *WorkflowRun) (CompletionReport, error) {
	if store == nil {
		return CompletionReport{}, errors.New("task store required")
	}
	if run == nil {
		return evaluateDirectCompletion(ctx, store, task)
	}
	contract := run.Graph.Completion
	report := CompletionReport{RunID: run.ID, Hash: completionContractHash(contract)}
	if contract == nil || len(contract.Requirements) == 0 {
		report.Items = []CompletionItemStatus{{
			ID:    "contract",
			Label: "工作流未声明完成证据",
			Kind:  "contract",
			Pass:  false,
			Detail: "这个工作流没有声明完成契约，没有证据可以核对；" +
				"请先在工作流里列出「做到什么算做完」",
		}}
		return report, nil
	}
	rows, rowsErr := store.ListWorkflowNodeRuns(ctx, run.ID)
	report.Contract = true
	passed := true
	report.Items = make([]CompletionItemStatus, 0, len(contract.Requirements))
	for _, req := range contract.Requirements {
		item := evaluateCompletionRequirement(ctx, s, store, task, run, rows, rowsErr, req)
		if !item.Pass {
			passed = false
		}
		report.Items = append(report.Items, item)
	}
	report.Passed = passed
	return report, nil
}

// evaluateDirectCompletion 是直连任务（没有 Run）的机检项：三条固定规则，
// 不读契约。它们只回答「对话是否收干净了」，因为直连回合没有可声明的产出证据。
func evaluateDirectCompletion(ctx context.Context, store *TaskStore, task Task) (CompletionReport, error) {
	if store == nil {
		return CompletionReport{}, errors.New("task store required")
	}
	report := CompletionReport{}
	items := make([]CompletionItemStatus, 0, 3)

	settled := CompletionItemStatus{ID: "reply_settled", Label: "最新留言已有回复", Kind: "reply_settled"}
	acts := NewActivityStore(store.db)
	list, err := acts.List(ctx, task.ID)
	switch {
	case err != nil:
		settled.Detail = "读取对话失败：" + err.Error()
	default:
		var latestReply *TaskActivity
		latestUserIdx, latestReplyIdx := -1, -1
		for i, a := range list {
			switch a.Type {
			case ActivityTypeUserMessage:
				latestUserIdx = i
			case ActivityTypeAgentReply:
				latestReply = a
				latestReplyIdx = i
			}
		}
		switch {
		case latestUserIdx < 0:
			settled.Detail = "任务还没有用户留言"
		case latestReply == nil:
			settled.Detail = "还没有任何回复"
		case latestReplyIdx < latestUserIdx:
			settled.Detail = "最新一条留言还没有回复"
		case latestReply.Status != ActivityStatusCompleted:
			settled.Detail = fmt.Sprintf("最新一条回复还是 %s，没有收尾", strings.TrimSpace(latestReply.Status))
		// 老数据没有 reply_to_id，只能按顺序判：「最新的回复」必须晚于「最新的留言」。
		case latestReply.ReplyToID != "" && latestReply.ReplyToID != list[latestUserIdx].ID:
			settled.Detail = "最新一条回复答复的是更早的留言，最新留言还没有人接"
		default:
			settled.Pass = true
			settled.Detail = "最新一条回复已收尾"
		}
	}
	items = append(items, settled)

	live := CompletionItemStatus{ID: "no_live_reply", Label: "没有正在生成的回复", Kind: "no_live_reply"}
	if alive, err := acts.LiveReplyExists(ctx, task.ID); err != nil {
		live.Detail = "查询回复状态失败：" + err.Error()
	} else if alive {
		live.Detail = "还有回复在生成（pending/running）"
	} else {
		live.Pass = true
		live.Detail = "没有正在生成的回复"
	}
	items = append(items, live)

	openRun := CompletionItemStatus{ID: "no_open_run", Label: "没有还在跑的工作流", Kind: "no_open_run"}
	run, err := store.WorkflowRunForTask(ctx, task.ID)
	switch {
	case errors.Is(err, ErrWorkflowRunNotFound):
		openRun.Pass = true
		openRun.Detail = "任务没有工作流运行"
	case err != nil:
		openRun.Detail = "读取工作流运行失败：" + err.Error()
	default:
		rows, rowsErr := store.ListWorkflowNodeRuns(ctx, run.ID)
		switch {
		case rowsErr != nil:
			openRun.Detail = "读取节点运行失败：" + rowsErr.Error()
		case workflowRunHasOpenNodes(run, rows):
			openRun.Detail = "还有工作流节点没有收尾"
		default:
			openRun.Pass = true
			openRun.Detail = "工作流运行已收尾"
		}
	}
	items = append(items, openRun)

	report.Items = items
	report.Passed = true
	for _, item := range items {
		if !item.Pass {
			report.Passed = false
		}
	}
	return report, nil
}
