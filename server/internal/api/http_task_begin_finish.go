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
// 四个分支，判据全是「此刻的客观事实」，与任务处于哪个列、什么 status 无关：
//
//	① 会话正在回复        → 什么都不做，回 session_running（收尾本来就要等它停，不是错误）
//	② 分支已合进主干      → 直接机械清场（agent 那半已经做完，再跑一遍只会多一个空提交）
//	③ 已有收尾段但还没成  → 重跑那段（nudge），不追加第二段
//	④ 其余                → 追加收尾段并跑起来（第一次收尾）
//
// 为什么必须这么分：以前这里无条件调 BeginFinishWorktree，于是「agent 已经把活合完了、
// 只差机械清场」的任务会撞上「该任务已在收尾流程中」被 409 顶回来，界面上表现为收尾
// 一直转、点不动、也结束不了。②③ 都是幂等的 —— 连点几次不会多出提交或多出收尾段。
//
// 刻意放在文件末尾：worktree-finish.test.mjs 按
// 「handleKanbanTaskFinishWorktree → handleKanbanTaskMove」切片断言那个 handler 的
// 函数体（整文件跑正则会因为 `[\s\S]*?` 一路跳到文件后面而恒真）。插在中间会让
// 新 handler 的内容落进那个切片里。
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

	// 收尾是「agent + 机械清场」两个阶段的整合，分流把两者编成一条幂等的流水：
	//
	//	① 执行体在动            → session_running（正常等待，不是错）
	//	② 机械清场能直接做完    → teardown（**机械优先**：不等 agent）
	//	②.5 机械清场没做成       → 落回 ③/④，交给 agent（不报错）
	//	③ 已有收尾段            → nudged（不追加第二段）
	//	④ 有 agent 段可继承     → stage_added（agent 先 commit + merge）
	//	④.5 没有 agent 段        → teardown（没有 agent 可继承）
	//
	// ② 的判定是**只读**的（PlanFinishWorktree）：worktree 里没有未提交的改动、
	// 合并不会冲突，就直接把机械清场做掉。以前只要有 agent 段就一定先跑 agent，
	// 于是「agent 早把活合完了、只差机械清场」的任务要白等一轮、还多一个空提交。
	//
	// ②.5 是「两个阶段整合」的兜底：机械那条路是**加速**，不是**门槛**。判定说能做
	// 但执行时被挡下（主 checkout 有改动、撞冲突、worktree 里还有没提交的活）时，
	// 不能把失败直接抛给用户 —— 那等于把整合退化成「机械不成 = 失败」，而 agent
	// 明明还能 commit、能解冲突、能判断哪些改动该留。所以这里落回 ③/④。

	// ① 执行体在动。收尾要拆掉它的 cwd，必须等它停 —— 但这是正常等待，
	// 不是失败，所以回 200 而不是 409：前端据此只提示一句，不渲染成红色报错。
	//
	// 判据是 TaskBusy（会话在回复，或当前段还在跑），不是只看会话探针：
	// 段在跑时只看探针会漏过去，让请求撞进收尾准入变成 409。
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
	// teardownTried 记住「机械清场已经试过一次」，免得 ④.5 再试第二遍。
	teardownTried := false
	var teardownReport FinishTeardownReport
	if plan.Mechanical {
		teardownTried = true
		done, report := h.teardownFinishWorktree(w, rootID, taskID, plan)
		if done {
			return
		}
		// ②.5 没做成 → 换一条路，不报错。
		teardownReport = report
		plan = agentFallbackPlan(report)
	}

	// ③ 收尾段已经在流水里但还没跑成 → 重跑它。不追加第二段：那会堆出一串收尾段。
	if index, exists, err := svc.TaskFinishStageIndex(ctx, rootID, taskID); err == nil && exists {
		detail, rerunErr := svc.RerunStage(ctx, kanban.MoveInput{
			RootID:     rootID,
			TaskID:     taskID,
			StageIndex: index,
		})
		if rerunErr != nil {
			respondJSON(w, http.StatusConflict, map[string]any{"error": rerunErr.Error()})
			return
		}
		respondJSON(w, http.StatusOK, map[string]any{
			"action": "nudged",
			"plan":   plan,
			"detail": detail,
			"task":   detail.Task,
		})
		return
	}

	// ④ 有 agent 段可继承 → 追加收尾段，让 agent 自己 commit + merge。
	// ④.5 没有 agent 段 → 没有 agent 可继承，只能靠机械清场。
	hasAgent, hasAgentErr := svc.TaskHasAgentStage(ctx, rootID, taskID)
	if hasAgentErr != nil {
		respondError(w, http.StatusBadRequest, hasAgentErr)
		return
	}
	if !hasAgent {
		// ② 已经试过一遍就不再试：重复跑清场会把同一段 git 操作做两次。
		if teardownTried {
			h.respondFinishTeardownFailure(w, plan, teardownReport)
			return
		}
		if done, report := h.teardownFinishWorktree(w, rootID, taskID, plan); !done {
			h.respondFinishTeardownFailure(w, plan, report)
		}
		return
	}
	detail, err := svc.BeginFinishWorktree(ctx, kanban.BeginFinishInput{
		RootID: rootID,
		TaskID: taskID,
	})
	// 409 而不是 400：这里的每一条错都是「仓库/任务正处在某个需要人处理的状态」
	// —— 正在跑、已在收尾、worktree 目录已失效。请求本身是合法的，是**状态**不答应，
	// 和 finish-worktree 把冲突也归 409 是同一个口径。
	if err != nil {
		// agent 这条路也走不通 —— 两条路都断了，只能让人来决策。
		//
		// **主 error 用 agent 那条**：它是最后失败的一步，也是「任务此刻为什么不答应」
		// 的直接答案。反过来把机械清场的失败当主 error 会误导：那份报告带着
		// user_changes / dirty_files / conflict_files，前端按「哪个清单非空」挑弹窗，
		// 于是渲染成「合并已成功，但 worktree 里还有没提交的东西」+ 一长串清单，
		// 而真正的拦路虎根本不是那个（2026-10-08 实测 task-9：真原因是当前 agent 段
		// 停在 waiting_user，机械那份失败只是前因）。
		//
		// 所以机械那份只留在 report 里当上下文，**不把它的文件清单提到顶层** ——
		// 提到顶层就等于让前端挑错弹窗。这条口径与「清单进弹窗、句子保持短」一致，
		// 只是这里连弹窗都不该有：能动手处理的原因是一句话，不是一列文件。
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
		"action": "stage_added",
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

// respondFinishTeardownFailure 在**没有 agent 可接手**时，把清场失败如实回给调用方。
//
// 走到这里说明两条路都断了：机械清场做不了，而且任务里没有 agent 段可供收尾段继承
// （BeginFinishWorktree 会拒绝「没有 agent 阶段」）。这时只能让人来决策，所以回
// 409 + 结构化清单 —— 清单**不进句子**，它是 UI 列表渲染的输入。
// 失败**不广播**：这次调用有 HTTP 响应可带结论，WS 那条通道留给异步路径
// （收尾段跑完后的钩子），否则点击者会连着同一个 hub 收到两遍。
func (h *HTTPHandler) respondFinishTeardownFailure(w http.ResponseWriter, plan kanban.FinishPlan, report FinishTeardownReport) {
	respondJSON(w, http.StatusConflict, map[string]any{
		"action":         "teardown",
		"plan":           plan,
		"report":         report,
		"error":          report.Error,
		"conflict_files": report.ConflictFiles,
		"output":         report.Output,
		"dirty_files":    report.DirtyFiles,
		"user_changes":   report.UserChanges,
	})
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
