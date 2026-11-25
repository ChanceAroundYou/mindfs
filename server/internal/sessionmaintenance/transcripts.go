package sessionmaintenance

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

type claudeAdapter struct{}

func (claudeAdapter) Harness() string { return "claude" }
func (claudeAdapter) LocksDir(opts Options) string {
	if strings.TrimSpace(opts.ClaudeLocksDir) != "" {
		return opts.ClaudeLocksDir
	}
	return defaultClaudeLocksDir()
}
func (claudeAdapter) SupportsImages() bool { return true }
func (claudeAdapter) SupportsPurge() bool  { return true }

func (claudeAdapter) TranscriptPaths(entry Entry, opts Options) ([]string, error) {
	projects := strings.TrimSpace(opts.ClaudeProjectsDir)
	if projects == "" {
		projects = defaultClaudeProjectsDir()
	}
	if projects == "" {
		return nil, nil
	}
	dirs, err := os.ReadDir(projects)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, 2)
	if id := strings.TrimSpace(entry.AgentSessionID); id != "" {
		ids = append(ids, id+".jsonl")
	}
	if key := strings.TrimSpace(entry.SessionKey); key != "" {
		ids = append(ids, key+".jsonl")
	}
	seen := make(map[string]bool)
	var out []string
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		for _, name := range ids {
			path := filepath.Join(projects, dir.Name(), name)
			if seen[path] {
				continue
			}
			seen[path] = true
			if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
				out = append(out, path)
			}
		}
	}
	return out, nil
}

type codexAdapter struct{}

func (codexAdapter) Harness() string { return "codex" }
func (codexAdapter) LocksDir(Options) string {
	return ""
}
func (codexAdapter) SupportsImages() bool { return false }
func (codexAdapter) SupportsPurge() bool  { return false }

func (codexAdapter) TranscriptPaths(entry Entry, opts Options) ([]string, error) {
	root := strings.TrimSpace(opts.CodexSessionsDir)
	if root == "" {
		root = defaultCodexSessionsDir()
	}
	if root == "" {
		return nil, nil
	}
	needles := make([][]byte, 0, 2)
	if id := strings.TrimSpace(entry.AgentSessionID); id != "" {
		needles = append(needles, []byte(id))
	}
	if key := strings.TrimSpace(entry.SessionKey); key != "" {
		needles = append(needles, []byte(key))
	}
	if len(needles) == 0 {
		return nil, nil
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		buf := make([]byte, 32*1024)
		n, _ := f.Read(buf)
		for _, needle := range needles {
			if bytes.Contains(buf[:n], needle) {
				out = append(out, path)
				return nil
			}
		}
		return nil
	})
	return out, err
}
