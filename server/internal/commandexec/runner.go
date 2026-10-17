package commandexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Options struct {
	Command      string
	Cwd          string
	Env          []string
	Shells       []ShellSpec
	Shell        string
	RootID       string
	Session      string
	TerminalCols int
}

type ShellSpec struct {
	Command       string
	Args          []string
	LongShellArgs []string
	CommandPrefix string
}

type Result struct {
	Shell      string
	ExitCode   int
	Duration   time.Duration
	StartedAt  time.Time
	FinishedAt time.Time
	Err        error
}

const (
	defaultTerminalCols = 120
	defaultTerminalRows = 24
	minTerminalCols     = 40
	maxTerminalCols     = 500
)

func terminalColsOrDefault(cols int) uint16 {
	if cols <= 0 {
		return defaultTerminalCols
	}
	if cols < minTerminalCols {
		cols = minTerminalCols
	}
	if cols > maxTerminalCols {
		cols = maxTerminalCols
	}
	return uint16(cols)
}

func terminalRowsOrDefault(rows int) uint16 {
	if rows <= 0 {
		rows = defaultTerminalRows
	}
	if rows < 4 {
		rows = 4
	}
	if rows > 60 {
		rows = 60
	}
	return uint16(rows)
}

type Process interface {
	Output() <-chan []byte
	WriteInput([]byte) (int, error)
	Resize(cols, rows int) error
	Interrupt() error
	Terminate() error
	KillTree() error
	Wait() Result
}

func Start(ctx context.Context, opts Options) (Process, error) {
	command := strings.TrimSpace(opts.Command)
	if command == "" {
		return nil, errors.New("command required")
	}
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
	if runtime.GOOS != "windows" {
		shell, args, err := shellCommand(command, opts.Shells, opts.Shell)
		if err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, shell, args...)
		cmd.Dir = absCwd
		cmd.Env = commandEnv(opts.Env)
		return startPlatformProcess(ctx, cmd, shell, opts.TerminalCols)
	}
	shell, args := interactiveShellCommand(spec)
	if shell == "" {
		return nil, errors.New("no configured shell found")
	}
	if shouldUseWindowsPipeFallback(shell) {
		shell, args, err := shellCommand(command, opts.Shells, opts.Shell)
		if err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, shell, args...)
		cmd.Dir = absCwd
		cmd.Env = commandEnv(opts.Env)
		return startPlatformProcess(ctx, cmd, shell, opts.TerminalCols)
	}
	script := interactiveShellScript(spec, command)
	if strings.TrimSpace(script) == "" {
		return nil, errors.New("command required")
	}
	cmd := exec.CommandContext(ctx, shell, args...)
	cmd.Dir = absCwd
	cmd.Env = commandEnv(opts.Env)
	proc, err := startPlatformProcess(ctx, cmd, shell, opts.TerminalCols)
	if err != nil {
		shell, args, shellErr := shellCommand(command, opts.Shells, opts.Shell)
		if shellErr != nil {
			return nil, shellErr
		}
		cmd := exec.CommandContext(ctx, shell, args...)
		cmd.Dir = absCwd
		cmd.Env = commandEnv(opts.Env)
		return startPlatformProcess(ctx, cmd, shell, opts.TerminalCols)
	}
	go func() {
		_, _ = proc.WriteInput([]byte(script))
	}()
	return proc, nil
}
