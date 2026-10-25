package usecase

import (
	"context"
	"errors"
	"sort"
	"strings"

	"mindfs/server/internal/session"
)

// archivedMode 把「只列归档」这个 bool 翻成 manager 的枚举值。
// 零值 "" 即「排除归档」，也就是主面板的老行为。
func archivedMode(only bool) string {
	if only {
		return "only"
	}
	return ""
}

func (s *Service) ListSessions(ctx context.Context, in ListSessionsInput) (ListSessionsOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return ListSessionsOutput{}, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return ListSessionsOutput{}, err
	}
	items, err := manager.List(ctx, session.ListOptions{
		BeforeTime:   in.BeforeTime,
		AfterTime:    in.AfterTime,
		TopLevelOnly: in.TopLevelOnly,
		Limit:        in.Limit,
		ArchivedMode: archivedMode(in.ArchivedOnly),
	})
	if err != nil {
		return ListSessionsOutput{}, err
	}
	totalCount, err := manager.Count(ctx, session.ListOptions{
		BeforeTime:   in.BeforeTime,
		AfterTime:    in.AfterTime,
		TopLevelOnly: in.TopLevelOnly,
		ArchivedMode: archivedMode(in.ArchivedOnly),
	})
	if err != nil {
		return ListSessionsOutput{}, err
	}
	if in.IncludeChildren {
		items, err = appendChildSessions(ctx, manager, items)
		if err != nil {
			return ListSessionsOutput{}, err
		}
	}
	if err := fillCommandShells(ctx, manager, items); err != nil {
		return ListSessionsOutput{}, err
	}
	return ListSessionsOutput{Sessions: items, TotalCount: totalCount}, nil
}

func (s *Service) ListMultiRootSessions(ctx context.Context, in ListMultiRootSessionsInput) (ListMultiRootSessionsOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return ListMultiRootSessionsOutput{}, err
	}
	limit := in.LimitPerRoot
	if limit <= 0 {
		limit = 6
	}
	groups := make([]SessionRootGroup, 0)
	for _, root := range s.Registry.ListRoots() {
		manager, err := s.Registry.GetSessionManager(root.ID)
		if err != nil {
			return ListMultiRootSessionsOutput{}, err
		}
		totalCount, err := manager.Count(ctx, session.ListOptions{TopLevelOnly: true})
		if err != nil {
			return ListMultiRootSessionsOutput{}, err
		}
		if totalCount <= 0 {
			continue
		}
		items, err := manager.List(ctx, session.ListOptions{TopLevelOnly: true, Limit: limit})
		if err != nil {
			return ListMultiRootSessionsOutput{}, err
		}
		items, err = appendChildSessions(ctx, manager, items)
		if err != nil {
			return ListMultiRootSessionsOutput{}, err
		}
		if err := fillCommandShells(ctx, manager, items); err != nil {
			return ListMultiRootSessionsOutput{}, err
		}
		latest, err := manager.LatestSessionUpdatedAt(ctx)
		if err != nil {
			return ListMultiRootSessionsOutput{}, err
		}
		groups = append(groups, SessionRootGroup{
			RootID:            root.ID,
			RootName:          root.EffectiveName(),
			LatestSessionTime: latest,
			Sessions:          items,
			TotalCount:        totalCount,
		})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].LatestSessionTime.After(groups[j].LatestSessionTime)
	})
	return ListMultiRootSessionsOutput{Groups: groups}, nil
}

func pinnedSessionKeys(items []*session.Session) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil || strings.TrimSpace(item.Key) == "" {
			continue
		}
		keys = append(keys, item.Key)
	}
	return keys
}

func (s *Service) ListChildSessions(ctx context.Context, in ListChildSessionsInput) (ListSessionsOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return ListSessionsOutput{}, err
	}
	parentKey := strings.TrimSpace(in.ParentSessionKey)
	if parentKey == "" {
		return ListSessionsOutput{}, errors.New("parent_session_key required")
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return ListSessionsOutput{}, err
	}
	items, err := manager.List(ctx, session.ListOptions{
		BeforeTime:       in.BeforeTime,
		ParentSessionKey: parentKey,
		Limit:            in.Limit,
	})
	if err != nil {
		return ListSessionsOutput{}, err
	}
	items = dropEmptyBashSubSessions(ctx, manager, items)
	if err := fillCommandShells(ctx, manager, items); err != nil {
		return ListSessionsOutput{}, err
	}
	return ListSessionsOutput{Sessions: items}, nil
}

