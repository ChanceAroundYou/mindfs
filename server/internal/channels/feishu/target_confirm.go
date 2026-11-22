package feishu

import (
	"context"
	"fmt"
	"strings"
)

// TaskAppender adds one message to an existing task. The implementation owns
// the server-side re-validation: it must only accept the call when the whole
// (node, project, task) triple still holds, and fail otherwise. The channel
// layer cannot see the task store, so it treats any error as "target is stale"
// and asks the owner again instead of silently creating a task elsewhere.
type TaskAppender interface {
	Append(ctx context.Context, target TargetRef, input string) error
}

// targetConfirmChoice is the button index 「确认」 occupies on a confirmation
// card. A form submit (task number typed in) means the same thing.
const targetConfirmChoice = 0

// SetTargetDeps installs the appender and task catalog used for explicit
// target confirmation. A nil appender degrades to 「新建 only」: no message is
// ever appended without the owner's confirmation; a nil catalog just means the
// card lists no candidates.
func (s *IntakeService) SetTargetDeps(appender TaskAppender, tasks TaskCatalog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appender = appender
	s.tasks = tasks
}

func (s *IntakeService) targetDeps() (TaskAppender, TaskCatalog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appender, s.tasks
}

func (s *IntakeService) logTargetf(format string, args ...any) {
	if s.cfg.Logf != nil {
		s.cfg.Logf(format, args...)
	}
}

// explicitTaskNumber reports the task number a message names with #N. It is
// deliberately narrow: only a literal #digits counts as "the owner picked a
// specific task", everything else is left to the active target.
func explicitTaskNumber(text string) (int, bool) {
	for i := 0; i < len(text); i++ {
		if text[i] != '#' {
			continue
		}
		j := i + 1
		for j < len(text) && text[j] >= '0' && text[j] <= '9' {
			j++
		}
		if j == i+1 {
			continue
		}
		n := 0
		for _, r := range text[i+1 : j] {
			n = n*10 + int(r-'0')
		}
		if n > 0 {
			return n, true
		}
	}
	return 0, false
}

