package acp

import (
	"context"
	"strings"
	"sync"
	"time"
)

type activePromptState struct {
	mu     sync.Mutex
	id     int64
	cancel context.CancelFunc
}

func (p *Process) RecentStderrHint() (string, bool) {
	return p.recentStderrHint()
}

func (p *Process) captureStderrHint(line string) {
	if p == nil {
		return
	}
	p.stderrHint.mu.Lock()
	defer p.stderrHint.mu.Unlock()

	if strings.Contains(line, `"code":`) {
		p.stderrHint.expectMessage = true
		return
	}
	if !p.stderrHint.expectMessage {
		return
	}
	message, ok := parseStderrHintMessage(line)
	if !ok {
		return
	}
	p.setRecentStderrHintLocked(message)
	p.cancelActivePrompt()
}

func (p *Process) setRecentStderrHintLocked(message string) {
	p.stderrHint.message = message
	p.stderrHint.messageAt = time.Now()
	p.stderrHint.expectMessage = false
}

func parseStderrHintMessage(line string) (string, bool) {
	match := stderrMessagePattern.FindStringSubmatch(line)
	if len(match) < 2 {
		return "", false
	}
	return strings.TrimSpace(match[1]), true
}

func (p *Process) recentStderrHint() (string, bool) {
	if p == nil {
		return "", false
	}
	p.stderrHint.mu.Lock()
	defer p.stderrHint.mu.Unlock()
	if strings.TrimSpace(p.stderrHint.message) == "" {
		return "", false
	}
	if time.Since(p.stderrHint.messageAt) > 5*time.Minute {
		return "", false
	}
	return p.stderrHint.message, true
}

func (p *Process) setActivePrompt(id int64, cancel context.CancelFunc) {
	if p == nil {
		return
	}
	p.activePrompt.mu.Lock()
	p.activePrompt.id = id
	p.activePrompt.cancel = cancel
	p.activePrompt.mu.Unlock()
}

func (p *Process) clearActivePrompt(id int64) {
	if p == nil {
		return
	}
	p.activePrompt.mu.Lock()
	if p.activePrompt.id == id {
		p.activePrompt.id = 0
		p.activePrompt.cancel = nil
	}
	p.activePrompt.mu.Unlock()
}

func (p *Process) cancelActivePrompt() {
	if p == nil {
		return
	}
	p.activePrompt.mu.Lock()
	cancel := p.activePrompt.cancel
	p.activePrompt.id = 0
	p.activePrompt.cancel = nil
	p.activePrompt.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
