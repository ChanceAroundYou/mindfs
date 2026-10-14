package usecase

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

func (s *Service) GetSession(ctx context.Context, in GetSessionInput) (*session.Session, *session.SessionWindowMeta, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, nil, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, nil, err
	}
	if in.BeforeSeq > 0 || in.Latest > 0 {
		return manager.GetWindow(ctx, in.Key, in.BeforeSeq, in.Limit, in.Latest)
	}
	out, err := manager.Get(ctx, in.Key, in.Seq)
	return out, nil, err
}

type GetSessionExchangeAuxInput struct {
	RootID string
	Key    string
	Seq    int
}

func (s *Service) GetSessionExchangeAux(ctx context.Context, in GetSessionExchangeAuxInput) (map[int][]session.ExchangeAux, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, err
	}
	return manager.GetExchangeAux(ctx, in.Key, in.Seq)
}

// GetSessionExchangeAuxWindow 只取窗口内 seq 的 aux。窗口请求原先经 GetSessionExchangeAux(Seq=0)
// 全量读取整个 aux 文件再按窗口过滤（实测某会话 11.8MB/~300ms），纯属浪费；此处按窗口 seqSet
// 走尾部读（见 Manager.readAuxWindowTail）。
func (s *Service) GetSessionExchangeAuxWindow(ctx context.Context, in GetSessionExchangeAuxWindowInput) (map[int][]session.ExchangeAux, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, err
	}
	return manager.GetExchangeAuxWindow(ctx, in.Key, in.Seqs)
}

type GetSessionExchangeAuxWindowInput struct {
	RootID string
	Key    string
	Seqs   map[int]bool
}

// GetSessionBlocksWindow 取窗口内 seq 的**正文块**（CUSTOM(G-BH)，方案 §5/§10 S2b）。
//
// 与 GetSessionExchangeAuxWindow 同形：按窗口 seqSet 走尾部读，绝不整份读。
// 块流是正文的唯一载体（D1），所以这条路径是全站最热端点的载荷来源，必须 O(窗口)。
// 历史会话没有块流 → 返回空 map，前端回退旧格式（content + aux），不是错误。
func (s *Service) GetSessionBlocksWindow(ctx context.Context, in GetSessionBlocksWindowInput) (map[int][]session.Block, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, err
	}
	blocks, err := manager.GetBlocksWindow(ctx, in.Key, in.Seqs)
	if err != nil {
		return nil, err
	}
	// S2d-1 回填：窗口里没有块的轮次，用**调用方本来就传进来的** exchanges/aux
	// 编译一次（幂等）。必须在 GetBlocksWindow 之后、返回之前：回填完的那一批块
	// 要出现在同一个回包里，否则用户看到的是「先空一屏、再自己冒出来」。
	backfillTurnsIfNeeded(ctx, manager, in.Key, in.Exchanges, in.Aux, in.Seqs)
	if len(missingBlockSeqs(blocks, in.Seqs)) > 0 {
		if refilled, err := manager.GetBlocksWindow(ctx, in.Key, in.Seqs); err == nil {
			blocks = refilled
		}
	}
	return session.GroupBlocksBySeq(blocks), nil
}

type GetSessionBlocksWindowInput struct {
	RootID string
	Key    string
	Seqs   map[int]bool
	// Exchanges / Aux 是窗口响应**本来就带着**的两份数据。它们只作为回填输入
	// （S2d-1）：已回填的会话一次都不会用到，而再读一遍会把全站最重的端点
	// （1.7s 一次轮询，占 70% 流量）拖慢 1.7s 一倍的 IO。
	Exchanges []session.Exchange
	Aux       map[int][]session.ExchangeAux
}

type GetSessionToolCallInput struct {
	RootID string
	Key    string
	CallID string
}

func (s *Service) GetSessionToolCall(ctx context.Context, in GetSessionToolCallInput) (*agenttypes.ToolCall, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, err
	}
	return manager.GetFullToolCall(ctx, in.Key, in.CallID)
}

type GetSessionContextWindowInput struct {
	RootID string
	Key    string
}