// resolveUniquelyNamedTarget keeps the "no immediate task" promise: a uniquely
// resolved project either appends to the chat's active target or asks the
// owner to confirm on a card before anything is written.
func (s *IntakeService) resolveUniquelyNamedTarget(ctx context.Context, msg InboundMessage, id string, project ProjectRef, text string, truncated bool) error {
	nodeID := project.NodeID
	if nodeID == "" {
		nodeID = s.cfg.NodeID
	}
	label := pickerLabel(project, s.cfg.NodeID)
	number, explicit := explicitTaskNumber(text)
	appender, tasks := s.targetDeps()

	if !explicit && appender != nil {
		if active, ok := s.store.ActiveTarget(msg.ChatID); ok && active.TaskID != "" &&
			active.NodeID == nodeID && active.RootID == project.RootID {
			target := TargetRef{
				NodeID: active.NodeID, RootID: active.RootID, TaskID: active.TaskID,
				TaskNumber: active.TaskNumber, TaskTitle: active.TaskTitle, ProjectLabel: label,
			}
			if err := appender.Append(ctx, target, text); err == nil {
				if err := s.store.SetStatus(id, active.TaskID, "appended"); err != nil {
					return err
				}
				n := Notice{
					Kind: KindNotice, SourceMessageID: id, ChatID: msg.ChatID, NodeID: s.cfg.NodeID,
					Text: "已追加到" + targetRefLabel(target) + "（项目：" + label + "）。" + truncSuffix(truncated),
				}
				return s.notifyWithCardID(ctx, id, n)
			} else {
				// The message must not be lost because a stale target failed:
				// fall through to the confirmation card, which the owner can
				// still act on.
				s.logTargetf("飞书入站直接追加到任务 %s 失败，改为确认卡：%v", active.TaskID, err)
			}
		}
	}

	var candidates []TargetRef
	if tasks != nil {
		refs, err := tasks.Match(ctx, nodeID, project.RootID, "")
		if err != nil {
			s.logTargetf("飞书确认卡查询任务候选失败：%v", err)
		} else {
			candidates = targetRefsFromTasks(refs)
		}
	}
	target := TargetRef{NodeID: nodeID, RootID: project.RootID, ProjectLabel: label}
	mode := "create"
	if explicit {
		for _, cand := range candidates {
			if cand.TaskNumber == number {
				target = cand
				mode = "append"
				break
			}
		}
	}

	n := Notice{
		Kind:            KindTarget,
		SourceMessageID: id,
		ChatID:          msg.ChatID,
		NodeID:          s.cfg.NodeID,
		IntakeText:      text,
		Text:            targetCardText(target, candidates, text, truncated),
		Target:          &target,
		Candidates:      candidates,
		TargetMode:      mode,
		PickerTTL:       s.pickerTTL(),
	}
	if err := s.notifyWithCardID(ctx, id, n); err != nil {
		// The owner saw nothing, so the message may be redelivered: release
		// the claim rather than have dedup swallow it silently (same contract
		// as the project picker).
		if relErr := s.store.Release(id); relErr != nil {
			return fmt.Errorf("%w（且未能释放去重占坑：%v）", err, relErr)
		}
		return err
	}
	return s.store.SetStatus(id, "", "confirming")
}

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

	if raw := strings.TrimSpace(a.Fields["task_no"]); raw != "" {
		if n, ok := parseTaskNumber(raw); ok && n != target.TaskNumber {
			found := false
			for _, cand := range c.Candidates {
				if cand.TaskNumber == n {
					target, mode, found = cand, "append", true
					break
				}
			}
			if !found {
				s.fail(ctx, c.ID, fmt.Sprintf("没有找到任务 #%d，请重新选择。", n))
				return nil
			}
		}
	}

	switch c.Choices[selected].Value {
	case "cancel":
		s.finish(ctx, c.ID, "已取消，本次没有建任务。")
		return nil
	case "switch":
		return s.refreshTargetCandidates(ctx, c, deps, target)
	case "create":
		mode = "create"
	}

	label := target.ProjectLabel
	if label == "" {
		label = target.RootID + " · " + target.NodeID
	}
	if mode == "append" && target.TaskID != "" {
		if deps.appender == nil {
			s.fail(ctx, c.ID, "暂时不能追加到已有任务，请稍后重试。")
			return nil
		}
		if target.NodeID == "" || target.RootID == "" {
			s.fail(ctx, c.ID, "目标信息不完整，请重新发送需求。")
			return nil
		}
		if err := deps.appender.Append(ctx, target, c.IntakeText); err != nil {
			s.logf("飞书确认卡 %s 追加失败：%v", c.ID, err)
			// The stored triple no longer holds: drop it so later messages do
			// not keep aiming at a task that is gone.
			if err := deps.store.ClearActiveTarget(c.ChatID); err != nil {
				s.logf("飞书确认卡 %s 清理活跃目标失败：%v", c.ID, err)
			}
			s.fail(ctx, c.ID, "目标已失效（任务可能被删除或移动），请重新发送。")
			return nil
		}
		if err := deps.store.SetStatus(c.SourceMessageID, target.TaskID, "appended"); err != nil {
			s.logf("飞书确认卡 %s 更新入站记录失败：%v", c.ID, err)
		}
		if c.ChatID != "" {
			if err := deps.store.SetActiveTarget(ActiveTarget{
				ChatID: c.ChatID, NodeID: target.NodeID, RootID: target.RootID,
				TaskID: target.TaskID, TaskNumber: target.TaskNumber, TaskTitle: target.TaskTitle,
			}); err != nil {
				s.logf("飞书确认卡 %s 记录活跃目标失败：%v", c.ID, err)
			}
		}
		s.finish(ctx, c.ID, "已追加到"+targetRefLabel(target)+"（项目："+label+"）。")
		return nil
	}

	if deps.creator == nil {
		s.fail(ctx, c.ID, "暂时不能新建任务，请稍后重试。")
		return nil
	}
	if target.NodeID == "" || target.RootID == "" {
		s.fail(ctx, c.ID, "目标信息不完整，请重新发送需求。")
		return nil
	}
	taskID, err := deps.creator.Create(ctx, target.NodeID, target.RootID, c.IntakeText)
	if err != nil {
		s.logf("飞书确认卡 %s 建任务失败：%v", c.ID, err)
		s.fail(ctx, c.ID, "建任务失败，请稍后重试。")
		return nil
	}
	if err := deps.store.SetStatus(c.SourceMessageID, taskID, "created"); err != nil {
		s.logf("飞书确认卡 %s 更新入站记录失败：%v", c.ID, err)
		s.fail(ctx, c.ID, "任务已建，但没能记下去重记录，请到网页确认。")
		return nil
	}
	if c.ChatID != "" {
		if err := deps.store.SetActiveTarget(ActiveTarget{
			ChatID: c.ChatID, NodeID: target.NodeID, RootID: target.RootID, TaskID: taskID,
		}); err != nil {
			s.logf("飞书确认卡 %s 记录活跃目标失败：%v", c.ID, err)
		}
	}
	s.finish(ctx, c.ID, "已建任务 "+taskID+"（项目："+label+"）。")
	return nil
}