// fillCommandShells 给命令会话补 shell 标签。
// 走 manager.GetCommandShell（aux 尾部有界读）而不是 GetExchangeAux(key, 0)：
// 后者是全量读，列表页对每个命令会话各读一次，实测单会话 aux 最大 19MB 且持 m.mu。
func fillCommandShells(ctx context.Context, manager *session.Manager, items []*session.Session) error {
	for _, item := range items {
		if item == nil || item.Type != session.TypeCommand {
			continue
		}
		shell, err := manager.GetCommandShell(ctx, item.Key)
		if err != nil {
			return err
		}
		item.Shell = shell
	}
	return nil
}

func appendChildSessions(ctx context.Context, manager *session.Manager, parents []*session.Session) ([]*session.Session, error) {
	if len(parents) == 0 {
		return parents, nil
	}
	out := make([]*session.Session, 0, len(parents))
	for _, parent := range parents {
		if parent == nil {
			continue
		}
		out = append(out, parent)
		children, err := manager.List(ctx, session.ListOptions{ParentSessionKey: parent.Key})
		if err != nil {
			return nil, err
		}
		children = dropEmptyBashSubSessions(ctx, manager, children)
		out = append(out, children...)
	}
	return out, nil
}

func (s *Service) SearchSessions(ctx context.Context, in SearchSessionsInput) (SearchSessionsOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return SearchSessionsOutput{}, err
	}
	if in.MultiRoot {
		return s.searchMultiRootSessions(ctx, in)
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return SearchSessionsOutput{}, err
	}
	items, err := manager.Search(ctx, session.SearchOptions{
		Query: in.Query,
		Limit: in.Limit,
	})
	if err != nil {
		return SearchSessionsOutput{}, err
	}
	return SearchSessionsOutput{Items: mapSessionSearchHits(in.RootID, items)}, nil
}

func (s *Service) searchMultiRootSessions(ctx context.Context, in SearchSessionsInput) (SearchSessionsOutput, error) {
	limit := normalizeSessionSearchLimit(in.Limit)
	items := make([]SessionSearchHit, 0, limit)
	for _, root := range s.Registry.ListRoots() {
		manager, err := s.Registry.GetSessionManager(root.ID)
		if err != nil {
			return SearchSessionsOutput{}, err
		}
		hits, err := manager.Search(ctx, session.SearchOptions{
			Query: in.Query,
			Limit: limit,
		})
		if err != nil {
			return SearchSessionsOutput{}, err
		}
		items = append(items, mapSessionSearchHits(root.ID, hits)...)
	}
	sortSessionSearchHits(items)
	if len(items) > limit {
		items = items[:limit]
	}
	return SearchSessionsOutput{Items: items}, nil
}

func mapSessionSearchHits(rootID string, hits []session.SearchHit) []SessionSearchHit {
	out := make([]SessionSearchHit, 0, len(hits))
	for _, hit := range hits {
		out = append(out, SessionSearchHit{
			RootID:           strings.TrimSpace(rootID),
			Key:              hit.Key,
			Type:             hit.Type,
			ParentSessionKey: hit.ParentSessionKey,
			ParentToolCallID: hit.ParentToolCallID,
			Agent:            hit.Agent,
			Model:            hit.Model,
			Shell:            hit.Shell,
			Name:             hit.Name,
			CreatedAt:        hit.CreatedAt,
			UpdatedAt:        hit.UpdatedAt,
			ClosedAt:         hit.ClosedAt,
			MatchType:        hit.MatchType,
			MatchScore:       hit.MatchScore,
			Seq:              hit.Seq,
			Snippet:          hit.Snippet,
		})
	}
	return out
}

func normalizeSessionSearchLimit(limit int) int {
	switch {
	case limit <= 0:
		return 20
	case limit > 50:
		return 50
	default:
		return limit
	}
}

