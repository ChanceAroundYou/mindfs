package commandexec

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func (s *longShellSession) run(_ context.Context, command string) (*longShellCommand, error) {
	run := &longShellCommand{
		session:     s,
		id:          strconv.FormatInt(time.Now().UnixNano(), 36),
		started:     time.Now().UTC(),
		out:         make(chan []byte, 32),
		done:        make(chan Result, 1),
		startMarker: "__MINDFS_CMD_START__",
	}
	run.startMarker = "__MINDFS_CMD_START_" + run.id + "__"
	run.endPrefix = "__MINDFS_CMD_END_" + run.id + "__:"

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("shell exited")
	}
	if s.current != nil {
		s.mu.Unlock()
		return nil, errors.New("command already running in this session")
	}
	s.current = run
	s.mu.Unlock()

	if _, err := s.proc.WriteInput([]byte(shellRunScript(s.shell, command, run.startMarker, run.endPrefix))); err != nil {
		s.clearCurrent(run)
		return nil, err
	}
	return run, nil
}

func shellRunScript(shell, command, startMarker, endPrefix string) string {
	base := strings.ToLower(filepath.Base(shell))
	if runtime.GOOS == "windows" {
		switch base {
		case "powershell.exe", "powershell", "pwsh.exe", "pwsh":
			return fmt.Sprintf("Write-Output %s\n%s\n$__mindfs_status = if ($global:LASTEXITCODE -ne $null) { $global:LASTEXITCODE } elseif ($?) { 0 } else { 1 }\n$global:LASTEXITCODE = $null\nWrite-Output (%s + $__mindfs_status)\n", powershellQuote(startMarker), command, powershellQuote(endPrefix))
		case "cmd.exe", "cmd":
			return fmt.Sprintf("echo %s\r\n%s\r\necho %s%%ERRORLEVEL%%\r\n", startMarker, command, endPrefix)
		}
	}
	if base == "fish" {
		return fmt.Sprintf("command printf '\\n%s\\n'\neval %s </dev/null\nset -l __mindfs_status $status\ncommand printf '\\n%s%%s\\n' $__mindfs_status\n", shellQuote(startMarker), shellQuote(command), shellQuote(endPrefix))
	}
	return fmt.Sprintf("command printf '\\n%%s\\n' %s\neval %s </dev/null\n__mindfs_status=$?\ncommand printf '\\n%%s%%s\\n' %s \"$__mindfs_status\"\n", shellQuote(startMarker), shellQuote(command), shellQuote(endPrefix))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func powershellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func (s *longShellSession) readLoop() {
	for chunk := range s.proc.Output() {
		s.mu.Lock()
		current := s.current
		s.mu.Unlock()
		if current == nil {
			continue
		}
		current.consume(chunk)
	}
	s.markClosed()
}

func (s *longShellSession) waitLoop() {
	result := s.proc.Wait()
	s.markClosed()
	defaultLongShells.remove(s.key, s)
	if result.Err != nil {
		return
	}
}

func (m *longShellManager) remove(key string, sess *longShellSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[key] == sess {
		delete(m.sessions, key)
	}
}

func (s *longShellSession) clearCurrent(run *longShellCommand) {
	s.mu.Lock()
	if s.current == run {
		s.current = nil
	}
	s.mu.Unlock()
}

func (s *longShellSession) killShell() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	if s.proc == nil {
		return nil
	}
	return s.proc.KillTree()
}

func (r *longShellCommand) Output() <-chan []byte {
	return r.out
}

func (r *longShellCommand) WriteInput(input []byte) (int, error) {
	if r == nil || r.session == nil || r.session.proc == nil {
		return 0, nil
	}
	return r.session.proc.WriteInput(input)
}

func (r *longShellCommand) Resize(cols, rows int) error {
	if r == nil || r.session == nil || r.session.proc == nil {
		return nil
	}
	return r.session.proc.Resize(cols, rows)
}

func (r *longShellCommand) Interrupt() error {
	if r == nil || r.session == nil || r.session.proc == nil {
		return nil
	}
	_, err := r.session.proc.WriteInput([]byte{0x03})
	return err
}

func (r *longShellCommand) Terminate() error {
	if r == nil || r.doneAlready() {
		return nil
	}
	if r.session == nil {
		return nil
	}
	return r.session.killShell()
}

func (r *longShellCommand) KillTree() error {
	return r.Terminate()
}

func (r *longShellCommand) Wait() Result {
	if r == nil {
		return Result{ExitCode: -1}
	}
	return <-r.done
}
