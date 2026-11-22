package feishu

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// answerRetryPatchTimeout lets a retryable delivery failure reach the owner
// even when the click handler's own deadline was the thing that expired.
const answerRetryPatchTimeout = 5 * time.Second

// errIntakeNotClaimable means the same source message is already being handled
// (or was handled before a restart): creating another task would duplicate it.
var errIntakeNotClaimable = errors.New("intake record is not in picker state")

// claimPickerIntake atomically turns a fresh "picker" record into "creating".
// The lock is held only for the in-memory check and the persisted write; the
// caller must invoke the remote creator after this returns. A missing record,
// an already claimed/created record, or an unexpected status all fail closed.
func claimPickerIntake(store *IntakeStore, messageID string) error {
	if store == nil || messageID == "" {
		return errIntakeNotClaimable
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.pruneLocked()
	rec, ok := store.records[messageID]
	if !ok || rec.Status != "picker" {
		return errIntakeNotClaimable
	}
	before := rec
	rec.TaskID, rec.Status = "", "creating"
	store.records[messageID] = rec
	if err := store.saveLocked(); err != nil {
		store.records[messageID] = before
		return err
	}
	return nil
}

// rollbackPickerClaim returns a claimed record to the picker state when the
// click cannot complete. It never touches a record that another handler has
// already advanced past the claim.
func (s *Service) rollbackPickerClaim(store *IntakeStore, messageID string) {
	if store == nil || messageID == "" {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	rec, ok := store.records[messageID]
	if !ok || rec.Status != "creating" {
		return
	}
	before := rec
	rec.TaskID, rec.Status = "", "picker"
	store.records[messageID] = rec
	if err := store.saveLocked(); err != nil {
		store.records[messageID] = before
		s.logf("飞书入站记录 %s 回滚 claim 失败：%v", messageID, err)
	}
}

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
			var retryable RetryableAnswerError
			if errors.As(applyErr, &retryable) && retryable.RetryableAnswer() {
				s.reopenForRetry(ctx, c.ID, oneLine(applyErr.Error())+"。回答已保留，请稍后重新点击。")
				return nil
			}
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
	// Claim before the (slow, possibly fatal) remote call. On restart the card
	// goes back to "open", but this record survives: if it is already claimed or
	// created, a redelivered click must not create a second task.
	if err := claimPickerIntake(deps.store, c.SourceMessageID); err != nil {
		s.logf("飞书选择卡 %s 入站记录不可重复认领：%v", c.ID, err)
		s.fail(ctx, c.ID, "该需求已在处理或已建任务，请勿重复点击。")
		return nil
	}
	project := c.Projects[selected]
	if project.NodeID == "" || project.RootID == "" {
		s.rollbackPickerClaim(deps.store, c.SourceMessageID)
		s.fail(ctx, c.ID, "所选项目信息不完整，请重新发送需求。")
		return nil
	}

	taskID, err := deps.creator.Create(ctx, project.NodeID, project.RootID, c.IntakeText)
	if err != nil {
		s.logf("飞书选择卡 %s 建任务失败：%v", c.ID, err)
		s.rollbackPickerClaim(deps.store, c.SourceMessageID)
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

// reopenForRetry keeps the card clickable after a remote delivery failure.
// Selected/note were already saved when the click was acknowledged; returning
// to open (instead of failed) lets the owner retry without losing the answer.
func (s *Service) reopenForRetry(ctx context.Context, id, message string) {
	reopened, _, err := s.store.Update(id, func(x *Card) bool {
		x.State = StateOpen
		x.LastError = message
		return true
	})
	if err != nil {
		s.logf("飞书卡片 %s 记下重试提示失败：%v", id, err)
		return
	}
	// The handler context may have expired while waiting for the worker. The
	// error must still become visible on the card.
	patchCtx, cancel := context.WithTimeout(context.Background(), answerRetryPatchTimeout)
	defer cancel()
	if err := s.patchCard(patchCtx, id); err != nil {
		s.logf("飞书卡片 %s 重试提示回写失败：%v", id, err)
	}
	// patchCard's MarkDelivered clears LastError. Restore it without a version
	// bump so the card keeps showing the reason while its buttons stay live.
	if _, _, err := s.store.RestoreOpenError(id, reopened.Version, message); err != nil {
		s.logf("飞书卡片 %s 保留重试提示失败：%v", id, err)
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
