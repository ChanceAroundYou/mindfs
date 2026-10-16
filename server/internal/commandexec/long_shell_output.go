package commandexec

import (
	"strconv"
	"strings"
	"time"
)

func (r *longShellCommand) consume(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.finished {
		return
	}
	r.pending += string(chunk)
	for {
		if !r.startedOutput {
			idx := findMarkerLine(r.pending, r.startMarker)
			if idx < 0 {
				r.pending = keepTail(r.pending, len(r.startMarker))
				return
			}
			r.pending = r.pending[idx+len(r.startMarker):]
			r.pending = trimLeadingLineBreak(r.pending)
			r.startedOutput = true
		}
		endIdx, codeText, ok := findEndMarkerWithExitCode(r.pending, r.endPrefix)
		if endIdx >= 0 {
			before := r.stripShellControlEchoLocked(r.pending[:endIdx])
			if before != "" {
				r.emitLocked([]byte(before))
			}
			if !ok {
				return
			}
			exitCode, _ := strconv.Atoi(codeText)
			r.pending = ""
			r.finishLocked(Result{
				Shell:      r.session.shell,
				ExitCode:   exitCode,
				Duration:   time.Since(r.started),
				StartedAt:  r.started,
				FinishedAt: time.Now().UTC(),
			})
			return
		}
		keep := len(r.endPrefix) + 16
		if len(r.pending) <= keep {
			return
		}
		emit, pending := splitCompleteLinesForEmit(r.pending, keep)
		if emit == "" {
			return
		}
		r.pending = pending
		emit = r.stripShellControlEchoLocked(emit)
		if emit != "" {
			r.emitLocked([]byte(emit))
		}
	}
}

func (r *longShellCommand) emitLocked(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	select {
	case r.out <- append([]byte(nil), chunk...):
	default:
		r.out <- append([]byte(nil), chunk...)
	}
}

func (r *longShellCommand) finish(result Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finishLocked(result)
}

func (r *longShellCommand) finishLocked(result Result) {
	if r.finished {
		return
	}
	r.finished = true
	if result.StartedAt.IsZero() {
		result.StartedAt = r.started
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = time.Now().UTC()
	}
	if result.Duration == 0 {
		result.Duration = result.FinishedAt.Sub(result.StartedAt)
	}
	r.session.clearCurrent(r)
	close(r.out)
	r.done <- result
	close(r.done)
}

func (r *longShellCommand) doneAlready() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.finished
}

func keepTail(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[len(value)-max:]
}

func splitCompleteLinesForEmit(value string, keep int) (string, string) {
	if keep < 0 {
		keep = 0
	}
	if idx := strings.LastIndexAny(value, "\r\n"); idx >= 0 {
		return value[:idx+1], value[idx+1:]
	}
	cut := len(value) - keep
	if cut <= 0 || len(value) <= maxPendingLineBytes {
		return "", value
	}
	return value[:cut], value[cut:]
}

func trimLeadingLineBreak(value string) string {
	return strings.TrimLeft(value, "\r\n")
}

func (r *longShellCommand) stripShellControlEchoLocked(value string) string {
	if value == "" {
		return value
	}
	lines := splitLinesKeepingBreaks(value)
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		hasLineBreak := strings.HasSuffix(line, "\n")
		if isPowerShellControlEchoLine(line) {
			if !hasLineBreak {
				r.suppressEcho = true
			}
			continue
		}
		if r.suppressEcho {
			if hasLineBreak {
				r.suppressEcho = false
			}
			continue
		}
		if idx := shellControlEchoIndex(line); idx >= 0 {
			if idx > 0 {
				prefix := line[:idx]
				if !looksLikeShellPromptPrefix(prefix) {
					kept = append(kept, prefix)
				}
			}
			if !hasLineBreak {
				r.suppressEcho = true
			}
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "")
}

func isPowerShellControlEchoLine(line string) bool {
	text := strings.TrimSpace(strings.TrimRight(line, "\r\n"))
	if text == "" {
		return false
	}
	if strings.Contains(text, "[Console]::OutputEncoding") ||
		strings.Contains(text, "[Console]::InputEncoding") ||
		strings.Contains(text, "$__mindfs_status") ||
		strings.Contains(text, "$global:LASTEXITCODE") {
		return true
	}
	prompt := strings.LastIndex(text, ">")
	if prompt < 0 {
		return false
	}
	afterPrompt := strings.TrimSpace(text[prompt+1:])
	if afterPrompt == "" {
		return false
	}
	commandFragment := afterPrompt
	if cut := strings.IndexAny(commandFragment, " \t('\""); cut >= 0 {
		commandFragment = commandFragment[:cut]
	}
	return len(commandFragment) >= len("Write-") && strings.HasPrefix("Write-Output", commandFragment)
}

func looksLikeShellPromptPrefix(prefix string) bool {
	trimmed := strings.TrimSpace(prefix)
	return strings.HasSuffix(trimmed, ">") || strings.HasSuffix(trimmed, "$") || strings.HasSuffix(trimmed, "%")
}

func splitLinesKeepingBreaks(value string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(value); i++ {
		if value[i] != '\n' {
			continue
		}
		lines = append(lines, value[start:i+1])
		start = i + 1
	}
	if start < len(value) {
		lines = append(lines, value[start:])
	}
	return lines
}

func isShellControlEcho(line string) bool {
	return shellControlEchoIndex(line) >= 0
}

func shellControlEchoIndex(line string) int {
	text := strings.TrimSpace(strings.TrimRight(line, "\r\n"))
	if text == "" {
		return -1
	}
	tokens := []string{
		"__MINDFS_CMD_START_",
		"__MINDFS_CMD_END_",
		"__mindfs_status",
		"$global:LASTEXITCODE",
		"[Console]::OutputEncoding",
		"[Console]::InputEncoding",
	}
	best := -1
	for _, token := range tokens {
		if idx := strings.Index(line, token); idx >= 0 && (best < 0 || idx < best) {
			best = idx
		}
	}
	return best
}

func takeExitCode(value string) (string, bool) {
	value = strings.TrimLeft(value, " \t")
	end := strings.IndexAny(value, "\r\n")
	if end < 0 {
		return "", false
	}
	code := strings.TrimSpace(value[:end])
	if code == "" {
		return "", false
	}
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return "", false
		}
	}
	return code, true
}

func findMarkerLine(value, marker string) int {
	for offset := 0; offset < len(value); {
		idx := strings.Index(value[offset:], marker)
		if idx < 0 {
			return -1
		}
		idx += offset
		after := idx + len(marker)
		if after < len(value) && (value[after] == '\r' || value[after] == '\n') {
			return idx
		}
		offset = after
	}
	return -1
}

func findMarkerLinePrefix(value, marker string) int {
	return strings.Index(value, marker)
}

func findEndMarkerWithExitCode(value, marker string) (int, string, bool) {
	for offset := 0; offset < len(value); {
		idx := strings.Index(value[offset:], marker)
		if idx < 0 {
			return -1, "", false
		}
		idx += offset
		code, ok := takeExitCode(value[idx+len(marker):])
		if ok {
			return idx, code, true
		}
		offset = idx + len(marker)
	}
	return -1, "", false
}
