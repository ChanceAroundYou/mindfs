package api

import (
	"context"
	"fmt"
	"strings"

	"mindfs/server/internal/channels/feishu"
)

// ApplyFeishuAnswer 把一张卡片上的点击变成 MindFS 里的动作。
//
// 顺序上的约定：通道在调用它之前已经把卡片改成了「已收到」，所以这里返回错误
// 只影响卡片的最终状态，不会让用户以为自己那一下没点上。notice 卡片看过了就是
// 处理完了，不需要落库；gate 与 ask 走与网页完全相同的服务方法 —— 网页上能做的
// 事，飞书上点一下就该是同一件事，而不是另开一条写入路径。
func (s *AppContext) ApplyFeishuAnswer(ctx context.Context, a feishu.Answer) error {
	switch a.Kind {
	case feishu.KindNotice:
		return nil
	case feishu.KindGate:
		decision := strings.ToLower(strings.TrimSpace(a.Decision))
		if decision != "approve" && decision != "reject" {
			return fmt.Errorf("飞书卡片 %s：审批决定不合法 %q", a.CardID, a.Decision)
		}
		kanbanService, err := s.GetKanbanService()
		if err != nil {
			return err
		}
		_, err = kanbanService.ResolveWorkflowHumanGate(ctx, a.RootID, a.TaskID, a.WorkflowNodeID, decision, "", a.Note)
		return err
	case feishu.KindAsk:
		text := strings.TrimSpace(a.Text)
		if text == "" {
			return fmt.Errorf("飞书卡片 %s：回答为空", a.CardID)
		}
		kanbanService, err := s.GetKanbanService()
		if err != nil {
			return err
		}
		activityService, err := kanbanService.ActivityServiceForRoot(a.RootID)
		if err != nil {
			return err
		}
		_, _, err = activityService.CreateUserReply(ctx, a.TaskID, text, a.QuestionID)
		return err
	default:
		return fmt.Errorf("飞书卡片 %s：未知类型 %q", a.CardID, a.Kind)
	}
}
