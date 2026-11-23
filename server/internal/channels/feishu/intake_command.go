package feishu

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// SetReviewClassifier installs the message → kanban review-category matcher.
// It is injected by the app layer because this package must not import kanban;
// the hits annotate a target confirmation card before the owner clicks.
func (s *IntakeService) SetReviewClassifier(fn func(string) []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reviewClassifier = fn
}

func (s *IntakeService) reviewHits(text string) []string {
	s.mu.Lock()
	fn := s.reviewClassifier
	s.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(text)
}

// intakeCommand is a message the owner addressed explicitly rather than by
// writing ordinary task text: a bare `#N`, `#N body`, `切换`, or `新任务`.
type intakeCommand struct {
	kind   commandKind
	number int
	body   string
}

type commandKind int

const (
	commandSwitch commandKind = iota + 1
	commandNewTask
	commandTask
)

// parseIntakeCommand recognises only unambiguous forms. The whole message must
// be `切换` / `新任务`, or start with `#<digits>` followed by whitespace or the
// end of the message. A command word inside a longer sentence stays ordinary
// task text (the rulings make this deliberate: 「帮我切换一下」 is a sentence,
// not a command).
func parseIntakeCommand(text string) (intakeCommand, bool) {
	trimmed := strings.TrimSpace(text)
	switch trimmed {
	case "切换":
		return intakeCommand{kind: commandSwitch}, true
	case "新任务":
		return intakeCommand{kind: commandNewTask}, true
	}
	if !strings.HasPrefix(trimmed, "#") {
		return intakeCommand{}, false
	}
	rest := trimmed[1:]
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	if i == 0 {
		return intakeCommand{}, false
	}
	if i < len(rest) && !commandSpace(rest[i]) {
		return intakeCommand{}, false
	}
	number, err := strconv.Atoi(rest[:i])
	if err != nil || number <= 0 {
		return intakeCommand{}, false
	}
	return intakeCommand{kind: commandTask, number: number, body: strings.TrimSpace(rest[i:])}, true
}

func commandSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// runIntakeCommand handles a parsed command. The switch/new-task commands need
// no project resolution; the task commands resolve the project exactly like an
// ordinary message would (binding first, then a unique name).
func (s *IntakeService) runIntakeCommand(ctx context.Context, msg InboundMessage, id, text string, cmd intakeCommand, truncated bool) error {
	switch cmd.kind {
	case commandSwitch:
		if err := s.store.ClearActiveTarget(msg.ChatID); err != nil {
			return s.fail(ctx, msg, "", "failed", "暂时无法清除默认目标，请稍后重试。", err)
		}
		return s.reject(ctx, msg, "", "cancelled", "已清除默认目标，下一条消息会重新识别项目与任务。")
	case commandNewTask:
		if err := s.store.ClearActiveTarget(msg.ChatID); err != nil {
			return s.fail(ctx, msg, "", "failed", "暂时无法清除默认目标，请稍后重试。", err)
		}
		return s.reject(ctx, msg, "", "cancelled", "下一条消息会重新确认目标（默认新建任务）。")
	case commandTask:
		return s.runTaskCommand(ctx, msg, id, text, cmd, truncated)
	default:
		return nil
	}
}

func (s *IntakeService) runTaskCommand(ctx context.Context, msg InboundMessage, id, text string, cmd intakeCommand, truncated bool) error {
	res, err := ResolveProject(text, msg.ChatID, s.cfg.Bindings, s.catalog, s.cfg.DefaultRootID, s.cfg.PickerMax)
	if err != nil {
		return s.fail(ctx, msg, "", "catalog_error", "暂时查不到项目，请稍后再试。", err)
	}
	if res.Kind != ResolutionProject {
		return s.reject(ctx, msg, "", "invalid", "没有找到唯一项目，没法定位任务。请写明项目名，如 #fn/docs。")
	}
	nodeID := res.Project.NodeID
	if nodeID == "" {
		nodeID = s.cfg.NodeID
	}
	label := pickerLabel(res.Project, s.cfg.NodeID)
	appender, tasks := s.targetDeps()
	target, ok := resolveTaskByNumber(ctx, tasks, nodeID, res.Project.RootID, cmd.number, nil)
	if !ok {
		return s.reject(ctx, msg, "", "invalid", fmt.Sprintf("没有找到任务 #%d（项目：%s）。", cmd.number, label))
	}
	target.ProjectLabel = label
	active := ActiveTarget{
		ChatID: msg.ChatID, NodeID: target.NodeID, RootID: target.RootID,
		TaskID: target.TaskID, TaskNumber: target.TaskNumber, TaskTitle: target.TaskTitle,
	}
	if cmd.body == "" {
		if err := s.store.SetActiveTarget(active); err != nil {
			return s.fail(ctx, msg, "", "failed", "暂时无法记住目标，请稍后重试。", err)
		}
		return s.reject(ctx, msg, target.TaskID, "appended",
			"已切换到"+commandTargetLabel(target)+"。后续消息会发到这里。")
	}
	if appender == nil {
		return s.reject(ctx, msg, "", "invalid", "还不能直接追加到任务，请稍后重试。")
	}
	if err := appender.Append(ctx, target, cmd.body); err != nil {
		if errors.Is(err, ErrTargetStale) {
			return s.reject(ctx, msg, "", "invalid", fmt.Sprintf("任务 #%d 已经不在了，请重新选择。", cmd.number))
		}
		return s.reject(ctx, msg, "", "invalid", fmt.Sprintf("暂时没能追加到任务 #%d，请稍后重试。", cmd.number))
	}
	if err := s.store.SetActiveTarget(active); err != nil {
		s.logTargetf("飞书入站记住命令目标失败：%v", err)
	}
	return s.reject(ctx, msg, target.TaskID, "appended",
		"已追加到"+commandTargetLabel(target)+"（项目："+label+"）。后续消息会发到这里。"+truncSuffix(truncated))
}

// commandTargetLabel appends the task title when known, e.g. 「任务 #12：登录页」.
func commandTargetLabel(t TargetRef) string {
	label := targetRefLabel(t)
	if t.TaskTitle != "" {
		label += "：" + t.TaskTitle
	}
	return label
}
