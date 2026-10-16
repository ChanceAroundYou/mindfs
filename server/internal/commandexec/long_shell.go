package commandexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type longShellManager struct {
	mu       sync.Mutex
	sessions map[string]*longShellSession
}

type longShellSession struct {
	key   string
	shell string
	proc  Process

	mu      sync.Mutex
	current *longShellCommand
	closed  bool
}

type longShellCommand struct {
	session *longShellSession
	id      string
	started time.Time
	out     chan []byte
	done    chan Result

	startMarker string
	endPrefix   string

	mu            sync.Mutex
	startedOutput bool
	pending       string
	finished      bool
	suppressEcho  bool
}

var defaultLongShells = &longShellManager{sessions: make(map[string]*longShellSession)}

const maxPendingLineBytes = 64 * 1024

func StartInSession(ctx context.Context, opts Options) (Process, error) {
	rootID := strings.TrimSpace(opts.RootID)
	sessionKey := strings.TrimSpace(opts.Session)
	if rootID == "" || sessionKey == "" {
		return Start(ctx, opts)
	}
	return defaultLongShells.start(ctx, longShellKey(rootID, sessionKey, opts.Shell), opts)
}

func CloseSession(rootID, sessionKey string) {
	rootID = strings.TrimSpace(rootID)
	sessionKey = strings.TrimSpace(sessionKey)
	if rootID == "" || sessionKey == "" {
		return
	}
	defaultLongShells.closeSession(rootID, sessionKey)
}

func longShellKey(rootID, sessionKey, shell string) string {
	return strings.TrimSpace(rootID) + "::" + strings.TrimSpace(sessionKey) + "::" + strings.TrimSpace(shell)
}

func longShellSessionPrefix(rootID, sessionKey string) string {
	return strings.TrimSpace(rootID) + "::" + strings.TrimSpace(sessionKey) + "::"
}

func (m *longShellManager) start(ctx context.Context, key string, opts Options) (Process, error) {
	command := strings.TrimSpace(opts.Command)
	if command == "" {
		return nil, errors.New("command required")
	}
	sess, err := m.getOrCreate(ctx, key, opts)
	if err != nil {
		return nil, err
	}
	return sess.run(ctx, command)
}

func (m *longShellManager) closeSession(rootID, sessionKey string) {
	prefix := longShellSessionPrefix(rootID, sessionKey)
	var sessions []*longShellSession
	m.mu.Lock()
	for key, sess := range m.sessions {
		if strings.HasPrefix(key, prefix) {
			sessions = append(sessions, sess)
			delete(m.sessions, key)
		}
	}
	m.mu.Unlock()
	for _, sess := range sessions {
		_ = sess.killShell()
	}
}

func (m *longShellManager) getOrCreate(ctx context.Context, key string, opts Options) (*longShellSession, error) {
	m.mu.Lock()
	if existing := m.sessions[key]; existing != nil && !existing.isClosed() {
		m.mu.Unlock()
		if err := existing.resize(opts.TerminalCols); err == nil && opts.TerminalCols > 0 {
			time.Sleep(25 * time.Millisecond)
		}
		return existing, nil
	}
	delete(m.sessions, key)
	m.mu.Unlock()

	sess, err := newLongShellSession(ctx, key, opts)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if existing := m.sessions[key]; existing != nil && !existing.isClosed() {
		m.mu.Unlock()
		_ = sess.killShell()
		if err := existing.resize(opts.TerminalCols); err == nil && opts.TerminalCols > 0 {
			time.Sleep(25 * time.Millisecond)
		}
		return existing, nil
	}
	m.sessions[key] = sess
	m.mu.Unlock()
	return sess, nil
}

func newLongShellSession(_ context.Context, key string, opts Options) (*longShellSession, error) {
	cwd := strings.TrimSpace(opts.Cwd)
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, err
	}
	spec, ok := ResolveConfiguredShell(opts.Shells, opts.Shell)
	if !ok {
		if len(normalizeShells(opts.Shells)) > 0 {
			return nil, errors.New("no configured shell found")
		}
		spec = ShellSpec{Command: DefaultShell()}
	}
	shell, args := longShellProcessCommand(spec)
	if strings.TrimSpace(shell) == "" {
		return nil, errors.New("no configured shell found")
	}
	cmd := exec.CommandContext(context.Background(), shell, args...)
	cmd.Dir = absCwd
	cmd.Env = longShellEnv(opts.Env, shell)
	proc, err := startLongShellPlatformProcess(context.Background(), cmd, shell, opts.TerminalCols)
	if err != nil {
		return nil, err
	}
	sess := &longShellSession{
		key:   key,
		shell: shell,
		proc:  proc,
	}
	go sess.readLoop()
	go sess.waitLoop()
	if bootstrap := longShellBootstrap(shell); bootstrap != "" {
		_, _ = proc.WriteInput([]byte(bootstrap))
	}
	time.Sleep(100 * time.Millisecond)
	return sess, nil
}

