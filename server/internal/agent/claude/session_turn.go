package claude

import (
	"context"
	"errors"
	"log"
	"strings"

	claudeagent "github.com/roasbeef/claude-agent-sdk-go"
)

func (s *session) SendMessage(ctx context.Context, content string) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	if s.stream == nil {
		return errors.New("claude session not initialized")
	}
	turnCtx, turnID := s.turn.Begin(ctx)
	defer s.turn.End(turnID)
	s.sawMessageText = false
	log.Printf("[agent/claude] input session=%s content=%q", s.sessionKey, preview(content))

	waiter := make(chan error, 1)
	s.enqueueTurn(waiter)
	if err := s.stream.Send(turnCtx, content); err != nil {
		s.dequeueTurn(waiter)
		log.Printf("[agent/claude] send.error session=%s err=%v", s.sessionKey, err)
		return err
	}

	select {
	case err := <-waiter:
		if err != nil && !isTurnCanceledError(err) {
			log.Printf("[agent/claude] send.error session=%s err=%v", s.sessionKey, err)
		}
		return err
	case <-turnCtx.Done():
		s.dequeueTurn(waiter)
		log.Printf("[agent/claude] send.error session=%s err=%v", s.sessionKey, turnCtx.Err())
		return turnCtx.Err()
	}
}

func (s *session) enqueueTurn(waiter chan error) {
	s.turnMu.Lock()
	s.turns = append(s.turns, waiter)
	s.turnMu.Unlock()
}

func (s *session) markTurnInterrupted() {
	s.turnMu.Lock()
	s.turnInterrupted = true
	s.turnMu.Unlock()
}

func (s *session) consumeTurnInterruptedLocked() bool {
	interrupted := s.turnInterrupted
	s.turnInterrupted = false
	return interrupted
}

func (s *session) dequeueTurn(waiter chan error) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	for i, ch := range s.turns {
		if ch != waiter {
			continue
		}
		s.turns = append(s.turns[:i], s.turns[i+1:]...)
		if len(s.turns) == 0 {
			s.turnInterrupted = false
		}
		return
	}
}

func (s *session) completeTurn(err error) {
	s.turnMu.Lock()
	interrupted := s.consumeTurnInterruptedLocked()
	if len(s.turns) == 0 {
		s.turnMu.Unlock()
		return
	}
	waiter := s.turns[0]
	s.turns = s.turns[1:]
	s.turnMu.Unlock()

	if interrupted && err != nil {
		err = errors.New("turn canceled")
	}
	waiter <- err
}

func (s *session) failPendingTurns(err error) {
	s.turnMu.Lock()
	pending := s.turns
	s.turns = nil
	s.turnInterrupted = false
	s.turnMu.Unlock()
	for _, ch := range pending {
		ch <- err
	}
}

func resultErr(msg claudeagent.ResultMessage) error {
	status := strings.ToLower(strings.TrimSpace(msg.Status))
	subtype := strings.ToLower(strings.TrimSpace(msg.Subtype))
	if !msg.IsError && (status == "success" || subtype == "success") && !strings.HasPrefix(subtype, "error") {
		return nil
	}
	if len(msg.Errors) > 0 {
		return errors.New(strings.Join(msg.Errors, "; "))
	}
	if strings.TrimSpace(msg.Result) != "" && (msg.IsError || strings.EqualFold(msg.Status, "error")) {
		return errors.New(msg.Result)
	}
	if strings.TrimSpace(msg.Subtype) != "" {
		return errors.New("claude result: " + msg.Subtype)
	}
	return errors.New("claude turn failed")
}

// isTurnBoundaryResult 判定一条 ResultMessage 是否代表「人类发起的那一轮结束」。
//
// SDK 只在**非人类来源**的 result 上填 Origin；缺省（nil）即人类轮 —— 本机实测
// 1084 条真回合 result 全部不带 origin 字段，19 条非边界 result 全部带
// origin.kind="task-notification"。白名单而非黑名单：MessageOriginKind 有 6 个值
// （human / channel / peer / task-notification / coordinator / auto-continuation），
// 黑名单只挡今天碰巧出现的那一个，下一种 kind 出现时症状原样复发。
//
// 未知 kind 一律按「非边界」处理：宁可让那一轮继续跑（它仍在 activeTurns 里，
// 停止按钮可用），也不要提前广播 done —— 提前 done 会让 agent 在跑的同时灯灭、
// 停止失效、并把后续输出落到已经关闭的回合上。
func isTurnBoundaryResult(msg claudeagent.ResultMessage) bool {
	if msg.Origin == nil {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(string(msg.Origin.Kind)), string(claudeagent.MessageOriginKindHuman))
}

func isTurnCanceledError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return value == "turn canceled" || value == "turn cancelled" || strings.Contains(value, "context canceled")
}
