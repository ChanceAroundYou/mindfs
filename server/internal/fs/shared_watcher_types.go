package fs

import (
	"context"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

var ignoredWatchNames = map[string]struct{}{
	".DS_Store":     {},
	".cache":        {},
	".git":          {},
	".gradle":       {},
	".mindfs":       {},
	".mypy_cache":   {},
	".next":         {},
	".nuxt":         {},
	".output":       {},
	".parcel-cache": {},
	".pytest_cache": {},
	".ruff_cache":   {},
	".svelte-kit":   {},
	".turbo":        {},
	".venv":         {},
	".vercel":       {},
	".vite":         {},
	"__pycache__":   {},
	"build":         {},
	"coverage":      {},
	"dist":          {},
	"node_modules":  {},
	"target":        {},
	"tmp":           {},
	"vendor":        {},
	"venv":          {},
}

// SharedFileWatcher manages file watching for one root shared by multiple sessions.
type SharedFileWatcher struct {
	root         RootInfo
	watcher      *fsnotify.Watcher
	sessionStore SessionFileRecorder

	mu                sync.RWMutex
	sessions          map[string]*sessionInfo
	watchedDirs       map[string]struct{}
	pendingWrites     map[string]string
	pendingChanges    map[string]FileChangeEvent
	pendingChangeDirs map[string]struct{}
	fileChangeTimer   *time.Timer
	fileChangeVersion uint64
	onFileChange      func(FileChangeEvent)
	onFileChangeBatch func(FileChangeBatchEvent)
	onRelatedFile     func(RelatedFileEvent)
	worktreeResolver  WorktreeResolver

	done chan struct{}
}

type SessionFileRecorder interface {
	RecordOutputFile(ctx context.Context, key, path string) error
	RecordOutputFileAtHead(ctx context.Context, key, path, head string) error
	RecordOutputFileInRepo(ctx context.Context, key, rootID, repoKind, repoPath, repoName, path, head string) error
	RecordRelatedWorktree(ctx context.Context, key, rootID, path, branch, head string) (bool, error)
}

type sessionInfo struct {
	key string
}

type FileChangeEvent struct {
	RootID string `json:"root_id"`
	Path   string `json:"path"`
	Op     string `json:"op"`
	IsDir  bool   `json:"is_dir"`
}

type FileChangeBatchEvent struct {
	RootID string            `json:"root_id"`
	Paths  []string          `json:"paths"`
	Dirs   []string          `json:"dirs"`
	Events []FileChangeEvent `json:"events"`
}

type RelatedFileEvent struct {
	RootID          string                `json:"root_id"`
	SessionKey      string                `json:"session_key"`
	Path            string                `json:"path"`
	RelatedWorktree *RelatedWorktreeMatch `json:"related_worktree,omitempty"`
}

type RelatedWorktreeMatch struct {
	Path    string
	Branch  string
	Head    string
	Current bool
}

type relatedFileRecord struct {
	rootID   string
	repoKind string
	repoPath string
	repoName string
	path     string
	head     string
}

type WorktreeResolver func(ctx context.Context, root RootInfo, filePath string) (RelatedWorktreeMatch, bool)