func longShellEnv(extra []string, shell string) []string {
	env := commandEnv(extra)
	if runtime.GOOS == "windows" {
		return env
	}
	switch strings.ToLower(filepath.Base(shell)) {
	case "bash", "zsh", "sh":
		env = append(env, "HISTFILE=/dev/null", "HISTSIZE=0", "HISTFILESIZE=0", "SAVEHIST=0")
	case "fish":
		env = append(env, "fish_history=")
	}
	return env
}

func longShellBootstrap(shell string) string {
	return longShellBootstrapForOS(shell, runtime.GOOS)
}

func longShellBootstrapForOS(shell, goos string) string {
	base := strings.ToLower(filepath.Base(shell))
	if goos == "windows" {
		switch base {
		case "powershell.exe", "powershell", "pwsh.exe", "pwsh":
			return "[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false); [Console]::InputEncoding = [Console]::OutputEncoding; $OutputEncoding = [Console]::OutputEncoding\n" +
				"if (Get-Command Set-PSReadLineOption -ErrorAction SilentlyContinue) { Set-PSReadLineOption -HistorySaveStyle SaveNothing -ErrorAction SilentlyContinue }\n" +
				"Clear-History -ErrorAction SilentlyContinue\n"
		case "cmd.exe", "cmd":
			return "prompt $S\r\nchcp 65001 >NUL\r\n"
		}
		return ""
	}
	switch base {
	case "fish":
		return "set -e fish_history 2>/dev/null\nset -g fish_history '' 2>/dev/null\nfunction fish_prompt; end\nfunction fish_right_prompt; end\nstty -echo 2>/dev/null\n"
	case "zsh":
		return "unset HISTFILE\nHISTSIZE=0\nSAVEHIST=0\nsetopt HIST_NO_STORE 2>/dev/null || true\nunsetopt INC_APPEND_HISTORY INC_APPEND_HISTORY_TIME SHARE_HISTORY APPEND_HISTORY EXTENDED_HISTORY 2>/dev/null || true\nunsetopt zle prompt_cr prompt_sp 2>/dev/null || true\nPROMPT=''\nRPROMPT=''\nPS1=''\nPROMPT_COMMAND=''\nstty -echo 2>/dev/null || true\n"
	case "bash":
		return "HISTFILE=/dev/null\nexport HISTFILE\nHISTSIZE=0\nHISTFILESIZE=0\nset +o history 2>/dev/null || true\nhistory -c 2>/dev/null || true\nPROMPT=''\nRPROMPT=''\nPS1=''\nPROMPT_COMMAND=''\nstty -echo 2>/dev/null || true\n"
	default:
		return "HISTFILE=/dev/null\nexport HISTFILE\nHISTSIZE=0\nHISTFILESIZE=0\nset +o history 2>/dev/null || true\nunsetopt zle prompt_cr prompt_sp 2>/dev/null || true\nPROMPT=''\nRPROMPT=''\nPS1=''\nPROMPT_COMMAND=''\nstty -echo 2>/dev/null || true\n"
	}
}

func longShellProcessCommand(spec ShellSpec) (string, []string) {
	shell := strings.TrimSpace(spec.Command)
	if spec.LongShellArgs != nil {
		return shell, append([]string(nil), spec.LongShellArgs...)
	}
	if runtime.GOOS == "windows" {
		return interactiveShellCommand(spec)
	}
	return shell, nil
}

func (s *longShellSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *longShellSession) resize(cols int) error {
	if s == nil || s.proc == nil {
		return nil
	}
	return s.proc.Resize(cols, defaultTerminalRows)
}

func (s *longShellSession) markClosed() {
	s.mu.Lock()
	s.closed = true
	current := s.current
	s.current = nil
	s.mu.Unlock()
	if current != nil {
		current.finish(Result{
			Shell:      s.shell,
			ExitCode:   -1,
			StartedAt:  current.started,
			FinishedAt: time.Now().UTC(),
			Err:        errors.New("shell exited"),
		})
	}
}
