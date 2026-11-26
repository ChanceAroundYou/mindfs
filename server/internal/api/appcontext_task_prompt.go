package api

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/kanban"
	"mindfs/server/internal/session"
)

// recordTaskQuestionFromEvent 把会话里的 ask_user 同步成任务对话里的 question 卡。
//
// 为什么挂在 BroadcastSessionUpdate 而不是看板执行器：ask_user 可能来自直连回合、
// 工作流节点回合或用户从会话查看器发起的回合，它们最终都经过这一条广播路径。
// tool_call（awaiting=true）建卡；tool_call_update complete 只收卡，不写答案 ——
// 真实答案由回答路径（WS session.answer_question）补写。
func (s *AppContext) recordTaskQuestionFromEvent(rootID, sessionKey string, event *StreamEvent) {
	if s == nil || event == nil {
		return
	}
	callID, awaiting, ok := askUserStreamEvent(event)
	if !ok || strings.TrimSpace(callID) == "" {
		return
	}
	manager, err := s.GetSessionManager(rootID)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sess, err := manager.Get(ctx, sessionKey, 0)
	if err != nil || sess == nil {
		return
	}
	if sess.Type == session.TypeRouter {
		return
	}
	taskID := strings.TrimSpace(sess.TaskID)
	if taskID == "" {
		return
	}
	if !awaiting {
		toolCall, _ := event.Data.(agenttypes.ToolCall)
		if askUserToolStatusAnswered(toolCall.Status) {
			s.markTaskQuestionAnswered(ctx, rootID, sessionKey, callID, nil)
			return
		}
		// 取消/失败：卡已经不可能再回答，按 cancelled 收口而不是 completed。
		s.closeTaskQuestions(ctx, rootID, sessionKey, "提问未完成（"+strings.TrimSpace(toolCall.Status)+"）")
		return
	}
	toolCall, ok := event.Data.(agenttypes.ToolCall)
	if !ok {
		return
	}
	kanbanService, err := s.GetKanbanService()
	if err != nil {
		return
	}
	activityService, err := kanbanService.ActivityServiceForRoot(rootID)
	if err != nil {
		return
	}
	agent := session.InferAgentFromSession(sess)
	payload, err := taskQuestionPayload(toolCall, agent)
	if err != nil {
		log.Printf("[kanban] task.question.payload.error root=%s task=%s session=%s tool=%s err=%v", rootID, taskID, sessionKey, callID, err)
		return
	}
	if _, _, err := activityService.RecordTaskQuestion(ctx, kanban.TaskQuestionInput{
		TaskID:      taskID,
		SessionKey:  sessionKey,
		ToolUseID:   callID,
		Body:        askUserSummary(toolCall),
		PayloadJSON: payload,
	}); err != nil {
		log.Printf("[kanban] task.question.record.error root=%s task=%s session=%s tool=%s err=%v", rootID, taskID, sessionKey, callID, err)
	}
}

// markTaskQuestionAnswered 把会话侧的提问结果写回任务对话里的同一张卡。
// 空 answers 表示只收口（complete 先到/轮次被杀）；非空才是用户真实答案。
func (s *AppContext) markTaskQuestionAnswered(ctx context.Context, rootID, sessionKey, toolUseID string, answers map[string]string) {
	if s == nil {
		return
	}
	rootID = strings.TrimSpace(rootID)
	sessionKey = strings.TrimSpace(sessionKey)
	toolUseID = strings.TrimSpace(toolUseID)
	if rootID == "" || sessionKey == "" || toolUseID == "" {
		return
	}
	kanbanService, err := s.GetKanbanService()
	if err != nil {
		return
	}
	activityService, err := kanbanService.ActivityServiceForRoot(rootID)
	if err != nil {
		return
	}
	_, changed, err := activityService.MarkTaskQuestionAnswered(ctx, sessionKey, toolUseID, answers)
	if err != nil {
		log.Printf("[kanban] task.question.answer.error root=%s session=%s tool=%s err=%v", rootID, sessionKey, toolUseID, err)
		return
	}
	// W4-1：把「这一轮答过提问」标记给回合结算，随后的 turn_settled 会带
	// answered_question=true。空答案（只收口）不算作答。
	if changed && len(answers) > 0 && s.Kanban != nil {
		s.Kanban.NoteSessionAnsweredQuestion(rootID, sessionKey)
	}
}

// closeTaskQuestions 收口某会话里所有仍等待回答的提问卡。回合终结与提问工具
// 失败都会走这里；harness 不发 tool update 时，回合终结是唯一兜底。
func (s *AppContext) closeTaskQuestions(ctx context.Context, rootID, sessionKey, reason string) {
	if s == nil {
		return
	}
	rootID = strings.TrimSpace(rootID)
	sessionKey = strings.TrimSpace(sessionKey)
	if rootID == "" || sessionKey == "" {
		return
	}
	// 普通聊天没有 task 归属，不值得为它打开任务库。回合终结会对每个会话调用，
	// 这一步保证只有任务会话才产生额外查询。
	manager, err := s.GetSessionManager(rootID)
	if err != nil {
		return
	}
	sess, err := manager.Get(ctx, sessionKey, 0)
	if err != nil || sess == nil || sess.Type == session.TypeRouter || strings.TrimSpace(sess.TaskID) == "" {
		return
	}
	kanbanService, err := s.GetKanbanService()
	if err != nil {
		return
	}
	activityService, err := kanbanService.ActivityServiceForRoot(rootID)
	if err != nil {
		return
	}
	closed, err := activityService.CloseOpenTaskQuestions(ctx, sessionKey, reason)
	if err != nil {
		log.Printf("[kanban] task.question.close.error root=%s session=%s err=%v", rootID, sessionKey, err)
		return
	}
	if closed > 0 {
		// 卡被回合终结/工具失败收口时，任务卡上的「等你回答」徽标也要退场：
		// 它由 aux 标志驱动，不随提问卡状态变化。清位同时会广播 task.updated。
		no := false
		if _, err := kanbanService.UpdateTaskAuxFlags(ctx, rootID, sess.TaskID,
			kanban.TaskAuxFlagsPatch{AskUserWaiting: &no}, "aux_ask_user_closed"); err != nil {
			log.Printf("[kanban] task.question.clear_flag.error root=%s task=%s err=%v", rootID, sess.TaskID, err)
		}
	}
}

// taskQuestionPayload 只保留前端渲染需要的字段。questions 优先用适配器已解析
// 的结构；没有时保留原始 input 字符串，前端 getAskUserQuestions 会自己解析。
func taskQuestionPayload(toolCall agenttypes.ToolCall, agent string) (string, error) {
	payload := map[string]any{"tool_use_id": toolCall.CallID}
	if strings.TrimSpace(agent) != "" {
		payload["agent"] = strings.TrimSpace(agent)
	}
	if title := strings.TrimSpace(toolCall.Title); title != "" {
		payload["title"] = title
	}
	if toolCall.Meta != nil {
		if questions, ok := toolCall.Meta["questions"]; ok && questions != nil {
			payload["questions"] = questions
		}
		if input, ok := toolCall.Meta["input"].(string); ok && strings.TrimSpace(input) != "" {
			payload["input"] = input
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
