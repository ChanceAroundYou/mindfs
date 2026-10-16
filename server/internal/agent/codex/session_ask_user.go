package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	types "mindfs/server/internal/agent/types"

	codexsdk "github.com/fanwenlin/codex-go-sdk/codex"
)

type codexAskUserAnswerResult struct {
	answers map[string]string
	err     error
}

func (s *session) AnswerQuestion(ctx context.Context, answer types.AskUserAnswer) error {
	callID := strings.TrimSpace(answer.ToolUseID)
	if callID == "" {
		return errors.New("toolUseId required")
	}
	if len(answer.Answers) == 0 {
		return errors.New("answers required")
	}

	s.questionMu.Lock()
	waiter, ok := s.questionWaits[callID]
	s.questionMu.Unlock()
	if !ok {
		return errors.New("question is not pending: " + callID)
	}

	answers := make(map[string]string, len(answer.Answers))
	for key, value := range answer.Answers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			answers[key] = value
		}
	}
	if len(answers) == 0 {
		return errors.New("answers required")
	}

	select {
	case waiter <- codexAskUserAnswerResult{answers: answers}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) handleAskUserRequest(req codexsdk.AskUserRequest) (codexsdk.AskUserResponse, error) {
	callID := strings.TrimSpace(req.ItemID)
	if callID == "" {
		return codexsdk.AskUserResponse{}, errors.New("ask user question missing item id")
	}
	if len(req.Questions) == 0 {
		return codexsdk.AskUserResponse{}, errors.New("ask user question missing questions")
	}

	waiter := make(chan codexAskUserAnswerResult, 1)
	s.questionMu.Lock()
	if s.questionWaits == nil {
		s.questionWaits = make(map[string]chan codexAskUserAnswerResult)
	}
	if _, exists := s.questionWaits[callID]; exists {
		s.questionMu.Unlock()
		return codexsdk.AskUserResponse{}, errors.New("ask user question already pending: " + callID)
	}
	s.questionWaits[callID] = waiter
	s.questionMu.Unlock()
	defer func() {
		s.questionMu.Lock()
		delete(s.questionWaits, callID)
		s.questionMu.Unlock()
	}()

	toolCall := codexAskUserToolCall(callID, req)
	s.emit(types.Event{
		Type:      types.EventTypeToolCall,
		SessionID: s.SessionID(),
		Data:      toolCall,
	})

	result := <-waiter
	if result.err != nil {
		return codexsdk.AskUserResponse{}, result.err
	}
	response := codexAskUserResponse(req.Questions, result.answers)
	if len(response.Answers) == 0 {
		return codexsdk.AskUserResponse{}, errors.New("empty ask user answers")
	}
	return response, nil
}

func (s *session) cancelPendingQuestions(err error) {
	if err == nil {
		err = errors.New("turn canceled")
	}
	s.questionMu.Lock()
	waiters := make([]chan codexAskUserAnswerResult, 0, len(s.questionWaits))
	for callID, waiter := range s.questionWaits {
		waiters = append(waiters, waiter)
		delete(s.questionWaits, callID)
	}
	s.questionMu.Unlock()

	for _, waiter := range waiters {
		select {
		case waiter <- codexAskUserAnswerResult{err: err}:
		default:
		}
	}
}

func codexAskUserToolCall(callID string, req codexsdk.AskUserRequest) types.ToolCall {
	questions := make([]types.AskUserQuestionItem, 0, len(req.Questions))
	for _, question := range req.Questions {
		options := make([]types.AskUserQuestionOption, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, types.AskUserQuestionOption{
				Label:       option.Label,
				Description: option.Description,
			})
		}
		questions = append(questions, types.AskUserQuestionItem{
			Question: question.Question,
			Header:   question.Header,
			Options:  options,
		})
	}

	raw, _ := json.Marshal(map[string]any{"questions": questions})
	meta := map[string]any{
		"toolUseId": callID,
		"questions": questions,
		"rawType":   "request_user_input",
		"input":     strings.TrimSpace(string(raw)),
	}
	if strings.TrimSpace(req.ThreadID) != "" {
		meta["threadId"] = strings.TrimSpace(req.ThreadID)
	}
	if strings.TrimSpace(req.TurnID) != "" {
		meta["turnId"] = strings.TrimSpace(req.TurnID)
	}
	return types.ToolCall{
		CallID:  callID,
		Title:   "ask user",
		Status:  "running",
		Kind:    types.ToolKindAskUser,
		RawType: "request_user_input",
		Meta:    meta,
	}
}

func codexAskUserResponse(questions []codexsdk.AskUserQuestion, answers map[string]string) codexsdk.AskUserResponse {
	response := codexsdk.AskUserResponse{Answers: make(map[string]codexsdk.AskUserAnswer)}
	for key, value := range answers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		questionID := codexQuestionIDForAnswerKey(key, questions)
		if questionID == "" {
			continue
		}
		response.Answers[questionID] = codexsdk.AskUserAnswer{
			Answers: []string{value},
		}
	}
	return response
}

func codexQuestionIDForAnswerKey(key string, questions []codexsdk.AskUserQuestion) string {
	if strings.HasPrefix(key, "q_") {
		indexText := strings.TrimPrefix(key, "q_")
		for index, question := range questions {
			if indexText == strconv.Itoa(index) {
				return strings.TrimSpace(question.ID)
			}
		}
	}
	for _, question := range questions {
		if key == strings.TrimSpace(question.ID) {
			return key
		}
	}
	return ""
}
