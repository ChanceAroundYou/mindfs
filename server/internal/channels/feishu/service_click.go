package feishu

import (
	"context"
	"strings"
	"sync"
)

// pickerTaskDeps are installed at assembly time by SetPickerTaskDeps. They
// live beside the click path instead of on the M1 Service struct so adding M2
// does not change the constructor every M1 caller uses.
type pickerTaskDeps struct {
	store   *IntakeStore
	creator TaskCreator
}

var pickerDepsByService sync.Map // map[*Service]pickerTaskDeps

// SetPickerTaskDeps installs the intake ledger and task creator used when a
// project-choice card is clicked. Both are required; with either missing a
// picker click fails its card closed instead of creating an untracked task.
func (s *Service) SetPickerTaskDeps(store *IntakeStore, creator TaskCreator) {
	if s == nil {
		return
	}
	pickerDepsByService.Store(s, pickerTaskDeps{store: store, creator: creator})
}

func (s *Service) pickerTaskDeps() pickerTaskDeps {
	v, ok := pickerDepsByService.Load(s)
	if !ok {
		return pickerTaskDeps{}
	}
	return v.(pickerTaskDeps)
}

// HandleAction applies one click. Its contract, in order: only the owner's
// click counts; only the message the card was sent as counts; an expired or
// already-settled card changes nothing; and the clicker sees "已收到" before
// anything is applied, so a slow MindFS never looks like a dead button.
func (s *Service) HandleAction(ctx context.Context, a Action) error {
	if s.cfg.OwnerOpenID == "" || a.OperatorID != s.cfg.OwnerOpenID {
		return nil
	}
	c, ok := s.store.Get(a.CardID)
	if !ok {
		return nil
	}
	if c.MessageID != "" && a.MessageID != "" && a.MessageID != c.MessageID {
		return nil
	}
	if c.Expired(s.now()) {
		if _, _, err := s.store.Update(c.ID, func(x *Card) bool {
			if x.State != StateOpen && x.State != StateApplying {
				return false
			}
			x.State = StateExpired
			return true
		}); err != nil {
			return err
		}
		return s.patchCard(ctx, c.ID)
	}

	selected, text, ok := interpret(c, a)
	if !ok {
		if c.Kind == KindPicker {
			// A stale card can still be clicked after its choices changed.
			// Tell the owner plainly rather than silently dropping the click.
			return s.failPickerSelection(ctx, c.ID)
		}
		return nil
	}

	_, changed, err := s.store.Update(c.ID, func(x *Card) bool {
		if x.State != StateOpen {
			return false
		}
		x.State = StateApplying
		x.Selected = selected
		if text != "" {
			x.Note = text
		}
		x.LastError = ""
		return true
	})
	if err != nil {
		return err
	}
	if !changed {
		// Someone else got there first (a second click, or the web UI). Show
		// the card's real state instead of acting twice.
		return s.patchCard(ctx, c.ID)
	}

	if err := s.patchCard(ctx, c.ID); err != nil {
		// The answer still matters more than the acknowledgement; the next
		// patch will show the outcome.
		s.logf("飞书卡片 %s 回执失败，继续处理点击：%v", c.ID, err)
	}

	// The acknowledgement above is already on its way; the side effect that
	// follows must never run before it (M1's click contract).
	if c.Kind == KindPicker {
		return s.applyPickerChoice(ctx, c, selected)
	}

	if c.Kind == KindNotice {
		return s.settleNotice(ctx, c.ID, text)
	}

	answer := Answer{
		CardID:         c.ID,
		Kind:           c.Kind,
		AccountID:      c.AccountID,
		RootID:         c.RootID,
		NodeID:         c.NodeID,
		TaskID:         c.TaskID,
		WorkflowNodeID: c.WorkflowNodeID,
		QuestionID:     c.QuestionID,
		Text:           text,
		Note:           text,
	}
	if c.Kind == KindGate {
		answer.Decision = c.Choices[selected].Value
	}

	handler := s.answerHandler()
	if handler != nil {
		if applyErr := handler(ctx, answer); applyErr != nil {
			s.fail(ctx, c.ID, "没能执行："+oneLine(applyErr.Error()))
			return nil
		}
	}
	s.finish(ctx, c.ID, doneSummary(c, selected, text))
	return nil
}

