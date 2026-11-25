package sessionmaintenance

// Entry 是一条待诊断的会话。运行时路径为空表示会话没有被钉在具体目录上。
type Entry struct {
	SessionKey     string
	Agent          string
	AgentSessionID string
	RuntimePath    string
}

// SessionReport 是单会话的无损诊断结果。
type SessionReport struct {
	SessionKey     string `json:"session_key"`
	Agent          string `json:"agent"`
	AgentSessionID string `json:"agent_session_id,omitempty"`
	Path           string `json:"path,omitempty"`
	Bytes          int64  `json:"bytes"`
	Lines          int    `json:"lines"`
	ImageBytes     int64  `json:"image_bytes"`
	RuntimePath    string `json:"runtime_path,omitempty"`
	RuntimeMissing bool   `json:"runtime_missing"`
	Unsupported    string `json:"unsupported,omitempty"`
	Error          string `json:"error,omitempty"`
}

// Report 是跨会话汇总。清理锁不依赖具体会话，单列在 StaleLocks。
type Report struct {
	Sessions   []SessionReport `json:"sessions"`
	StaleLocks []string        `json:"stale_locks,omitempty"`
	Scanned    int             `json:"scanned"`
}

// CleanResult 是一次无损清理的结果。
type CleanResult struct {
	Path           string `json:"path"`
	RemovedImages  int    `json:"removed_images,omitempty"`
	ReclaimedBytes int64  `json:"reclaimed_bytes,omitempty"`
	BackupPath     string `json:"backup_path,omitempty"`
	Purged         bool   `json:"purged,omitempty"`
}

// LockResult 是陈旧锁清理结果。
type LockResult struct {
	Removed []string `json:"removed,omitempty"`
	Failed  []string `json:"failed,omitempty"`
}
