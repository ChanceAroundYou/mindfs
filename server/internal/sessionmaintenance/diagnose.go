package sessionmaintenance

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Diagnose 只读取转录与锁目录，不修改任何文件。
func Diagnose(entries []Entry, opts Options) Report {
	report := Report{Scanned: len(entries)}
	for _, entry := range entries {
		adapter := ForHarness(entry.Agent)
		if adapter == nil {
			report.Sessions = append(report.Sessions, SessionReport{
				SessionKey:  entry.SessionKey,
				Agent:       entry.Agent,
				RuntimePath: entry.RuntimePath,
				Unsupported: "当前 harness 不支持会话维护",
			})
			continue
		}
		paths, err := adapter.TranscriptPaths(entry, opts)
		if err != nil {
			report.Sessions = append(report.Sessions, SessionReport{
				SessionKey:  entry.SessionKey,
				Agent:       entry.Agent,
				RuntimePath: entry.RuntimePath,
				Error:       err.Error(),
			})
			continue
		}
		for _, path := range paths {
			item := SessionReport{
				SessionKey:     entry.SessionKey,
				Agent:          entry.Agent,
				AgentSessionID: entry.AgentSessionID,
				Path:           path,
				RuntimePath:    entry.RuntimePath,
			}
			if item.RuntimePath != "" {
				if _, err := os.Stat(item.RuntimePath); err != nil && os.IsNotExist(err) {
					item.RuntimeMissing = true
				}
			}
			bytes, lines, images := inspectTranscript(path)
			item.Bytes, item.Lines, item.ImageBytes = bytes, lines, images
			if !adapter.SupportsImages() && images > 0 {
				item.Unsupported = "当前 harness 暂不支持安全改写图片块"
			}
			report.Sessions = append(report.Sessions, item)
		}
	}
	locksDir := ""
	for _, entry := range entries {
		if adapter := ForHarness(entry.Agent); adapter != nil {
			locksDir = adapter.LocksDir(opts)
			if locksDir != "" {
				break
			}
		}
	}
	if locksDir != "" {
		report.StaleLocks = findStaleLocks(locksDir)
	}
	return report
}

func inspectTranscript(path string) (int64, int, int64) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, 0
	}
	f, err := os.Open(path)
	if err != nil {
		return info.Size(), 0, 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	var lines int
	var images int64
	for scanner.Scan() {
		lines++
		line := scanner.Bytes()
		if bytesContainImageBlock(line) {
			images += int64(len(line))
		}
	}
	return info.Size(), lines, images
}

func bytesContainImageBlock(line []byte) bool {
	return strings.Contains(string(line), `"type":"image"`) ||
		strings.Contains(string(line), `"type": "image"`) ||
		strings.Contains(string(line), `"type":"input_image"`) ||
		strings.Contains(string(line), `"type": "input_image"`)
}

func findStaleLocks(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil || pid <= 0 || pidAlive(pid) {
			continue
		}
		out = append(out, filepath.Join(dir, entry.Name()))
	}
	return out
}

// CleanLocks 只删除 PID 已死的锁。任何 stat/删除错误都留在 Failed，不静默成功。
func CleanLocks(dir string) LockResult {
	var result LockResult
	for _, path := range findStaleLocks(dir) {
		if err := os.Remove(path); err != nil {
			result.Failed = append(result.Failed, path)
			continue
		}
		result.Removed = append(result.Removed, path)
	}
	return result
}