// interpret reads a click against the card it names; anything inconsistent
// with the card as sent is not an answer.
func interpret(c Card, a Action) (selected int, text string, ok bool) {
	switch c.Kind {
	case KindGate:
		if a.Choice < 0 || a.Choice >= len(c.Choices) {
			return 0, "", false
		}
		return a.Choice, "", true
	case KindAsk:
		text = strings.TrimSpace(a.Text)
		if a.Choice >= 0 && a.Choice < len(c.Choices) {
			if text == "" {
				text = c.Choices[a.Choice].Label
			}
			return a.Choice, text, true
		}
		if text == "" {
			return 0, "", false
		}
		return -1, text, true
	case KindNotice:
		return 0, strings.TrimSpace(a.Text), true
	case KindPicker:
		if a.Choice < 0 || a.Choice >= len(c.Projects) {
			return 0, "", false
		}
		return a.Choice, "", true
	default:
		return 0, "", false
	}
}

// failPickerSelection settles a card whose selected index no longer maps to a
// project. Only an open card may be changed this way: a late click on a card
// that already settled must not overwrite its real outcome.
func (s *Service) failPickerSelection(ctx context.Context, id string) error {
	_, _, err := s.store.Update(id, func(x *Card) bool {
		if x.State != StateOpen {
			return false
		}
		x.State = StateFailed
		x.Summary = "选择已失效，请重新发送需求。"
		return true
	})
	if err != nil {
		return err
	}
	return s.patchCard(ctx, id)
}

// applyPickerChoice creates the task on the project carried by the card (not
// by the card's ambient root): the card may outlive the current selection and
// projects from different nodes can share a root id.
func (s *Service) applyPickerChoice(ctx context.Context, c Card, selected int) error {
	deps := s.pickerTaskDeps()
	if deps.store == nil || deps.creator == nil || c.SourceMessageID == "" {
		s.fail(ctx, c.ID, "项目选择暂不可用，请稍后重试。")
		return nil
	}
	project := c.Projects[selected]
	if project.NodeID == "" || project.RootID == "" {
		s.fail(ctx, c.ID, "所选项目信息不完整，请重新发送需求。")
		return nil
	}

	taskID, err := deps.creator.Create(ctx, project.NodeID, project.RootID, c.IntakeText)
	if err != nil {
		s.logf("飞书选择卡 %s 建任务失败：%v", c.ID, err)
		s.fail(ctx, c.ID, "建任务失败，请稍后重试。")
		return nil
	}
	// Record is first-write-wins and T4 already wrote "picker", so the click
	// must transition the same record with SetStatus.
	if err := deps.store.SetStatus(c.SourceMessageID, taskID, "created"); err != nil {
		s.logf("飞书选择卡 %s 更新入站记录失败：%v", c.ID, err)
		s.fail(ctx, c.ID, "任务已建，但没能记下去重记录，请到网页确认。")
		return nil
	}
	label := project.Label
	if label == "" {
		label = project.RootID + " · " + project.NodeID
	}
	s.finish(ctx, c.ID, "已在「"+label+"」建任务。")
	return nil
}

func (s *Service) settleNotice(ctx context.Context, id, note string) error {
	summary := "已记录，结案。"
	if note != "" {
		summary = "已记录你的处理说明，结案。"
	}
	if _, _, err := s.store.Update(id, func(x *Card) bool {
		x.State = StateDone
		x.Summary = summary
		return true
	}); err != nil {
		return err
	}
	return s.patchCard(ctx, id)
}

func (s *Service) finish(ctx context.Context, id, summary string) {
	if _, _, err := s.store.Update(id, func(x *Card) bool {
		x.State = StateDone
		x.Summary = summary
		return true
	}); err != nil {
		s.logf("飞书卡片 %s 记下结果失败：%v", id, err)
		return
	}
	if err := s.patchCard(ctx, id); err != nil {
		s.logf("飞书卡片 %s 结果回写失败：%v", id, err)
	}
}

func (s *Service) fail(ctx context.Context, id, summary string) {
	if _, _, err := s.store.Update(id, func(x *Card) bool {
		x.State = StateFailed
		x.Summary = summary
		return true
	}); err != nil {
		s.logf("飞书卡片 %s 记下失败失败：%v", id, err)
		return
	}
	if err := s.patchCard(ctx, id); err != nil {
		s.logf("飞书卡片 %s 失败回写失败：%v", id, err)
	}
}

func doneSummary(c Card, selected int, text string) string {
	switch c.Kind {
	case KindGate:
		if selected >= 0 && selected < len(c.Choices) {
			if c.Choices[selected].Value == "approve" {
				return "已批准。"
			}
			return "已驳回。"
		}
		return "已处理。"
	case KindAsk:
		if text != "" {
			return "已收到你的回答：「" + text + "」。"
		}
		return "已收到你的回答。"
	default:
		return "已处理。"
	}
}
