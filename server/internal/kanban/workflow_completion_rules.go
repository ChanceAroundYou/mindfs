package kanban

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// 完成契约的逐条求值规则。每条规则只回答一个可以重算的问题，并且只取每个
// 节点**最新一次 attempt** —— 历史 attempt 是证据，不是现状；用「曾经成功过」
// 判定收尾会让重试失败的节点蒙混过关。
func evaluateCompletionRequirement(ctx context.Context, s *Service, store *TaskStore, task Task, run *WorkflowRun, rows []NodeRun, rowsErr error, req CompletionRequirement) CompletionItemStatus {
	item := CompletionItemStatus{ID: req.ID, Label: req.Label, Kind: req.Kind, NodeID: strings.TrimSpace(req.NodeID)}
	if item.Label == "" {
		item.Label = req.ID
	}
	switch req.Kind {
	case CompletionKindNodeDone:
		evaluateNodeDone(&item, rows, rowsErr, req)
	case CompletionKindGateApproved:
		evaluateGateApproved(&item, rows, rowsErr, req)
	case CompletionKindAllNodesTerminal:
		evaluateAllNodesTerminal(&item, run, rows, rowsErr)
	case CompletionKindFileExists:
		evaluateFileExists(ctx, &item, s, store, task, req, rows, rowsErr)
	default:
		item.Detail = "未知的证据类型：" + strings.TrimSpace(req.Kind)
	}
	return item
}

func evaluateNodeDone(item *CompletionItemStatus, rows []NodeRun, rowsErr error, req CompletionRequirement) {
	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		item.Detail = "证据没有指定节点"
		return
	}
	if rowsErr != nil {
		item.Detail = "读取节点运行失败：" + rowsErr.Error()
		return
	}
	latest, ok := latestTopLevelNodeRun(rows, nodeID)
	if !ok {
		item.Detail = fmt.Sprintf("节点 %s 还没有任何一次尝试", nodeID)
		return
	}
	if latest.Status != NodeRunSuccess {
		item.Detail = fmt.Sprintf("节点 %s 最新一次尝试是 %s，不是成功", nodeID, latest.Status)
		return
	}
	outcome, _ := latest.Output["outcome"].(string)
	if strings.TrimSpace(outcome) != "done" {
		item.Detail = fmt.Sprintf("节点 %s 的结论不是 done（%s）", nodeID, strings.TrimSpace(outcome))
		return
	}
	// 结果转换器读不懂模型输出时会兜底出一个 done。那是「没能核对」，不是证据。
	if unverified, _ := latest.Output["unverified"].(bool); unverified {
		item.Detail = fmt.Sprintf("节点 %s 的结论是转换器兜底的 done（没有核对过实际产出）", nodeID)
		return
	}
	var missing []string
	for _, key := range req.Keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if !completionOutputHasValue(latest.Output, key) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		item.Detail = fmt.Sprintf("节点 %s 的输出缺少：%s", nodeID, strings.Join(missing, "、"))
		return
	}
	item.Pass = true
	item.Detail = fmt.Sprintf("节点 %s 最新一次尝试成功，输出齐全", nodeID)
}

func evaluateGateApproved(item *CompletionItemStatus, rows []NodeRun, rowsErr error, req CompletionRequirement) {
	nodeID := strings.TrimSpace(req.NodeID)
	if nodeID == "" {
		item.Detail = "证据没有指定审批节点"
		return
	}
	if rowsErr != nil {
		item.Detail = "读取节点运行失败：" + rowsErr.Error()
		return
	}
	latest, ok := latestTopLevelNodeRun(rows, nodeID)
	if !ok {
		item.Detail = fmt.Sprintf("审批节点 %s 还没有任何一次尝试", nodeID)
		return
	}
	if latest.Status != NodeRunSuccess {
		item.Detail = fmt.Sprintf("审批节点 %s 的状态是 %s，不是已批准", nodeID, latest.Status)
		return
	}
	want := strings.TrimSpace(req.Decision)
	if want == "" {
		want = "approve"
	}
	got, _ := latest.Output["decision"].(string)
	if strings.TrimSpace(got) != want {
		item.Detail = fmt.Sprintf("审批节点 %s 的决定是 %q，期望 %q", nodeID, strings.TrimSpace(got), want)
		return
	}
	item.Pass = true
	item.Detail = fmt.Sprintf("审批节点 %s 已给出 %s", nodeID, want)
}

func evaluateAllNodesTerminal(item *CompletionItemStatus, run *WorkflowRun, rows []NodeRun, rowsErr error) {
	if rowsErr != nil {
		item.Detail = "读取节点运行失败：" + rowsErr.Error()
		return
	}
	if run == nil {
		item.Detail = "没有工作流运行"
		return
	}
	if workflowRunHasOpenNodes(run, rows) {
		item.Detail = "还有节点没有收尾"
		return
	}
	var pending []string
	for _, node := range run.Graph.Nodes {
		if completionNodeSettled(run, rows, node.ID) {
			continue
		}
		pending = append(pending, node.ID)
	}
	if len(pending) > 0 {
		item.Detail = "这些节点还没有结论：" + strings.Join(pending, "、")
		return
	}
	item.Pass = true
	item.Detail = "全部节点都已收尾"
}