func (s *Service) GetSessionContextWindow(ctx context.Context, in GetSessionContextWindowInput) (agenttypes.ContextWindow, error) {
	if err := s.ensureRegistry(); err != nil {
		return agenttypes.ContextWindow{}, err
	}
	if strings.TrimSpace(in.Key) == "" {
		return agenttypes.ContextWindow{}, errors.New("session key required")
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return agenttypes.ContextWindow{}, err
	}
	current, err := manager.Get(ctx, in.Key, 0)
	if err != nil {
		return agenttypes.ContextWindow{}, err
	}
	pool := s.Registry.GetAgentPool()
	if pool == nil {
		return current.LastContextWindow, nil
	}
	agentName := strings.TrimSpace(session.InferAgentFromSession(current))
	if agentName == "" {
		return current.LastContextWindow, nil
	}
	sess, ok := pool.Get(agentPoolSessionKey(in.Key, agentName))
	if !ok || sess == nil {
		return current.LastContextWindow, nil
	}
	contextWindow, err := sess.ContextWindow(ctx)
	if err != nil {
		return agenttypes.ContextWindow{}, err
	}
	if contextWindow.TotalTokens <= 0 || contextWindow.ModelContextWindow <= 0 {
		return current.LastContextWindow, nil
	}
	return contextWindow, nil
}

type GetSessionRelatedFilesInput struct {
	RootID string
	Key    string
}

func (s *Service) GetSessionRelatedFiles(ctx context.Context, in GetSessionRelatedFilesInput) ([]session.RelatedFile, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, err
	}
	current, err := manager.Get(ctx, in.Key, 0)
	if err != nil {
		return nil, err
	}
	return normalizeSessionRelatedFiles(ctx, manager.Root(), current.RelatedFiles), nil
}

func normalizeSessionRelatedFiles(ctx context.Context, root fs.RootInfo, files []session.RelatedFile) []session.RelatedFile {
	next := append([]session.RelatedFile(nil), files...)
	rootPath := strings.TrimSpace(root.RootPath)
	if rootPath == "" {
		return next
	}
	for i := range next {
		file := &next[i]
		if !strings.HasPrefix(filepath.ToSlash(strings.TrimSpace(file.Path)), ".worktree/task-") {
			continue
		}
		repoPath := strings.TrimSpace(file.RepoPath)
		if repoPath != "" && !sameManagedDirPath(repoPath, rootPath) {
			continue
		}
		resolvedRepoPath, resolvedPath, resolvedHead, ok := resolveTaskWorktreeRelatedFile(ctx, rootPath, file.Path)
		if !ok {
			continue
		}
		file.RootID = strings.TrimSpace(file.RootID)
		file.RepoKind = "git"
		file.RepoPath = resolvedRepoPath
		file.RepoName = filepath.Base(resolvedRepoPath)
		file.Path = resolvedPath
		if file.Head == "" {
			file.Head = resolvedHead
		}
	}
	return next
}

type RemoveSessionRelatedFileInput struct {
	RootID   string
	Key      string
	Path     string
	Head     string
	RepoPath string
	RepoKind string
}

func (s *Service) RemoveSessionRelatedFile(ctx context.Context, in RemoveSessionRelatedFileInput) error {
	if err := s.ensureRegistry(); err != nil {
		return err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return err
	}
	if err := manager.RemoveRelatedFileAtHead(ctx, in.Key, in.Path, in.Head, in.RepoPath, in.RepoKind); err != nil {
		return err
	}
	root := manager.Root()
	if legacyPath, ok := legacyTaskWorktreeRelatedPath(root.RootPath, in.RepoPath, in.Path); ok {
		return manager.RemoveRelatedFileAtHead(ctx, in.Key, legacyPath, in.Head, root.RootPath, in.RepoKind)
	}
	return nil
}

func legacyTaskWorktreeRelatedPath(rootPath, repoPath, path string) (string, bool) {
	rootPath = strings.TrimSpace(rootPath)
	repoPath = strings.TrimSpace(repoPath)
	path = strings.TrimSpace(path)
	if rootPath == "" || repoPath == "" || path == "" || sameManagedDirPath(rootPath, repoPath) {
		return "", false
	}
	absPath := filepath.Clean(filepath.Join(repoPath, filepath.FromSlash(filepath.ToSlash(path))))
	rel, err := filepath.Rel(filepath.Clean(rootPath), absPath)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, ".worktree/task-") {
		return "", false
	}
	return rel, true
}

type CloseSessionInput struct {
	RootID string
	Key    string
}