func sortSessionSearchHits(items []SessionSearchHit) {
	sort.SliceStable(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		if left.MatchType != right.MatchType {
			return sessionSearchMatchTypeRank(left.MatchType) < sessionSearchMatchTypeRank(right.MatchType)
		}
		if left.MatchScore != right.MatchScore {
			return left.MatchScore > right.MatchScore
		}
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		if left.RootID != right.RootID {
			return left.RootID < right.RootID
		}
		return left.Key < right.Key
	})
}

func sessionSearchMatchTypeRank(matchType string) int {
	switch strings.TrimSpace(matchType) {
	case "name":
		return 0
	case "user":
		return 1
	case "reply":
		return 2
	default:
		return 3
	}
}

type CreateSessionInput struct {
	RootID string
	Input  session.CreateInput
}

// dropEmptyBashSubSessions 丢弃「空壳 Bash 子会话」。
//
// 2026-10-25 之前的写入路径把 Bash 工具进度心跳（ToolProgressMessage）误当成
// 新子代理，每次心跳都建一个名为 "Bash" 的子会话；它只有一条空正文行和一条
// 无命令无输出的 toolcall aux，在侧栏里却和真子代理一样占一行。写入侧已在
// claudeSubagentRouter.Handle 修掉，这里负责让**存量**幽灵也不再出现。
//
// 只对名字是 Bash 的子会话做深检（读转录 + aux），其余子会话零成本放行；
// 有任何真实正文 / 用户输入 / 思考 / todo / plan / 带命令或输出的工具调用，
// 都判定为「有内容」，照常显示。
func dropEmptyBashSubSessions(ctx context.Context, manager *session.Manager, items []*session.Session) []*session.Session {
	if len(items) == 0 {
		return items
	}
	out := items[:0]
	for _, item := range items {
		if item == nil {
			continue
		}
		if !isBashNamedSubSession(item) || !isEmptyBashSubSession(ctx, manager, item.Key) {
			out = append(out, item)
		}
	}
	return out
}

func isBashNamedSubSession(item *session.Session) bool {
	if strings.TrimSpace(item.ParentSessionKey) == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(item.Name), "bash")
}

func isEmptyBashSubSession(ctx context.Context, manager *session.Manager, key string) bool {
	full, err := manager.Get(ctx, key, 0)
	if err != nil || full == nil {
		return false
	}
	for _, exchange := range full.Exchanges {
		if strings.TrimSpace(exchange.LegacyContent) != "" {
			return false
		}
		if exchange.Role == "user" {
			return false
		}
	}
	if blocks, err := manager.ReadBlocks(ctx, key); err == nil {
		for _, block := range blocks {
			if blockHasRenderableContent(block) {
				return false
			}
		}
	}
	aux, err := manager.GetExchangeAux(ctx, key, 0)
	if err != nil {
		return false
	}
	for _, entries := range aux {
		for _, entry := range entries {
			if strings.TrimSpace(entry.Thought) != "" || entry.Todo != nil || entry.Plan != nil || entry.Compact != nil {
				return false
			}
			toolCall := entry.ToolCall
			if toolCall == nil {
				continue
			}
			if len(toolCall.Content) > 0 || len(toolCall.Locations) > 0 {
				return false
			}
			if !strings.EqualFold(strings.TrimSpace(toolCall.Title), "bash") {
				return false
			}
			if strings.TrimSpace(stringMeta(toolCall.Meta, "command")) != "" ||
				strings.TrimSpace(stringMeta(toolCall.Meta, "input")) != "" ||
				strings.TrimSpace(stringMeta(toolCall.Meta, "output")) != "" {
				return false
			}
		}
	}
	return true
}

// blockHasRenderableContent 只认「用户看得见的东西」：正文 / 思考 / 工具调用的
// 命令与输出。空壳 Bash 子会话的块流里只有无内容的工具块，不算。
func blockHasRenderableContent(block session.Block) bool {
	if strings.TrimSpace(block.Text) != "" {
		return true
	}
	if block.ToolCall == nil {
		return block.Todo != nil || block.Plan != nil
	}
	if len(block.ToolCall.Content) > 0 || len(block.ToolCall.Locations) > 0 {
		return true
	}
	return strings.TrimSpace(stringMeta(block.ToolCall.Meta, "command")) != "" ||
		strings.TrimSpace(stringMeta(block.ToolCall.Meta, "input")) != "" ||
		strings.TrimSpace(stringMeta(block.ToolCall.Meta, "output")) != ""
}
