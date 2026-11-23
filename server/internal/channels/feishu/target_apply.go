package feishu

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// applyTargetChoice is the click side of a confirmation card. Every path ends
// through finish/fail so the card, the intake record and the active target
// stay one story.
func (s *Service) applyTargetChoice(ctx context.Context, c Card, selected int, a Action) error {
	deps := s.pickerTaskDeps()
	if deps.store == nil {
		s.fail(ctx, c.ID, "目标确认暂不可用，请稍后重试。")
		return nil
	}
	if c.Target == nil {
		s.fail(ctx, c.ID, "这张卡没有目标信息，请重新发送需求。")
		return nil
	}
	target := *c.Target
	mode := c.TargetMode
	action := ""
	if selected >= 0 && selected < len(c.Choices) {
		action = c.Choices[selected].Value
	}

	// A non-empty task number must resolve exactly. A typo must not silently
	// become "confirm" and create a new task, and a real number outside the
	// card's ≤5 displayed candidates must still resolve against the catalog.
	if raw := strings.TrimSpace(a.Fields["task_no"]); raw != "" {
		n, ok := parseTaskNumber(raw)
		if !ok {
			s.settleTargetRecord(deps.store, c.SourceMessageID, "", "invalid")
			s.fail(ctx, c.ID, fmt.Sprintf("任务号 %q 看不懂，请填 #数字 或留空。", oneLineLimit(raw, 16)))
			return nil
		}
		resolved, found := resolveTaskByNumber(ctx, deps.tasks, target.NodeID, target.RootID, n, c.Candidates)
		if !found {
			s.settleTargetRecord(deps.store, c.SourceMessageID, "", "invalid")
			s.fail(ctx, c.ID, fmt.Sprintf("任务号 #%d 找不到，请检查后重试。", n))
			return nil
		}
		target, mode = resolved, "append"
	}

	switch action {
	case "cancel":
		// Terminal record so a later redelivery is swallowed even if the card's
		// state were lost.
		s.settleTargetRecord(deps.store, c.SourceMessageID, "", "cancelled")
		s.finish(ctx, c.ID, "已取消，本次没有建任务。")
		return nil
	case "switch":
		return s.refreshTargetCandidates(ctx, c, deps, target)
	case "create":
		mode = "create"
	}

	appendMode := mode == "append" && target.TaskID != ""
	if appendMode && deps.appender == nil {
		s.fail(ctx, c.ID, "暂时不能追加到已有任务，请稍后重试。")
		return nil
	}
	if !appendMode && deps.creator == nil {
		s.fail(ctx, c.ID, "暂时不能新建任务，请稍后重试。")
		return nil
	}
	if target.NodeID == "" || target.RootID == "" {
		s.fail(ctx, c.ID, "目标信息不完整，请重新发送需求。")
		return nil
	}

	// Claim the intake record before the remote write: a restart resets the
	// card from applying to open, so a redelivered click must be stopped here
	// rather than repeat the side effect.
	claimedStatus := "creating"
	if appendMode {
		claimedStatus = "appending"
	}
	claimed, err := deps.store.ClaimTarget(c.SourceMessageID, claimedStatus)
	if err != nil {
		s.logf("飞书确认卡 %s 认领入站记录失败：%v", c.ID, err)
		s.fail(ctx, c.ID, "目标确认暂不可用，请稍后重试。")
		return nil
	}
	if !claimed {
		s.fail(ctx, c.ID, "该需求已在处理或已建任务，请勿重复点击。")
		return nil
	}

	// The card is a snapshot: if the chat's target moved to a different task
	// after this card was created, writing now would fan out two tasks.
	if active, ok := deps.store.ActiveTarget(c.ChatID); ok &&
		active.UpdatedAt.After(c.CreatedAt) && active.TaskID != target.TaskID {
		s.settleTargetRecord(deps.store, c.SourceMessageID, "", "stale")
		s.fail(ctx, c.ID, "目标已变化，请重新发送需求后再确认。")
		return nil
	}

	if appendMode {
		return s.appendConfirmedTarget(ctx, c, deps, target)
	}
	return s.createConfirmedTarget(ctx, c, deps, target)
}

// resolveTaskByNumber finds a task by number: first against the live catalog so
// a number outside the card's displayed candidates still resolves, then among
// the candidates already shown. Only tasks with a real id are append targets.
func resolveTaskByNumber(ctx context.Context, tasks TaskCatalog, nodeID, rootID string, n int, candidates []TargetRef) (TargetRef, bool) {
	if tasks != nil {
		if refs, err := tasks.Match(ctx, nodeID, rootID, fmt.Sprintf("#%d", n)); err == nil {
			for _, ref := range refs {
				if ref.TaskNumber == n && ref.TaskID != "" {
					return TargetRef{
						NodeID: ref.NodeID, RootID: ref.RootID, TaskID: ref.TaskID,
						TaskNumber: ref.TaskNumber, TaskTitle: ref.Title,
					}, true
				}
			}
		}
	}
	for _, cand := range candidates {
		if cand.TaskNumber == n && cand.TaskID != "" {
			return cand, true
		}
	}
	return TargetRef{}, false
}

