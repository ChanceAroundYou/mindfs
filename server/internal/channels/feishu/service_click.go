package feishu

import (
	"context"
	"strings"
)

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
	default:
		return 0, "", false
	}
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
