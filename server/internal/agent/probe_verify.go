package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/agent/claude"
	agenttypes "mindfs/server/internal/agent/types"
)

// VerifySessionInteraction sends a deterministic ping prompt and verifies the response contains the token.
func VerifySessionInteraction(ctx context.Context, sess agenttypes.Session) error {
	if sess == nil {
		return errors.New("session required")
	}

	var (
		mu      sync.Mutex
		text    strings.Builder
		gotDone bool
		doneCh  = make(chan struct{}, 1)
	)

	sess.OnUpdate(func(ev agenttypes.Event) {
		switch ev.Type {
		case agenttypes.EventTypeMessageChunk:
			if chunk, ok := ev.Data.(agenttypes.MessageChunk); ok {
				mu.Lock()
				text.WriteString(chunk.Content)
				mu.Unlock()
			}
		case agenttypes.EventTypeMessageDone:
			mu.Lock()
			gotDone = true
			mu.Unlock()
			select {
			case doneCh <- struct{}{}:
			default:
			}
		}
	})

	if err := sess.SendMessage(ctx, "hello"); err != nil {
		return err
	}

	select {
	case <-doneCh:
	case <-ctx.Done():
		return fmt.Errorf("wait done: %w", ctx.Err())
	}

	mu.Lock()
	defer mu.Unlock()
	if !gotDone {
		return errors.New("done event not received")
	}
	gotText := strings.TrimSpace(text.String())
	if gotText == "" {
		return errors.New("response was empty")
	}
	return nil
}

// SanitizeDefaultModelID 修正过期默认模型：不在当前模型目录内（含 [1m]/家族等价）时
// 回退到目录中第一个非 default 条目。cc-switch 切换 provider 后，旧默认模型（of/os 等）
// 已不可通，继续使用只会让新建会话直接 400。
func SanitizeDefaultModelID(status Status) Status {
	fallback := firstSelectableModelID(status.Models)
	if fallback == "" {
		// 目录不可得（探活未完成），保持现状。
		return status
	}
	id := strings.TrimSpace(status.DefaultModelID)
	if id != "" && modelInStatusModels(status.Models, id) {
		return status
	}
	status.DefaultModelID = fallback
	return status
}

func modelInStatusModels(models []agenttypes.ModelInfo, id string) bool {
	key := claude.TierKey(claude.Strip1MSuffix(id))
	for _, model := range models {
		if claude.TierKey(claude.Strip1MSuffix(model.ID)) == key || strings.EqualFold(strings.TrimSpace(model.ID), strings.TrimSpace(id)) {
			return true
		}
	}
	return false
}

func firstSelectableModelID(models []agenttypes.ModelInfo) string {
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" || strings.EqualFold(id, "default") {
			continue
		}
		return id
	}
	return ""
}

// probeChangedConfigAgents 检测外部改写（cc-switch）的 ~/.claude/settings.json mtime，
// 变化则对 claude 触发一次 recovery 探测，无需重启 MindFS 即可刷新模型目录。
func (p *Prober) probeChangedConfigAgents() {
	if p == nil || p.cfg == nil {
		return
	}
	for _, def := range p.cfg.Agents {
		if agentDefinitionProtocol(def.Name, def) != ProtocolClaudeSDK {
			continue
		}
		path, err := claude.ClaudeSettingsPath()
		if err != nil {
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			continue
		}
		p.mu.Lock()
		prev := p.claudeSettingsMtimes[def.Name]
		p.claudeSettingsMtimes[def.Name] = info.ModTime()
		p.mu.Unlock()
		if prev.IsZero() || !info.ModTime().After(prev) {
			continue
		}
		log.Printf("[agent/probe] claude_settings.changed agent=%s mtime=%s action=reprobe", def.Name, info.ModTime().Format(time.RFC3339))
		status := safeProbeConfiguredAgentWithPool(context.Background(), def.Name, def, p.pool, p.probeSessions, probePhaseRecovery)
		p.setStatus(status)
	}
}