// refreshTargetCandidates re-asks the task catalog and re-opens the card. The
// click acknowledgement already ran, so this only rewrites the body.
func (s *Service) refreshTargetCandidates(ctx context.Context, c Card, deps pickerTaskDeps, target TargetRef) error {
	if deps.tasks == nil {
		s.fail(ctx, c.ID, "任务列表暂不可用，请稍后重试。")
		return nil
	}
	refs, err := deps.tasks.Match(ctx, target.NodeID, target.RootID, "")
	if err != nil {
		s.logf("飞书确认卡 %s 刷新候选失败：%v", c.ID, err)
		s.fail(ctx, c.ID, "没能刷新任务列表，请稍后重试。")
		return nil
	}
	candidates := targetRefsFromTasks(refs)
	text := targetCardText(target, candidates, c.IntakeText, false) + "\n\n已刷新候选，请重新确认。"
	_, changed, err := s.store.Update(c.ID, func(x *Card) bool {
		if x.State != StateApplying {
			return false
		}
		x.Candidates = candidates
		x.Text = text
		x.State = StateOpen
		x.Selected = -1
		x.Note = ""
		return true
	})
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return s.patchCard(ctx, c.ID)
}

// targetCardText is the explanatory body of an open confirmation card: what
// was understood, what the message says, and how to override the task.
func targetCardText(target TargetRef, candidates []TargetRef, text string, truncated bool) string {
	label := target.ProjectLabel
	if label == "" {
		label = target.RootID + " · " + target.NodeID
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**项目**：%s\n\n", label)
	b.WriteString("**消息**：" + oneLineLimit(text, 200))
	b.WriteString("\n\n")
	if target.TaskNumber > 0 || target.TaskID != "" {
		b.WriteString("默认" + targetRefLabel(target) + "；点「确认」把这条消息追加进去。\n")
	} else {
		b.WriteString("默认在这个项目里新建任务；点「确认」建任务。\n")
	}
	if len(candidates) > 0 {
		b.WriteString("也可以在表单里填任务号（如 #42）再提交，把消息追加到已有任务：\n")
		for _, cand := range candidates {
			fmt.Fprintf(&b, "- %s\n", candidateLabel(cand))
		}
	}
	if truncated {
		b.WriteString("\n消息超过 32 KiB，后续内容已截断。")
	}
	return b.String()
}

func candidateLabel(t TargetRef) string {
	label := targetRefLabel(t)
	if t.TaskTitle != "" && t.TaskNumber > 0 {
		label += "｜" + t.TaskTitle
	}
	return label
}

func targetRefLabel(t TargetRef) string {
	if t.TaskNumber > 0 {
		return fmt.Sprintf("任务 #%d", t.TaskNumber)
	}
	if t.TaskID != "" {
		return "任务 " + t.TaskID
	}
	return "任务"
}

func targetRefsFromTasks(refs []TaskRef) []TargetRef {
	out := make([]TargetRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, TargetRef{
			NodeID: ref.NodeID, RootID: ref.RootID, TaskID: ref.TaskID,
			TaskNumber: ref.TaskNumber, TaskTitle: ref.Title,
		})
	}
	return out
}

func truncSuffix(truncated bool) string {
	if truncated {
		return "消息超过 32 KiB，后续内容已截断。"
	}
	return ""
}

func oneLineLimit(text string, max int) string {
	line := oneLine(text)
	runes := []rune(line)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return line
}