func (s *Service) appendConfirmedTarget(ctx context.Context, c Card, deps pickerTaskDeps, target TargetRef) error {
	label := projectLabel(target)
	if err := deps.appender.Append(ctx, target, c.IntakeText); err != nil {
		s.logf("飞书确认卡 %s 追加失败：%v", c.ID, err)
		if errors.Is(err, ErrTargetStale) {
			// The triple no longer holds: drop the active target and settle the
			// record terminally so nothing keeps retrying a dead target.
			if clearErr := deps.store.ClearActiveTarget(c.ChatID); clearErr != nil {
				s.logf("飞书确认卡 %s 清理活跃目标失败：%v", c.ID, clearErr)
			}
			s.settleTargetRecord(deps.store, c.SourceMessageID, "", "stale")
			s.fail(ctx, c.ID, "目标已失效（任务可能被删除或移动），请重新发送。")
			return nil
		}
		// Transient: keep the active target and reopen the card so the owner can
		// retry without losing the message.
		s.rollbackTargetClaim(deps.store, c.SourceMessageID)
		s.reopenForRetry(ctx, c.ID, "暂时追加不上："+oneLine(err.Error())+"。请稍后重试。")
		return nil
	}
	s.settleTargetRecord(deps.store, c.SourceMessageID, target.TaskID, "appended")
	s.rememberActiveTarget(c, deps.store, target)
	s.finish(ctx, c.ID, "已追加到"+targetRefLabel(target)+"（项目："+label+"）。")
	return nil
}

func (s *Service) createConfirmedTarget(ctx context.Context, c Card, deps pickerTaskDeps, target TargetRef) error {
	label := projectLabel(target)
	taskID, err := deps.creator.Create(ctx, target.NodeID, target.RootID, c.IntakeText)
	if err != nil {
		s.logf("飞书确认卡 %s 建任务失败：%v", c.ID, err)
		s.rollbackTargetClaim(deps.store, c.SourceMessageID)
		s.fail(ctx, c.ID, "建任务失败，请稍后重试。")
		return nil
	}
	if err := deps.store.SetStatus(c.SourceMessageID, taskID, "created"); err != nil {
		s.logf("飞书确认卡 %s 更新入站记录失败：%v", c.ID, err)
		s.fail(ctx, c.ID, "任务已建，但没能记下去重记录，请到网页确认。")
		return nil
	}
	created := target
	created.TaskID = taskID
	s.rememberActiveTarget(c, deps.store, created)
	s.finish(ctx, c.ID, "已建任务 "+taskID+"（项目："+label+"）。")
	return nil
}

func (s *Service) rememberActiveTarget(c Card, store *IntakeStore, target TargetRef) {
	if c.ChatID == "" {
		return
	}
	if err := store.SetActiveTarget(ActiveTarget{
		ChatID: c.ChatID, NodeID: target.NodeID, RootID: target.RootID,
		TaskID: target.TaskID, TaskNumber: target.TaskNumber, TaskTitle: target.TaskTitle,
	}); err != nil {
		s.logf("飞书确认卡 %s 记录活跃目标失败：%v", c.ID, err)
	}
}

// settleTargetRecord records an intake status; a failure only logs — the card
// is already the owner's source of truth.
func (s *Service) settleTargetRecord(store *IntakeStore, messageID, taskID, status string) {
	if store == nil || messageID == "" {
		return
	}
	if err := store.SetStatus(messageID, taskID, status); err != nil {
		s.logf("飞书确认卡入站记录 %s 写状态 %s 失败：%v", messageID, status, err)
	}
}

// rollbackTargetClaim returns a claimed record to "confirming" when the side
// effect definitively did not happen, so the open card can be retried.
func (s *Service) rollbackTargetClaim(store *IntakeStore, messageID string) {
	if store == nil || messageID == "" {
		return
	}
	if err := store.RollbackTarget(messageID); err != nil {
		s.logf("飞书入站记录 %s 回滚 target claim 失败：%v", messageID, err)
	}
}

func projectLabel(target TargetRef) string {
	if target.ProjectLabel != "" {
		return target.ProjectLabel
	}
	return target.RootID + " · " + target.NodeID
}
