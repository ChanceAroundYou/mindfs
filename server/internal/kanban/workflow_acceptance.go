package kanban

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// 验收项（Plan 2026-11-21 W1 T3）。完成契约回答「这一轮该有哪些事实」；验收项
// 回答「Router 声称做完了哪些事，每一件的证据是什么」。它比契约晚一层：Router
// 在收尾前用 router_record_acceptance 登记，证据门按证据等级逐条核对。
//
// 三个等级刻意不并列：
//   - self_report 只是声称，永远不足以过门（它逼 Router 去拿真证据）；
//   - artifact 要求 evidence_ref 指向工作区里真实存在的东西；
//   - independent_verification 要求独立复核回合给出 pass。

const (
	acceptanceMaxItems  = 8
	acceptanceTextLimit = 300
	acceptanceRefLimit  = 400
)

type AcceptanceEvidenceClass string

const (
	AcceptanceSelfReport              AcceptanceEvidenceClass = "self_report"
	AcceptanceArtifact                AcceptanceEvidenceClass = "artifact"
	AcceptanceIndependentVerification AcceptanceEvidenceClass = "independent_verification"
)

const (
	AcceptanceStatusOpen = "open"
	AcceptanceStatusMet  = "met"
)

// AcceptanceItemInput 是 Router 登记时的原始输入（工具参数 / 动作载荷）。
type AcceptanceItemInput struct {
	NodeID        string                  `json:"node_id,omitempty"`
	Text          string                  `json:"text"`
	EvidenceClass AcceptanceEvidenceClass `json:"evidence_class"`
	EvidenceRef   string                  `json:"evidence_ref,omitempty"`
}

