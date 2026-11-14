package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"mindfs/server/internal/kanban"

	"github.com/go-chi/chi/v5"
)

// handleKanbanTaskBeginFinish 收尾按钮的唯一入口：按**真实状态**决定这次点击该做什么。
//
// 三个分支，判据全是「此刻的客观事实」，与任务处于哪个列、什么 status 无关：
//
//	① 会话正在回复        → 什么都不做，回 session_running（收尾本来就要等它停，不是错误）
//	② 分支已合进主干      → 直接机械清场（agent 那半已经做完，再跑一遍只会多一个空提交）
//	③ 其余                → 起一轮收尾回合，让 agent 自己 commit + merge；成功后服务端清场
//
// 任务没有阶段，所以也没有「重跑收尾段 / 追加收尾段」这两条路：③ 就是普通对话的一轮，
// 收尾状态由 Service.Finishing 的内存标记表达，不落任何阶段数据。
func (h *HTTPHandler) handleKanbanTaskBeginFinish(w http.ResponseWriter, r *http.Request) {
	svc, ok := h.kanbanService(w)
	if !ok {
		return
	}
	var req struct {
		RootID string `json:"root_id"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req)
	rootID := strings.TrimSpace(req.RootID)
	taskID := strings.TrimSpace(chi.URLParam(r, "id"))
	if rootID == "" || taskID == "" {
		respondError(w, http.StatusBadRequest, errInvalidRequest("root_id and id required"))
		return
	}
	ctx := r.Context()

	// ① 执行体在动。收尾要拆掉它的 cwd，必须等它停 —— 但这是正常等待，
	// 不是失败，所以回 200 而不是 409：前端据此只提示一句，不渲染成红色报错。
	if svc.TaskBusy(ctx, rootID, taskID) {
		respondJSON(w, http.StatusOK, map[string]any{"action": "session_running"})
		return
	}

	// ② 机械清场能直接做完 → 当场做掉。判定失败（读不到 git 状态等）不当成错误：
	// 退给 agent 那条路，让人去看一眼。
	plan, planErr := svc.PlanFinishWorktree(ctx, rootID, taskID)
	if planErr != nil {
		plan = kanban.FinishPlan{Mechanical: false, Reason: "收尾判定失败：" + planErr.Error()}
	}
	teardownTried := false
	var teardownReport FinishTeardownReport
	if plan.Mechanical {
		teardownTried = true
		done, report := h.teardownFinishWorktree(w, rootID, taskID, plan)
		if done {
			return
		}
		// ②.5 没做成 → 换一条路，不报错。机械清场是加速，不是门槛。
		teardownReport = report
		plan = agentFallbackPlan(report)
	}

	// ③ 起一轮收尾回合。已在收尾中时不并发起第二轮。
	if svc.Finishing(rootID, taskID) {
		respondJSON(w, http.StatusConflict, map[string]any{
			"error": "该任务正在收尾中，请等它结束",
			"plan":  plan,
		})
		return
	}
	detail, err := svc.BeginFinishWorktree(ctx, kanban.BeginFinishInput{
		RootID: rootID,
		TaskID: taskID,
	})
	if err != nil {
		// 409 而不是 400：这里的每一条错都是「仓库/任务正处在某个需要人处理的状态」
		// —— 正在跑、已在收尾、worktree 目录已失效。请求本身是合法的，是**状态**不答应。
		if teardownTried {
			respondJSON(w, http.StatusConflict, map[string]any{
				"error":  err.Error(),
				"plan":   plan,
				"report": teardownReport,
			})
			return
		}
		respondJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "plan": plan})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"action": "finish_turn_started",
		"plan":   plan,
		"detail": detail,
		"task":   detail.Task,
	})
}

// teardownFinishWorktree 当场做机械清场。
//
// 做成了：广播 task.finish_teardown + 回 200，返回 done=true。
// 没做成：**什么都不写、什么都不播**，返回 done=false 和那份 report —— 调用方要
// 把这次失败落回 agent 那条路（见 agentFallbackPlan）。机械清场是「两个阶段整合」
// 里的加速段，不是门槛：它没成，结论不是「收尾失败」，而是「让 agent 去做」。
// 在这里就把 409 抛出去的话，用户拿到的是一句他无从下手的报错，而机器其实还有
// 一条路没走（2026-10-08 用户实测：「不应该报错，而是应该把 comment 提交给 agent，
// 进入 agent 合并流程」）。
func (h *HTTPHandler) teardownFinishWorktree(w http.ResponseWriter, rootID, taskID string, plan kanban.FinishPlan) (bool, FinishTeardownReport) {
	report := h.AppContext.FinishWorktreeAndRepoint(rootID, taskID)
	if report.Error != "" {
		return false, report
	}
	h.AppContext.BroadcastTaskFinishTeardown(rootID, report)
	respondJSON(w, http.StatusOK, map[string]any{
		"action": "teardown",
		"plan":   plan,
		"report": report,
	})
	return true, report
}

// agentFallbackPlan 把一次失败的机械清场翻译成「交给 agent」的判定。
//
// 这是「agent + 机械合并两个阶段整合」的关键一步：机械那条路能省掉一轮 agent，
// 省不掉的时候就得把活交出去 —— agent 能 commit、能解冲突、能判断哪些改动该留。
// 只把原因收进一句人话 + 一份文件清单：前端会把它们拼成
// 「收尾已发起：agent 正在提交并合并 — 涉及文件：… — <原因>」。
func agentFallbackPlan(report FinishTeardownReport) kanban.FinishPlan {
	// 三个清单在实现里互斥（一次清场只会因为一个原因停），所以取第一个非空的。
	// 原因写在这里而不是直接搬 report.Error：那些句子是**对用户说的**
	// （「先提交或暂存后再收尾」），拼进「已交给 agent」里会自相矛盾。
	cause := strings.TrimSpace(report.Error)
	var files []string
	switch {
	case len(report.ConflictFiles) > 0:
		cause, files = "合并会冲突", report.ConflictFiles
	case len(report.DirtyFiles) > 0:
		cause, files = "主 checkout 有未提交改动", report.DirtyFiles
	case len(report.UserChanges) > 0:
		cause, files = "worktree 里还有没提交的改动", report.UserChanges
	}
	return kanban.FinishPlan{
		Mechanical: false,
		Reason:     "机械合并没能直接做完（" + cause + "）",
		Files:      files,
	}
}