// completionNodeSettled 先看投影，投影落后时用该节点最新一行兜底（只认
// success/skipped —— cancelled/failed 是被中止的证据，不是完成）。
func completionNodeSettled(run *WorkflowRun, rows []NodeRun, nodeID string) bool {
	switch run.NodeStates[nodeID] {
	case NodeRunSuccess, NodeRunSkipped:
		return true
	}
	latest, ok := latestTopLevelNodeRun(rows, nodeID)
	if !ok {
		return false
	}
	return latest.Status == NodeRunSuccess || latest.Status == NodeRunSkipped
}

func evaluateFileExists(ctx context.Context, item *CompletionItemStatus, s *Service, store *TaskStore, task Task, req CompletionRequirement, rows []NodeRun, rowsErr error) {
	root, source, err := completionWorkspaceRoot(ctx, s, store, task, rows, rowsErr, req.NodeID)
	if err != nil {
		item.Detail = err.Error()
		return
	}
	full, err := completionResolvePath(root, req.Path)
	if err != nil {
		item.Detail = err.Error()
		return
	}
	if err := completionGuardSymlinkEscape(root, full, req.Path); err != nil {
		item.Detail = err.Error()
		return
	}
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		item.Detail = fmt.Sprintf("文件不存在：%s（在 %s）", strings.TrimSpace(req.Path), source)
		return
	}
	if err != nil {
		item.Detail = fmt.Sprintf("无法检查 %s：%v", strings.TrimSpace(req.Path), err)
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		item.Detail = fmt.Sprintf("%s 是符号链接，不跟随链接判断是否存在", strings.TrimSpace(req.Path))
		return
	}
	item.Pass = true
	item.Detail = fmt.Sprintf("存在：%s（在 %s）", strings.TrimSpace(req.Path), source)
}

// completionWorkspaceRoot 按「节点最新 attempt 的独立工作树 → 任务工作树 →
// 项目根」解析相对路径的基准。前两级都为空时退回项目根，保证脚本类节点也能
// 核对项目内的产物。
func completionWorkspaceRoot(ctx context.Context, s *Service, store *TaskStore, task Task, rows []NodeRun, rowsErr error, nodeID string) (string, string, error) {
	if nodeID = strings.TrimSpace(nodeID); nodeID != "" && rowsErr == nil {
		if latest, ok := latestTopLevelNodeRun(rows, nodeID); ok {
			if path := strings.TrimSpace(latest.WorktreePath); path != "" {
				if info, err := os.Lstat(path); err == nil && info.IsDir() {
					return path, "节点工作树", nil
				}
			}
		}
	}
	if path := strings.TrimSpace(task.WorktreePath); path != "" && !task.WorktreeMissing {
		if info, err := os.Lstat(path); err == nil && info.IsDir() {
			return path, "任务工作树", nil
		}
	}
	if s == nil || s.Roots == nil {
		return "", "", errors.New("无法解析项目根：服务没有根目录提供者")
	}
	root, err := s.Roots.GetRoot(task.RootID)
	if err != nil {
		return "", "", fmt.Errorf("无法解析项目根：%v", err)
	}
	if strings.TrimSpace(root.RootPath) == "" {
		return "", "", errors.New("无法解析项目根：路径为空")
	}
	_ = ctx
	_ = store
	return root.RootPath, "项目根", nil
}

// completionResolvePath 把契约里的相对路径限制在工作区内：不接受绝对路径、
// 不接受 `..` 越界。只做字符串归一，最终是否真的在根内由这里的判断决定，
// 绝不依赖调用方传参的整洁程度。
func completionResolvePath(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errors.New("证据没有指定路径")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("不接受绝对路径：%s", rel)
	}
	clean := filepath.Clean(rel)
	sep := string(filepath.Separator)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+sep) {
		return "", fmt.Errorf("路径越出工作区：%s", rel)
	}
	root = filepath.Clean(root)
	full := filepath.Join(root, clean)
	if full != root && !strings.HasPrefix(full, root+sep) {
		return "", fmt.Errorf("路径越出工作区：%s", rel)
	}
	return full, nil
}

// completionGuardSymlinkEscape 逐段解析路径上的符号链接。父目录是链接时，
// Lstat 只看最后一段会把整条越界路径当成存在 —— 这里按真实路径重新判一次。
func completionGuardSymlinkEscape(root, full, rel string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = filepath.Clean(root)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(full))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // 目录都不存在，Lstat 会报「文件不存在」
		}
		return fmt.Errorf("无法解析 %s 的上级目录：%v", strings.TrimSpace(rel), err)
	}
	realRoot = filepath.Clean(realRoot)
	if parent != realRoot && !strings.HasPrefix(parent, realRoot+string(filepath.Separator)) {
		return fmt.Errorf("%s 的上级目录链接到工作区之外，不作为证据", strings.TrimSpace(rel))
	}
	return nil
}

// latestTopLevelNodeRun 取某个顶层节点最新 attempt 的行。Loop 内部节点的行
// 带 LoopPath，不属于同名顶层节点的证据。
func latestTopLevelNodeRun(rows []NodeRun, nodeID string) (NodeRun, bool) {
	var latest NodeRun
	found := false
	for _, row := range rows {
		if row.NodeID != nodeID || strings.TrimSpace(row.LoopPath) != "" {
			continue
		}
		if !found || row.Attempt > latest.Attempt {
			latest = row
			found = true
		}
	}
	return latest, found
}

func completionOutputHasValue(output map[string]any, key string) bool {
	value, ok := output[key]
	if !ok || value == nil {
		return false
	}
	if text, isText := value.(string); isText {
		return strings.TrimSpace(text) != ""
	}
	return true
}