// AcceptanceItem 是落库的一行。Status / ReworkRound 是审计水位：证据门每次都
// 重新机检，不靠这两个字段做判定。
type AcceptanceItem struct {
	ID            string
	TaskID        string
	RunID         string
	NodeID        string
	Seq           int
	Text          string
	EvidenceClass AcceptanceEvidenceClass
	Status        string
	EvidenceRef   string
	ReworkRound   int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// normalizeAcceptanceItems 校验并裁剪一轮登记的验收项。空文本、未知等级、
// artifact 缺产物路径一律拒绝 —— 这些是 Router 自己能看出来的错误，不该等
// 到收尾时才发现。
func normalizeAcceptanceItems(items []AcceptanceItemInput) ([]AcceptanceItemInput, error) {
	if len(items) == 0 {
		return nil, errors.New("at least one acceptance item required")
	}
	if len(items) > acceptanceMaxItems {
		return nil, fmt.Errorf("too many acceptance items: %d > %d", len(items), acceptanceMaxItems)
	}
	out := make([]AcceptanceItemInput, 0, len(items))
	for index, item := range items {
		text := truncateRouterText(strings.TrimSpace(item.Text), acceptanceTextLimit)
		if text == "" {
			return nil, fmt.Errorf("acceptance item %d: text required", index+1)
		}
		class := AcceptanceEvidenceClass(strings.ToLower(strings.TrimSpace(string(item.EvidenceClass))))
		switch class {
		case AcceptanceSelfReport, AcceptanceArtifact, AcceptanceIndependentVerification:
		default:
			return nil, fmt.Errorf("acceptance item %d: unknown evidence_class %q", index+1, item.EvidenceClass)
		}
		ref := truncateRouterText(strings.TrimSpace(item.EvidenceRef), acceptanceRefLimit)
		if class == AcceptanceArtifact && ref == "" {
			return nil, fmt.Errorf("acceptance item %d: artifact requires evidence_ref (a workspace path)", index+1)
		}
		if class == AcceptanceSelfReport {
			// 自述不需要产物路径；带了也不当证据，直接清掉免得误导读者。
			ref = ""
		}
		out = append(out, AcceptanceItemInput{
			NodeID:        strings.TrimSpace(item.NodeID),
			Text:          text,
			EvidenceClass: class,
			EvidenceRef:   ref,
		})
	}
	return out, nil
}

// acceptanceItemID 给验收项一个确定 ID：同一轮同一序号重登记得到同一个 ID，
// 收尾报告与补做事件里的编号才不会漂。
func acceptanceItemID(taskID, runID string, seq int) string {
	scope := taskID + "\x00" + runID
	sum := sha256.Sum256([]byte(scope))
	return fmt.Sprintf("acc-%s-%d", hex.EncodeToString(sum[:])[:6], seq)
}

func acceptanceItemLabel(item AcceptanceItem) string {
	return fmt.Sprintf("验收项 %d：%s", item.Seq, item.Text)
}

// evaluateAcceptanceItems 把登记的验收项翻成机检项。返回的 bool 表示「有项在
// 等独立复核结论」—— 复核分支据此启动，哪怕契约本身已经对上。
func evaluateAcceptanceItems(ctx context.Context, s *Service, store *TaskStore, task Task, run *WorkflowRun, rows []NodeRun, rowsErr error) ([]CompletionItemStatus, bool) {
	runID := ""
	if run != nil {
		runID = run.ID
	}
	items, err := store.ListAcceptanceItems(ctx, task.ID, runID)
	if err != nil {
		return []CompletionItemStatus{{
			ID: "acceptance", Label: "验收项", Kind: "acceptance",
			Detail: "读取验收项失败：" + err.Error(),
		}}, false
	}
	if len(items) == 0 {
		return nil, false
	}
	out := make([]CompletionItemStatus, 0, len(items))
	needsVerification := false
	for _, item := range items {
		status := CompletionItemStatus{
			ID:            item.ID,
			Label:         acceptanceItemLabel(item),
			Kind:          "acceptance_" + string(item.EvidenceClass),
			NodeID:        item.NodeID,
			Source:        "acceptance",
			EvidenceClass: string(item.EvidenceClass),
		}
		switch item.EvidenceClass {
		case AcceptanceSelfReport:
			status.Detail = "只是自述，不算证据：改用一个产物路径（artifact），或交给独立复核（independent_verification）"
		case AcceptanceArtifact:
			evaluateAcceptanceArtifact(ctx, s, store, task, rows, rowsErr, item, &status)
		case AcceptanceIndependentVerification:
			needsVerification = true
			status.Detail = "等待独立复核结论"
		default:
			status.Detail = fmt.Sprintf("未知证据等级 %q", item.EvidenceClass)
		}
		out = append(out, status)
	}
	return out, needsVerification
}

// evaluateAcceptanceArtifact 的判据与契约里的 file_exists 完全一致（同一套
// 工作区基准解析与符号链接越界守卫），产物路径按节点工作树优先。
func evaluateAcceptanceArtifact(ctx context.Context, s *Service, store *TaskStore, task Task, rows []NodeRun, rowsErr error, item AcceptanceItem, status *CompletionItemStatus) {
	root, source, err := completionWorkspaceRoot(ctx, s, store, task, rows, rowsErr, item.NodeID)
	if err != nil {
		status.Detail = err.Error()
		return
	}
	full, err := completionResolvePath(root, item.EvidenceRef)
	if err != nil {
		status.Detail = err.Error()
		return
	}
	if err := completionGuardSymlinkEscape(root, full, item.EvidenceRef); err != nil {
		status.Detail = err.Error()
		return
	}
	info, err := os.Lstat(full)
	if os.IsNotExist(err) {
		status.Detail = fmt.Sprintf("产物不存在：%s（在 %s）", item.EvidenceRef, source)
		return
	}
	if err != nil {
		status.Detail = fmt.Sprintf("无法检查 %s：%v", item.EvidenceRef, err)
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		status.Detail = fmt.Sprintf("%s 是符号链接，不跟随链接判断是否存在", item.EvidenceRef)
		return
	}
	status.Pass = true
	status.Detail = fmt.Sprintf("存在：%s（在 %s）", item.EvidenceRef, source)
}

// appendAcceptanceItems 把验收项挂到报告上并重算 Passed。没有登记验收项时原样
// 返回 —— 存量任务不受影响（「每轮至少一条」靠提示词约束，不做硬门）。
func appendAcceptanceItems(report CompletionReport, items []CompletionItemStatus, needsVerification bool) CompletionReport {
	if len(items) == 0 {
		return report
	}
	report.Items = append(report.Items, items...)
	report.AcceptanceNeedsVerification = needsVerification
	report.Passed = true
	for _, item := range report.Items {
		if !item.Pass {
			report.Passed = false
		}
	}
	return report
}

// markAcceptanceVerified 在独立复核给出 pass 之后把 waiting 的验收项改成通过。
// evidence_class=independent_verification 的语义就是「以复核结论为准」。
func markAcceptanceVerified(report CompletionReport) CompletionReport {
	if !report.AcceptanceNeedsVerification {
		return report
	}
	for i := range report.Items {
		item := &report.Items[i]
		if item.Source != "acceptance" || item.EvidenceClass != string(AcceptanceIndependentVerification) {
			continue
		}
		item.Pass = true
		item.Detail = "独立复核通过"
	}
	report.AcceptanceNeedsVerification = false
	report.Passed = true
	for _, item := range report.Items {
		if !item.Pass {
			report.Passed = false
		}
	}
	return report
}
