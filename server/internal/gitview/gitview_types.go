package gitview

import (
	"mindfs/server/internal/fs"
)

type StatusItem struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status"`
	Staged    bool   `json:"staged,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	IsDir     bool   `json:"is_dir,omitempty"`
}

type StatusResult struct {
	Available  bool         `json:"available"`
	Branch     string       `json:"branch,omitempty"`
	DirtyCount int          `json:"dirty_count"`
	Items      []StatusItem `json:"items"`
}

type BranchItem struct {
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

type BranchListResult struct {
	Current  string       `json:"current,omitempty"`
	Branches []BranchItem `json:"branches"`
}

type WorktreeItem struct {
	Path    string `json:"path"`
	Branch  string `json:"branch,omitempty"`
	Head    string `json:"head,omitempty"`
	Current bool   `json:"current"`
}

type WorktreeListResult struct {
	Items []WorktreeItem `json:"items"`
}

type RepositoryInfo struct {
	Path string `json:"path"`
	Head string `json:"head,omitempty"`
}

type DiffResult struct {
	Path      string             `json:"path"`
	OldPath   string             `json:"old_path,omitempty"`
	Status    string             `json:"status"`
	Additions int                `json:"additions"`
	Deletions int                `json:"deletions"`
	Content   string             `json:"content"`
	FileMeta  []fs.FileMetaEntry `json:"file_meta,omitempty"`
}

type RelatedFileDiffResult struct {
	DiffResult
	BaseHead   string `json:"base_head,omitempty"`
	TargetHead string `json:"target_head,omitempty"`
	Source     string `json:"source,omitempty"`
}

// RelatedFileStatTarget 是一次批量统计请求里的单个文件。BaseHead 为空表示
// 「没有记录基线」，按工作区改动统计。
type RelatedFileStatTarget struct {
	Path     string `json:"path"`
	BaseHead string `json:"head,omitempty"`
}

// RelatedFileStat 是批量统计里单个文件的结果。
//
// Status 为空表示「自记录的基线以来没有任何变更可显示」—— 与
// emptyRelatedFileDiff 同一语义，前端据此不渲染 +N −M 徽标。
//
// 单个文件在这个接口里**不报错**：基线提交不在历史里、路径不在区间里，都只让它
// 自己留空，不牵连整批。逐文件调 ReadRelatedFileDiff 时那两种情况会返回错误、
// 前端 catch 成 null 不显示徽标 —— 观感一致。
type RelatedFileStat struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type HistoryItem struct {
	Hash       string `json:"hash"`
	Message    string `json:"message"`
	CommitTime string `json:"commit_time"`
	Remote     bool   `json:"remote"`
}

type HistoryResult struct {
	Available     bool          `json:"available"`
	Items         []HistoryItem `json:"items"`
	HasMore       bool          `json:"has_more"`
	CommitMissing bool          `json:"commit_missing,omitempty"`
	RemoteHead    string        `json:"remote_head,omitempty"`
}

type CommitFilesResult struct {
	Commit string       `json:"commit"`
	Items  []StatusItem `json:"items"`
}

type ActionResult struct {
	Output string       `json:"output"`
	Status StatusResult `json:"status"`
}
