package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func ensureStateDir() (string, error) {
	base, err := platformStateDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "mindfs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func servicePaths(stateDir, addr string) (string, string, error) {
	logDir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", "", err
	}
	key := sanitizeAddrForFile(addr)
	return filepath.Join(stateDir, "mindfs-"+key+".pid"), filepath.Join(logDir, "mindfs.log"), nil
}

func sanitizeAddrForFile(addr string) string {
	if strings.TrimSpace(addr) == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range addr {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}

func rotateLogIfNeeded(path string, maxSize int64, backups int) error {
	if strings.TrimSpace(path) == "" || maxSize <= 0 || backups < 1 {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Size() < maxSize {
		return nil
	}
	oldest := rotatedLogPath(path, backups)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := backups - 1; i >= 1; i-- {
		src := rotatedLogPath(path, i)
		dst := rotatedLogPath(path, i+1)
		if err := os.Rename(src, dst); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(path, rotatedLogPath(path, 1)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func rotatedLogPath(path string, index int) string {
	return fmt.Sprintf("%s.%d", path, index)
}

func writePIDFile(pidPath string) error {
	return writePIDValue(pidPath, os.Getpid())
}

func writePIDValue(pidPath string, pid int) error {
	return os.WriteFile(pidPath, []byte(strconv.Itoa(pid)+"\n"), 0o644)
}

func removePIDFile(pidPath string) {
	_ = os.Remove(pidPath)
}

func readPIDFile(pidPath string) (int, error) {
	raw, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0, err
	}
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pid in %s", pidPath)
	}
	return pid, nil
}
