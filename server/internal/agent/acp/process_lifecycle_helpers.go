package acp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"

	gopsprocess "github.com/shirou/gopsutil/v4/process"
)

func processDiagnostic(pid int) string {
	parts := make([]string, 0, 3)
	proc, err := gopsprocess.NewProcess(int32(pid))
	if err != nil {
		parts = append(parts, "state=not_found")
	} else {
		if statuses, statusErr := proc.Status(); statusErr == nil && len(statuses) > 0 {
			parts = append(parts, "state="+strings.Join(statuses, ","))
		} else if statusErr != nil {
			parts = append(parts, "state_error="+strconv.Quote(statusErr.Error()))
		}
		if ppid, ppidErr := proc.Ppid(); ppidErr == nil {
			parts = append(parts, fmt.Sprintf("ppid=%d", ppid))
		}
	}
	parts = append(parts, platformProcessDiagnostic(pid))
	return strings.Join(parts, " ")
}

func streamProcessStderr(proc *Process, reader io.Reader) {
	if reader == nil {
		return
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		log.Printf("[agent/acp][stderr] agent=%s %s", proc.agentLabel(), line)
		proc.captureStderrHint(line)
	}
	if err := scanner.Err(); err != nil && !isExpectedStreamCloseError(err) {
		log.Printf("[agent/acp][stderr] agent=%s stream_error=%v", proc.agentLabel(), err)
	}
}

func isExpectedStreamCloseError(err error) bool {
	return err == nil || errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe)
}

func configureProcessCommand(cmd *exec.Cmd, env map[string]string) {
	if cmd == nil {
		return
	}
	configurePlatformProcessCommand(cmd)
	if len(env) == 0 {
		return
	}
	cmd.Env = cmd.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
}
