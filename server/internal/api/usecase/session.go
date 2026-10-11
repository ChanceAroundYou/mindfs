package usecase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"mindfs/server/internal/agent"
	"mindfs/server/internal/agent/claude"
	agenttypes "mindfs/server/internal/agent/types"
	"mindfs/server/internal/commandexec"
	"mindfs/server/internal/fs"
	"mindfs/server/internal/session"
)

type ClientContext struct {
	CurrentRoot   string     `json:"current_root"`
	PluginCatalog string     `json:"plugin_catalog,omitempty"`
	Selection     *Selection `json:"selection,omitempty"`
}

type Selection struct {
	FilePath  string `json:"file_path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Text      string `json:"text,omitempty"`
}

type ListSessionsInput struct {
	RootID          string
	BeforeTime      time.Time
	AfterTime       time.Time
	Limit           int
	TopLevelOnly    bool
	IncludeChildren bool
	// ArchivedOnly = 只列已归档（归档区）。默认 false = 排除已归档（主面板）。
	ArchivedOnly bool
}

type ListSessionsOutput struct {
	Sessions []*session.Session
	// 置顶（pinned_items / pinned_keys）**不在这里**：权威是主节点的置顶表，
	// 由 api 层按项目取（http_pins.go 的 pinnedSessionsForRoot）。放这里就得让
	// 本机会话库再算一遍，等于把退役的 pinned_at 复活。
	TotalCount int
}

type ListMultiRootSessionsInput struct {
	LimitPerRoot int
}

type SessionRootGroup struct {
	RootID            string
	RootName          string
	LatestSessionTime time.Time
	Sessions          []*session.Session
	TotalCount        int
}

type ListMultiRootSessionsOutput struct {
	Groups []SessionRootGroup
}

type ListChildSessionsInput struct {
	RootID           string
	ParentSessionKey string
	BeforeTime       time.Time
	Limit            int
}

type SearchSessionsInput struct {
	RootID    string
	Query     string
	Limit     int
	MultiRoot bool
}

type SearchSessionsOutput struct {
	Items []SessionSearchHit
}

type SessionSearchHit struct {
	RootID           string     `json:"root_id,omitempty"`
	Key              string     `json:"key"`
	Type             string     `json:"type"`
	ParentSessionKey string     `json:"parent_session_key,omitempty"`
	ParentToolCallID string     `json:"parent_tool_call_id,omitempty"`
	Agent            string     `json:"agent,omitempty"`
	Model            string     `json:"model,omitempty"`
	Shell            string     `json:"shell,omitempty"`
	Name             string     `json:"name"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ClosedAt         *time.Time `json:"closed_at,omitempty"`
	MatchType        string     `json:"match_type"`
	MatchScore       int        `json:"match_score"`
	Seq              int        `json:"seq"`
	Snippet          string     `json:"snippet,omitempty"`
}

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
		latest := time.Time{}
		if len(items) > 0 && items[0] != nil {
			latest = items[0].UpdatedAt
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

func (s *Service) CreateSession(ctx context.Context, in CreateSessionInput) (*session.Session, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, err
	}
	return manager.Create(ctx, in.Input)
}

type ForkSessionInput struct {
	RootID string
	Key    string
	Seq    int
}

type ForkSessionOutput struct {
	Session *session.Session
}

func (s *Service) ForkSession(ctx context.Context, in ForkSessionInput) (ForkSessionOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return ForkSessionOutput{}, err
	}
	if strings.TrimSpace(in.Key) == "" {
		return ForkSessionOutput{}, errors.New("session key required")
	}
	if in.Seq <= 0 {
		return ForkSessionOutput{}, errors.New("seq required")
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return ForkSessionOutput{}, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return ForkSessionOutput{}, err
	}
	current, err := manager.Get(ctx, in.Key, 0)
	if err != nil {
		return ForkSessionOutput{}, err
	}
	target, agentTurnIndex, err := resolveForkTarget(current, in.Seq)
	if err != nil {
		return ForkSessionOutput{}, err
	}
	agentName := strings.TrimSpace(target.Agent)
	if agentName == "" {
		agentName = strings.TrimSpace(session.InferAgentFromSession(current))
	}
	if agentName == "" {
		return ForkSessionOutput{}, errors.New("agent not found for fork target")
	}
	binding, err := manager.FindAgentBinding(ctx, current.Key, agentName)
	if err != nil {
		return ForkSessionOutput{}, err
	}
	if binding == nil || strings.TrimSpace(binding.AgentSessionID) == "" {
		return ForkSessionOutput{}, errors.New("agent session binding not found")
	}
	pool := s.Registry.GetAgentPool()
	if pool == nil {
		return ForkSessionOutput{}, errors.New("agent pool not configured")
	}
	isACP := isACPAgent(pool, agentName)
	var forkPoint agenttypes.ResolveForkPointOutput
	if !isACP {
		importer, err := s.resolveExternalSessionImporter(agentName)
		if err != nil {
			return ForkSessionOutput{}, err
		}
		resolver, ok := importer.(agenttypes.ForkPointResolver)
		if !ok {
			return ForkSessionOutput{}, errors.New("agent does not support fork point resolution")
		}
		forkPoint, err = resolver.ResolveForkPointByAgentTurnIndex(ctx, agenttypes.ResolveForkPointInput{
			RootPath:       root.RootPath,
			AgentSessionID: binding.AgentSessionID,
			AgentTurnIndex: agentTurnIndex,
		})
		if err != nil {
			return ForkSessionOutput{}, err
		}
	}
	sourceJSON, err := json.Marshal(map[string]any{
		"type":             "fork",
		"session_key":      current.Key,
		"seq":              target.Seq,
		"agent":            agentName,
		"agent_session_id": binding.AgentSessionID,
	})
	if err != nil {
		return ForkSessionOutput{}, err
	}
	created, err := manager.Create(ctx, session.CreateInput{
		Type:     session.TypeChat,
		Source:   string(sourceJSON),
		Agent:    agentName,
		Model:    resolveForkModel(current, target),
		Name:     buildForkSessionName(current, target.Seq),
		PlanMode: current.PlanMode,
	})
	if err != nil {
		return ForkSessionOutput{}, err
	}
	copiedCount, err := copyForkHistory(ctx, manager, current, created, target.Seq, agentName)
	if err != nil {
		_ = manager.Delete(ctx, created.Key)
		return ForkSessionOutput{}, err
	}
	openCtx := pool.Context()
	if openCtx == nil {
		openCtx = ctx
	}
	agentCtxSeq := copiedCount
	if isACP {
		agentCtxSeq = 0
	}
	sess, err := pool.GetOrCreate(openCtx, agenttypes.OpenSessionInput{
		SessionKey:     agentPoolSessionKey(created.Key, agentName),
		AgentName:      agentName,
		Model:          resolveForkModel(current, target),
		Mode:           strings.TrimSpace(target.Mode),
		Effort:         strings.TrimSpace(target.Effort),
		FastService:    strings.TrimSpace(target.FastService),
		PlanMode:       current.PlanMode,
		RootPath:       root.RootPath,
		AgentSessionID: "",
		AgentCtxSeq:    agentCtxSeq,
		ForkPoint:      forkPoint,
	})
	if err != nil {
		_ = manager.Delete(ctx, created.Key)
		return ForkSessionOutput{}, err
	}
	agentSessionID := strings.TrimSpace(sess.SessionID())
	if agentSessionID == "" {
		_ = manager.Delete(ctx, created.Key)
		return ForkSessionOutput{}, errors.New("forked agent session id not found")
	}
	latest, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		_ = manager.Delete(ctx, created.Key)
		return ForkSessionOutput{}, err
	}
	if err := manager.UpdateAgentState(ctx, latest, agentName, agentCtxSeq, agentSessionID); err != nil {
		_ = manager.Delete(ctx, created.Key)
		return ForkSessionOutput{}, err
	}
	out, err := manager.Get(ctx, created.Key, 0)
	if err != nil {
		return ForkSessionOutput{}, err
	}
	log.Printf("[session/fork] done root=%s parent=%s child=%s agent=%s seq=%d turn=%d source_agent_session=%s fork_agent_session=%s", strings.TrimSpace(in.RootID), current.Key, out.Key, agentName, target.Seq, agentTurnIndex, binding.AgentSessionID, agentSessionID)
	return ForkSessionOutput{Session: out}, nil
}

func isACPAgent(pool *agent.Pool, agentName string) bool {
	if pool == nil {
		return false
	}
	def, ok := pool.Config().GetAgent(agentName)
	if !ok {
		return false
	}
	protocol := def.Protocol
	if protocol == "" {
		protocol = agent.DefaultProtocol(agentName)
	}
	return protocol == agent.ProtocolACP
}

func resolveForkTarget(current *session.Session, seq int) (session.Exchange, int, error) {
	if current == nil {
		return session.Exchange{}, 0, errors.New("session required")
	}
	var target session.Exchange
	found := false
	for _, exchange := range current.Exchanges {
		if exchange.Seq == seq {
			target = exchange
			found = true
			break
		}
	}
	if !found {
		return session.Exchange{}, 0, errors.New("message not found")
	}
	if !isAgentExchangeRole(target.Role) {
		return session.Exchange{}, 0, errors.New("fork target must be an agent message")
	}
	agentName := strings.TrimSpace(target.Agent)
	if agentName == "" {
		agentName = strings.TrimSpace(session.InferAgentFromSession(current))
	}
	turnIndex := 0
	for _, exchange := range current.Exchanges {
		if exchange.Seq > target.Seq {
			break
		}
		if !isAgentExchangeRole(exchange.Role) {
			continue
		}
		exchangeAgent := strings.TrimSpace(exchange.Agent)
		if agentName != "" && exchangeAgent != "" && exchangeAgent != agentName {
			continue
		}
		turnIndex++
	}
	if turnIndex <= 0 {
		return session.Exchange{}, 0, errors.New("agent turn not found")
	}
	return target, turnIndex, nil
}

func isAgentExchangeRole(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "agent", "assistant":
		return true
	default:
		return false
	}
}

func resolveForkModel(current *session.Session, target session.Exchange) string {
	if model := strings.TrimSpace(target.Model); model != "" {
		return model
	}
	if current == nil {
		return ""
	}
	return strings.TrimSpace(current.Model)
}

func buildForkSessionName(current *session.Session, seq int) string {
	base := "Fork"
	if current != nil && strings.TrimSpace(current.Name) != "" {
		base = strings.TrimSpace(current.Name)
	}
	if seq > 0 {
		name := fmt.Sprintf("%s#%d", base, seq)
		runes := []rune(name)
		if len(runes) > 80 {
			name = string(runes[:80])
		}
		return name
	}
	name := base
	runes := []rune(name)
	if len(runes) > 80 {
		name = string(runes[:80])
	}
	return name
}

func copyForkHistory(ctx context.Context, manager *session.Manager, from, to *session.Session, maxSeq int, fallbackAgent string) (int, error) {
	if manager == nil || from == nil || to == nil {
		return 0, errors.New("session required")
	}
	seqMap := make(map[int]int)
	copied := 0
	for _, exchange := range from.Exchanges {
		if exchange.Seq <= 0 || exchange.Seq > maxSeq {
			continue
		}
		agentName := strings.TrimSpace(exchange.Agent)
		if agentName == "" {
			agentName = strings.TrimSpace(fallbackAgent)
		}
		exchangeCtx := session.WithExchangeModelDisplayName(ctx, exchange.ModelDisplayName)
		exchangeCtx = session.WithExchangeTokenUsage(exchangeCtx, exchange.TokenUsage)
		exchangeCtx = session.WithExchangeSource(exchangeCtx, exchange.Source)
		// 复制轮次身份：turn_id 是 MindFS 权威身份，fork 复制的是**同一批轮次**，
		// 所以原样带过去；turn_seq 要重映射到 fork 的 seq 空间。
		// native_* **不带**：fork 开的是全新 agent 会话（AgentSessionID:"" + ForkPoint），
		// 源会话的 native id 对新转录一律无效（方案 §14 C1）。
		exchangeCtx = session.WithExchangeIdentity(exchangeCtx, session.ExchangeIdentity{
			TurnID:  exchange.TurnID,
			TurnSeq: seqMap[exchange.TurnSeq],
		})
		if err := manager.AddExchangeForAgentAt(exchangeCtx, to, exchange.Role, exchange.Content, agentName, exchange.Mode, exchange.Effort, exchange.FastService, exchange.Timestamp); err != nil {
			return copied, err
		}
		copied++
		seqMap[exchange.Seq] = copied
	}
	auxBySeq, err := manager.GetExchangeAux(ctx, from.Key, 0)
	if err != nil {
		return copied, err
	}
	for oldSeq, items := range auxBySeq {
		newSeq := seqMap[oldSeq]
		if newSeq <= 0 {
			continue
		}
		for _, aux := range items {
			next := aux
			next.Seq = newSeq
			if err := manager.AddExchangeAux(ctx, to.Key, next); err != nil {
				return copied, err
			}
		}
	}
	// 块流也要一起复制：块是**唯一正文载体**（D1），少了它 fork 会话在 S2b 之后是空的。
	// 与 aux 同样的重映射规则，外加清掉 native_id。
	if err := copyForkBlocks(ctx, manager, from.Key, to.Key, seqMap); err != nil {
		return copied, err
	}
	return copied, nil
}

// copyForkBlocks 把源会话的块复制到 fork，seq 按 seqMap 重映射、native_id 清空。
//
// revision 原样保留：源会话里同一 (seq,pos) 的最高 revision 就是真值，复制时没必要
// 重新编号 —— 重编号反而会让「复制过来的块」与「源会话的块」对不上。
func copyForkBlocks(ctx context.Context, manager *session.Manager, fromKey, toKey string, seqMap map[int]int) error {
	if len(seqMap) == 0 {
		return nil
	}
	blocks, err := manager.ReadBlocks(ctx, fromKey)
	if err != nil {
		return err
	}
	if len(blocks) == 0 {
		return nil
	}
	copied := make([]session.Block, 0, len(blocks))
	for _, block := range blocks {
		newSeq := seqMap[block.Seq]
		if newSeq <= 0 {
			continue
		}
		next := block
		next.Seq = newSeq
		next.NativeID = ""
		copied = append(copied, next)
	}
	if len(copied) == 0 {
		return nil
	}
	if err := manager.AppendBlocks(ctx, toKey, copied); err != nil {
		return err
	}
	return nil
}

type GetSessionInput struct {
	RootID    string
	Key       string
	Seq       int
	BeforeSeq int
	Limit     int
	Latest    int
}

// GetSession returns the session. When BeforeSeq>0 or Latest>0 it serves a
// windowed slice via manager.GetWindow and the returned SessionWindowMeta is
// non-nil; otherwise it serves a seq-incremental (Seq>0) or full (Seq<=0) view.
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

func (s *Service) CloseSession(ctx context.Context, in CloseSessionInput) (*session.Session, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, err
	}
	closed, err := manager.Close(ctx, in.Key)
	if err != nil {
		return nil, err
	}
	if pool := s.Registry.GetAgentPool(); pool != nil && closed != nil {
		for agentName := range closed.AgentCtxSeq {
			pool.Close(agentPoolSessionKey(closed.Key, agentName))
		}
	}
	s.Registry.ReleaseFileWatcher(in.RootID, in.Key)
	return closed, nil
}

type DeleteSessionInput struct {
	RootID string
	Key    string
}

// DeleteSession 删除会话**整棵子树**，返回被删掉的 key 集合（父 + 全部子会话）。
//
// 返回 key 列表是给调用方的：上层要拿它去解绑任务引用（Task.MainSessionKey /
// StageRun.SessionKey），否则任务会一直指向一个已不存在的会话。
func (s *Service) DeleteSession(ctx context.Context, in DeleteSessionInput) ([]string, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return nil, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, err
	}
	keys, err := deleteSessionCascadeKeys(ctx, manager, in.Key)
	if err != nil {
		return nil, err
	}
	if err := s.deleteSessionKeys(ctx, root, manager, keys); err != nil {
		return nil, err
	}
	return keys, nil
}

// deleteSessionKeys 真删一批会话的完整清理序列：先掐断在跑的 turn，再逐个收资源。
// 归档要删子树时也走这里 —— 归档过的子会话和普通删除在资源层面没有区别。
func (s *Service) deleteSessionKeys(
	ctx context.Context,
	root fs.RootInfo,
	manager *session.Manager,
	keys []string,
) error {
	for _, key := range keys {
		cancelActiveSessionTurn(root.ID, key)
	}
	for _, key := range keys {
		cancelActiveSessionTurn(root.ID, key)
		if err := manager.Delete(ctx, key); err != nil {
			return err
		}
		if err := root.RemoveSessionFileMeta(key); err != nil {
			return err
		}
		commandexec.CloseSession(root.ID, key)
		s.Registry.ReleaseFileWatcher(root.ID, key)
	}
	return nil
}

// sessionSubtreeKeys 收集 key 及其全部后代（子会话）的 key 集合。
//
// 删除和归档共用：两者都是「整棵树一起处理」，区别只在最后一步是删还是打归档标记。
// 按 ParentSessionKey 遍历，fork 会话不在其中（fork 只是 source 里记了来源，不是父子关系）。
func sessionSubtreeKeys(ctx context.Context, manager *session.Manager, key string) ([]string, error) {
	rootKey := strings.TrimSpace(key)
	if rootKey == "" {
		return nil, errors.New("session key required")
	}
	items, err := manager.ListMetas(ctx)
	if err != nil {
		return nil, err
	}
	childrenByParent := make(map[string][]string)
	exists := false
	for _, item := range items {
		if item == nil {
			continue
		}
		itemKey := strings.TrimSpace(item.Key)
		if itemKey == "" {
			continue
		}
		if itemKey == rootKey {
			exists = true
		}
		parentKey := strings.TrimSpace(item.ParentSessionKey)
		if parentKey != "" {
			childrenByParent[parentKey] = append(childrenByParent[parentKey], itemKey)
		}
	}
	if !exists {
		return nil, errors.New("session not found")
	}
	keys := make([]string, 0, 1)
	seen := make(map[string]bool)
	var visit func(string)
	visit = func(current string) {
		if seen[current] {
			return
		}
		seen[current] = true
		for _, childKey := range childrenByParent[current] {
			visit(childKey)
		}
		keys = append(keys, current)
	}
	visit(rootKey)
	return keys, nil
}

// deleteSessionCascadeKeys 保留旧名：删除用例用它，语义就是「要删的整棵子树」。
func deleteSessionCascadeKeys(ctx context.Context, manager *session.Manager, key string) ([]string, error) {
	return sessionSubtreeKeys(ctx, manager, key)
}

type ArchiveSessionInput struct {
	RootID string
	Key    string
	// Archived = true 归档（自己），false 取消归档。
	Archived bool
}

type ArchiveSessionOutput struct {
	// Session 是被归档/取消归档的那个会话本身（请求的那个 key）。
	Session *session.Session
	// DeletedKeys 是随这次归档被**删掉**的子会话。父会话只被标记，子会话是真删除 ——
	// 两种不同的状态：父会话留在归档区，子会话从主面板和归档区都消失。
	DeletedKeys []string
}

// ArchiveSession 归档一个会话：**只把它自己**打归档标记，它整棵子树的子会话**直接删除**。
//
// 为什么子会话是删而不是跟着归档：归档区按项目分组平铺，子会话挂在那儿只会变成
// 孤儿行（父被折叠就找不到，或撑成两层）。而归档本来就不该保留过程噪声 —— 用户
// 归档的是「这段对话到此为止」，子会话是它内部的展开步骤，不值得单独占一行。
//
// 与 DeleteSession 的差别在**对象**：删除要收掉整棵树（包括被点的那个），归档只删
// 后代、被点的那个只打标记不动内容（深链接能打开、还能继续跑）。
//
// 取消归档维持原样（整棵子树一起解标记）：删除不可逆，逆操作没法补偿，只解标记
// 是当前能给出的最接近还原的行为。
func (s *Service) ArchiveSession(ctx context.Context, in ArchiveSessionInput) (ArchiveSessionOutput, error) {
	if err := s.ensureRegistry(); err != nil {
		return ArchiveSessionOutput{}, err
	}
	root, err := s.Registry.GetRoot(in.RootID)
	if err != nil {
		return ArchiveSessionOutput{}, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return ArchiveSessionOutput{}, err
	}
	self := strings.TrimSpace(in.Key)
	keys, err := sessionSubtreeKeys(ctx, manager, self)
	if err != nil {
		return ArchiveSessionOutput{}, err
	}
	if !in.Archived {
		// 取消归档：整棵子树一起解标记。
		for _, key := range keys {
			if _, err := manager.SetArchived(ctx, key, false); err != nil {
				return ArchiveSessionOutput{}, err
			}
		}
		updated, err := manager.SetArchived(ctx, self, false)
		if err != nil {
			return ArchiveSessionOutput{}, err
		}
		return ArchiveSessionOutput{Session: updated}, nil
	}
	// 归档：先删子树（删的是后代，自己不在里面），再给自己打标记。
	// 顺序反了会出问题 —— 删完子树后 self 的后代链已不存在，但 self 本身仍在，
	// 两种顺序都能跑；先删是为了中途失败时不留下「已归档但子会话还在」的窗口。
	children := make([]string, 0, len(keys))
	for _, key := range keys {
		if key != self {
			children = append(children, key)
		}
	}
	if len(children) > 0 {
		if err := s.deleteSessionKeys(ctx, root, manager, children); err != nil {
			return ArchiveSessionOutput{}, err
		}
	}
	updated, err := manager.SetArchived(ctx, self, true)
	if err != nil {
		return ArchiveSessionOutput{}, err
	}
	return ArchiveSessionOutput{Session: updated, DeletedKeys: children}, nil
}

func cancelActiveSessionTurn(rootID, sessionKey string) {
	active := getActiveTurn(rootID, sessionKey)
	if active == nil {
		return
	}
	active.cancel()
	if active.session != nil {
		if err := active.session.CancelCurrentTurn(); err != nil {
			log.Printf("[session] turn.cancel.error root=%s session=%s err=%v", rootID, sessionKey, err)
		}
	}
}

type RenameSessionInput struct {
	RootID string
	Key    string
	Name   string
}

func (s *Service) RenameSession(ctx context.Context, in RenameSessionInput) (*session.Session, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, err
	}
	return manager.Rename(ctx, in.Key, in.Name)
}

type BuildPromptInput struct {
	Session                       *session.Session
	Manager                       *session.Manager
	Agent                         string
	Message                       string
	ClientContext                 ClientContext
	AgentCtxSeq                   *int
	IsInitial                     bool
	IncludeReplyTipsInUserMessage bool
	// LinesBeforeThisTurn 是「本轮 user 行写入之前」的会话行数。本轮 user 行在
	// SendMessage 起手就已落盘（见 persistUserTurnExchange），所以 Session.Exchanges
	// 里已经包含它；切 agent 提示要读的「上一轮之后还剩几行」必须用写入前的计数，
	// 否则会把本轮自己的 user 行当成「需要回读的历史」。
	//
	// 必须是 *int 而不是 int：会话首轮的真实值**恰好也是 0**，用零值当「没传」的哨兵
	// 会让首轮回退到 len(Session.Exchanges)=1，于是 delta=1>0 —— 全新会话的第一轮就被
	// 告知「本会话从别处迁移而来，去读你自己的历史文件」，读到的是刚收到的那条消息。
	// 语义与同结构体的 AgentCtxSeq 一致，nil 即「没传」。
	LinesBeforeThisTurn *int
}

func (s *Service) BuildPrompt(in BuildPromptInput) string {
	clientCtx := in.ClientContext
	prompt := buildUserPrompt(in.Message, clientCtx)
	if strings.TrimSpace(clientCtx.PluginCatalog) != "" {
		pluginDir := ".mindfs/plugins"
		if in.Manager != nil && in.Manager.Root().EffectiveMetaLocation() == fs.MetaLocationHome {
			pluginDir = filepath.Join(in.Manager.Root().MetaDir(), "plugins")
		}
		prompt = buildPluginPrompt(clientCtx.PluginCatalog, in.Message, in.IsInitial, pluginDir)
	} else if in.IsInitial && in.IncludeReplyTipsInUserMessage {
		prompt = appendReplyTips(prompt)
	}
	if in.IsInitial && in.Manager != nil && in.Session != nil {
		prompt += fmt.Sprintf("\n\nMindFS context: root_id=%s session_key=%s", in.Manager.Root().ID, in.Session.Key)
		if in.Session.TaskID != "" {
			prompt += " task_id=" + in.Session.TaskID
		}
		prompt += ". If asked to orchestrate tasks, read mindfs -orchestration; create a task group with this parent session_key, then create ordinary template tasks in that group.\n"
	}
	return prependSwitchHint(in, prompt)
}

func prependSwitchHint(in BuildPromptInput, prompt string) string {
	if in.Session == nil || in.Manager == nil {
		return prompt
	}
	currentAgent := strings.TrimSpace(in.Agent)
	if currentAgent == "" {
		return prompt
	}
	total := contextLineCount(in.Session.Exchanges)
	if in.LinesBeforeThisTurn != nil {
		total = *in.LinesBeforeThisTurn
	}
	last := 0
	if in.AgentCtxSeq != nil {
		last = *in.AgentCtxSeq
	} else {
		last = in.Session.AgentCtxSeq[currentAgent]
	}
	linesToRead := calculateSwitchReadLines(total, last)
	if linesToRead <= 0 {
		return prompt
	}
	logPath := switchReadHintPath(in.Manager, in.Session.Key)
	readHint := buildSwitchReadHint(logPath, linesToRead)
	return readHint + prompt
}

type SendMessageInput struct {
	RootID                 string
	RuntimeRootPath        string
	Key                    string
	Agent                  string
	Model                  string
	Mode                   string
	Effort                 string
	FastService            string
	PlanMode               *bool
	Shell                  string
	TerminalCols           int
	Content                string
	UserTimestamp          time.Time
	ClientCtx              ClientContext
	OnStart                func(MessageStart)
	OnUpdate               func(agenttypes.Event)
	OnSubSessionCreated    func(*session.Session)
	OnSubSessionUpdate     func(sessionKey string, update agenttypes.Event)
	OnAgentDefaultsChanged func(agentName string)
}

type MessageStart struct {
	Model            string
	ModelDisplayName string
	Mode             string
	Effort           string
	FastService      string
	BaseExchangeSeq  int
	// UserExchangeSeq 是本轮用户消息**已落盘**的 seq（user 行在回合开始前就写入了，
	// 见 persistUserTurnExchange），不是「即将获得」的预测值。仅用于下发给客户端做本地
	// 乐观条目的收敛；0 表示未知（写入失败或会话里没有可落地的行），此时不要下发。
	//
	// 为什么必须是真值：客户端据此认定该行已持久化，于是 overlay 的
	// 「seq<=latestSeq 即让位给窗口」规则不再保留它。若这里给的是预测值，窗口
	// maxSeq 会在整轮执行期间都看不到它 —— 用户自己的消息就会从列表里消失，
	// 且点同步也刷不回来（2026-09-28 实测）。
	UserExchangeSeq int
}

func applyMessageRuntimeDefaultsFromStatus(
	in *SendMessageInput,
	status agent.Status,
	statusOK bool,
) {
	if in == nil || strings.TrimSpace(in.Model) != "" {
		return
	}
	in.Model = ""
	in.Effort = strings.TrimSpace(in.Effort)
	in.FastService = strings.TrimSpace(in.FastService)
	if !statusOK {
		return
	}
	in.Model = strings.TrimSpace(status.DefaultModelID)
	if in.Model == "" {
		in.Model = strings.TrimSpace(status.CurrentModelID)
	}
	if in.Effort == "" {
		in.Effort = strings.TrimSpace(status.DefaultEffort)
	}
	if in.FastService == "" {
		in.FastService = strings.TrimSpace(status.DefaultFastService)
	}
}

func (s *Service) applyMessageRuntimeDefaults(in *SendMessageInput) {
	if in == nil || strings.TrimSpace(in.Model) != "" {
		return
	}
	if s == nil || s.Registry == nil || s.Registry.GetProber() == nil {
		applyMessageRuntimeDefaultsFromStatus(in, agent.Status{}, false)
		return
	}
	status, ok := s.Registry.GetProber().GetStatus(strings.TrimSpace(in.Agent))
	if ok {
		if prefs := s.Registry.GetPreferences(); prefs != nil {
			status = prefs.ApplyAgentDefaults([]agent.Status{status})[0]
		}
	}
	applyMessageRuntimeDefaultsFromStatus(in, status, ok)
}

func sendMessageUserTimestamp(in SendMessageInput, fallback time.Time) time.Time {
	if !in.UserTimestamp.IsZero() {
		return in.UserTimestamp.UTC()
	}
	if fallback.IsZero() {
		return time.Now().UTC()
	}
	return fallback.UTC()
}

type RunTransientSlashCommandInput struct {
	RootID      string
	Key         string
	Agent       string
	Model       string
	Mode        string
	Effort      string
	FastService string
	Command     string
	OnUpdate    func(agenttypes.Event)
}

type codexDeviceCodeLoginSession interface {
	LoginChatGPTDeviceCode(context.Context) error
}

var activeSubagentSubscriptions sync.Map

type AnswerQuestionInput struct {
	RootID     string
	SessionKey string
	Agent      string
	ToolUseID  string
	Answers    map[string]string
}

type CancelSessionTurnInput struct {
	RootID string
	Key    string

	// TargetTurnID / TargetPinned 描述调用方「点下取消那一刻」在跑的是哪一轮。
	//
	// 取消的落地晚于它的决定：请求从用户点到执行可能隔几百毫秒（ws.go 怕阻塞读循环
	// 用 goroutine 派发），这期间同一会话完全可能已经开了新一轮。实测 2026-10-02：
	// 用户在空闲会话上点停止，几百毫秒后又发了一条消息，那一轮刚 send.begin 就被这个
	// 迟到的取消掐掉 —— dsh 表现为「733ms 就完成了，什么都没吐」。
	//
	//	TargetPinned=false            服务端自己发起的取消（如「立即发送」挤掉当前轮），
	//	                              不校验，作用于此刻活跃的那一轮。
	//	TargetPinned=true, ID=非 0    只作用于 id 相同的那一轮；对不上就丢弃。
	//	TargetPinned=true, ID=0       决定取消时根本没有在跑的一轮 → 直接丢弃，
	//	                              不能落到之后起来的新轮上。
	TargetTurnID uint64
	TargetPinned bool
}

const (
	switchContextTailLines   = 20
	sessionNameTimeout       = 30 * time.Second
	sessionNameMinMessageLen = 12
	sessionRecoveryAttempts  = 3
	sessionRecoveryDelay     = 30 * time.Second
)

type SuggestSessionNameInput struct {
	RootID       string
	SessionKey   string
	Agent        string
	Model        string
	FirstMessage string
}

var (
	sessionSendLocksMu sync.Mutex
	sessionSendLocks   = make(map[string]*sync.Mutex)
	activeTurnsMu      sync.Mutex
	activeTurns        = make(map[string]*activeTurnState)
	activeTurnSeq      uint64
)

type activeTurnState struct {
	// id 标识「具体是哪一轮」。取消是异步落地的（ws.go 异步派发，因为 Interrupt
	// 可能阻塞），从用户点到执行可以隔上几百毫秒；这期间同一会话可能已经开了新的
	// 一轮（用户点完停止紧接着发消息）。调用方在收到请求的那一刻就记下当时的 id，
	// 落地时 id 对不上就说明它要停的那一轮已经结束了 —— 直接放弃，别误伤新轮。
	id      uint64
	cancel  context.CancelFunc
	session agenttypes.Session
}

func getSessionSendLock(sessionKey string) *sync.Mutex {
	sessionSendLocksMu.Lock()
	defer sessionSendLocksMu.Unlock()
	lock := sessionSendLocks[sessionKey]
	if lock == nil {
		lock = &sync.Mutex{}
		sessionSendLocks[sessionKey] = lock
	}
	return lock
}

func activeTurnKey(rootID, sessionKey string) string {
	return rootID + "::" + sessionKey
}

func registerActiveTurn(rootID, sessionKey string, cancel context.CancelFunc) {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(sessionKey) == "" || cancel == nil {
		return
	}
	activeTurnsMu.Lock()
	activeTurnSeq++
	activeTurns[activeTurnKey(rootID, sessionKey)] = &activeTurnState{id: activeTurnSeq, cancel: cancel}
	activeTurnsMu.Unlock()
}

// ActiveTurnID 返回此刻在跑的那一轮的 id，没有在跑的一轮时返回 0。
//
// 调用方必须在**决定取消的那一刻**同步取证（ws.go 的读循环里），再拿着这个 id
// 去做异步的取消：取消落地时 id 不一致就说明目标轮次已经结束，必须放弃。
func ActiveTurnID(rootID, sessionKey string) uint64 {
	activeTurnsMu.Lock()
	defer activeTurnsMu.Unlock()
	if state := activeTurns[activeTurnKey(rootID, sessionKey)]; state != nil {
		return state.id
	}
	return 0
}

func setActiveTurnSession(rootID, sessionKey string, sess agenttypes.Session) {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(sessionKey) == "" || sess == nil {
		return
	}
	activeTurnsMu.Lock()
	state := activeTurns[activeTurnKey(rootID, sessionKey)]
	if state != nil {
		state.session = sess
	}
	activeTurnsMu.Unlock()
}

func unregisterActiveTurn(rootID, sessionKey string) {
	if strings.TrimSpace(rootID) == "" || strings.TrimSpace(sessionKey) == "" {
		return
	}
	activeTurnsMu.Lock()
	delete(activeTurns, activeTurnKey(rootID, sessionKey))
	activeTurnsMu.Unlock()
}

func getActiveTurn(rootID, sessionKey string) *activeTurnState {
	activeTurnsMu.Lock()
	defer activeTurnsMu.Unlock()
	return activeTurns[activeTurnKey(rootID, sessionKey)]
}

func agentPoolSessionKey(sessionKey, agentName string) string {
	trimmedSessionKey := strings.TrimSpace(sessionKey)
	if trimmedSessionKey == "" {
		return ""
	}
	trimmedAgent := strings.TrimSpace(agentName)
	if trimmedAgent == "" {
		return trimmedSessionKey
	}
	return strings.ToLower(trimmedAgent) + "-" + trimmedSessionKey
}

func calculateSwitchReadLines(total, lastCtxSeq int) int {
	delta := total - lastCtxSeq
	if delta < 0 {
		return 0
	}
	if delta > switchContextTailLines {
		return switchContextTailLines
	}
	return delta
}

// switchReadHintPath 一律返回绝对路径。
//
// 早先项目内 meta（非 MetaLocationHome）会走 filepath.Rel 产出相对 runtime root 的
// 路径（worktree 下是 ../../.mindfs/...）。但提示文案里没有说明基准目录，agent 只能
// 猜：猜成 home 就变成 ~/.mindfs/sessions/...，读不到文件，误判成「历史丢失」而停摆。
// Rel 失败时还会回退到无基准的 .mindfs/... —— 同一段文案出现三种路径形态。
// 绝对路径把基准歧义整个消掉，worktree 与否不再影响结果，runtimeRootAbs 参数随之作废。
func switchReadHintPath(manager *session.Manager, sessionKey string) string {
	if manager == nil {
		return ""
	}
	absLogPath := manager.ExchangeLogAbsolutePath(sessionKey)
	if strings.TrimSpace(absLogPath) == "" {
		return manager.ExchangeLogPath(sessionKey)
	}
	return filepath.ToSlash(absLogPath)
}

func buildSwitchReadHint(exchangeLogPath string, lines int) string {
	return "This session was migrated from elsewhere. Your context may lag behind this session;\n" +
		"Before replying, read the last " + strconv.Itoa(lines) + " lines from " + exchangeLogPath + " to recover context.\n" +
		"If you still need more context, decide and read older history yourself.\n" +
		"When continuing to read, keep each backward batch to about " + strconv.Itoa(switchContextTailLines) + " lines.\n\n" +
		"Execution order: read history first, then compose the final answer.\n" +
		"Note: do not send any natural-language response before finishing the required history reads. Start reading immediately via tools/commands.\n" +
		"Only if reading fails, output a brief error and stop.\n\n"
}

func sessionNameRunner(ctx context.Context, pool *agent.Pool, rootAbs string, in SuggestSessionNameInput) (string, error) {
	agentName := strings.TrimSpace(in.Agent)
	if agentName == "" || pool == nil {
		return "", nil
	}

	tmpRoot, err := agent.EnsureStableWorkDir("title-rename", agentName)
	if err != nil {
		return "", err
	}

	sessionKey := agentPoolSessionKey("name-"+in.SessionKey, agentName)
	sess, err := pool.GetOrCreate(ctx, agenttypes.OpenSessionInput{
		SessionKey: sessionKey,
		AgentName:  agentName,
		Model:      strings.TrimSpace(in.Model),
		RootPath:   tmpRoot,
	})
	if err != nil {
		return "", err
	}
	defer pool.Close(sessionKey)

	var response strings.Builder
	sess.OnUpdate(func(update agenttypes.Event) {
		if update.Type != agenttypes.EventTypeMessageChunk {
			return
		}
		chunk, ok := update.Data.(agenttypes.MessageChunk)
		if !ok {
			return
		}
		response.WriteString(chunk.Content)
	})

	if err := sess.SendMessage(ctx, buildSessionNamePrompt(normalizeSessionNameCandidate(in.FirstMessage))); err != nil {
		return "", err
	}
	return response.String(), nil
}

func (s *Service) SuggestSessionName(ctx context.Context, in SuggestSessionNameInput) (*session.Session, error) {
	if err := s.ensureRegistry(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.RootID) == "" || strings.TrimSpace(in.SessionKey) == "" {
		return nil, nil
	}
	agentName := strings.TrimSpace(in.Agent)
	model := strings.TrimSpace(in.Model)
	if prefs := s.Registry.GetPreferences(); prefs != nil {
		namingDefaults := prefs.SessionNamingDefaults()
		if namingDefaults.Disabled {
			return nil, nil
		}
		if strings.TrimSpace(namingDefaults.Agent) != "" {
			agentName = strings.TrimSpace(namingDefaults.Agent)
			model = strings.TrimSpace(namingDefaults.Model)
		}
	}
	if agentName == "" {
		return nil, nil
	}
	in.Agent = agentName
	in.Model = model
	message := normalizeSessionNameCandidate(in.FirstMessage)
	if sessionNameScore(message) < sessionNameMinMessageLen {
		return nil, nil
	}

	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return nil, err
	}
	current, err := manager.Get(ctx, in.SessionKey, 0)
	if err != nil {
		return nil, err
	}
	fallback := BuildFallbackSessionName(in.FirstMessage)
	if strings.TrimSpace(current.Name) != fallback {
		return nil, nil
	}

	pool := s.Registry.GetAgentPool()
	if pool == nil {
		return nil, nil
	}

	rootAbs, err := manager.Root().RootDir()
	if err != nil {
		return nil, err
	}
	nameCtx, cancel := context.WithTimeout(ctx, sessionNameTimeout)
	defer cancel()

	rawName, err := sessionNameRunner(nameCtx, pool, rootAbs, in)
	if err != nil {
		log.Printf("[session-name] suggest.error root=%s session=%s agent=%s model=%q err=%v", in.RootID, in.SessionKey, agentName, model, err)
		return nil, nil
	}

	name := normalizeSessionNameCandidate(rawName)
	if name == "" || name == fallback {
		return nil, nil
	}
	renamed, err := manager.Rename(ctx, in.SessionKey, name)
	if err != nil {
		log.Printf("[session-name] rename.error root=%s session=%s err=%v", in.RootID, in.SessionKey, err)
		return nil, err
	}
	log.Printf("[session-name] rename.done root=%s session=%s name=%q", in.RootID, in.SessionKey, renamed.Name)
	return renamed, nil
}

func BuildFallbackSessionName(message string) string {
	oneLine := strings.Join(strings.Fields(strings.TrimSpace(message)), " ")
	if oneLine == "" {
		return ""
	}
	const max = 60
	runes := []rune(oneLine)
	if len(runes) <= max {
		return oneLine
	}
	return string(runes[:max]) + "..."
}

func buildSessionNamePrompt(message string) string {
	return strings.TrimSpace(strings.Join([]string{
		"Generate a concise session title for the user's first message.",
		"Rules:",
		"- Reply with the title only.",
		"- Single line only.",
		"- No quotes.",
		"- No trailing punctuation.",
		"- Keep it under 18 Chinese characters or 8 English words.",
		"",
		"User message:",
		message,
	}, "\n"))
}

func normalizeSessionNameCandidate(raw string) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	cleaned = strings.Trim(cleaned, "\"'`“”‘’")
	cleaned = strings.TrimSpace(cleaned)
	cleaned = strings.TrimRight(cleaned, ".,;:!?，。；：！？")
	return cleaned
}

func sessionNameScore(message string) int {
	score := 0
	tokenRun := 0

	flushTokenRun := func() {
		if tokenRun == 0 {
			return
		}
		score++
		tokenRun = 0
	}

	for _, r := range message {
		switch {
		case isSessionNameTokenRune(r):
			tokenRun++
		default:
			flushTokenRun()
			if unicode.IsSpace(r) || unicode.IsPunct(r) {
				continue
			}
			score++
		}
	}
	flushTokenRun()
	return score
}

func isSessionNameTokenRune(r rune) bool {
	if r > unicode.MaxASCII {
		return false
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func isCanceledTurnError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(value, "context canceled") ||
		strings.Contains(value, "context cancelled") ||
		strings.Contains(value, "turn canceled") ||
		strings.Contains(value, "turn cancelled") ||
		strings.Contains(value, "cancelled")
}

func isNonRecoverableAgentError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(err.Error()))
	if value == "" {
		return false
	}
	needles := []string{
		"429",
		"too many requests",
		"exceeded retry limit",
		"rate limit",
		"ratelimit",
		"usage limit",
		"usagelimitexceeded",
		"remote compaction failed",
		"compact_remote",
		"responsetoomanyfailedattempts",
	}
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func cancelRuntimeAfterNonRecoverableError(sess agenttypes.Session, pool *agent.Pool, agentName string, cause error) {
	agentName = strings.TrimSpace(agentName)
	if sess != nil {
		if err := sess.CancelCurrentTurn(); err != nil {
			log.Printf("[session] turn.cancel_after_non_recoverable.error agent=%s cause=%v err=%v", agentName, cause, err)
		}
	}
	if pool != nil && agentName != "" {
		if _, ok := pool.KillAgentProcess(agentName, 0); ok {
			log.Printf("[session] runtime.kill_after_non_recoverable.done agent=%s cause=%v", agentName, cause)
			return
		}
	}
	if sess != nil {
		if err := sess.Close(); err != nil {
			log.Printf("[session] runtime.close_after_non_recoverable.error agent=%s cause=%v err=%v", agentName, cause, err)
		}
	}
}

func contextLineCount(exchanges []session.Exchange) int {
	return len(exchanges)
}

const replyTips = "[REPLY_TIPS]\n\n" +
	"- Prefer GitHub-flavored Markdown. Fenced `mermaid` diagrams, mathematical formulas, and Markdown images are supported.\n" +
	"- When a useful workspace image exists, embed it with `![alt](path)` and prefer a workspace-root-relative path.\n" +
	"- Never invent file paths or URLs. Use images or diagrams only when they materially improve the reply."

func appendReplyTips(prompt string) string {
	return strings.TrimSpace(prompt) + "\n\n" + replyTips
}

func buildUserPrompt(message string, clientCtx ClientContext) string {
	lines := []string{strings.TrimSpace(message)}
	if clientCtx.Selection != nil {
		lines = append(lines, "[USER_SELECTION]")
		if clientCtx.Selection.FilePath != "" {
			lines = append(lines, "file: "+clientCtx.Selection.FilePath)
		}
		if clientCtx.Selection.StartLine > 0 || clientCtx.Selection.EndLine > 0 {
			lines = append(lines, "line range: "+strconv.Itoa(clientCtx.Selection.StartLine)+"-"+strconv.Itoa(clientCtx.Selection.EndLine))
		}
		if strings.TrimSpace(clientCtx.Selection.Text) != "" {
			lines = append(lines, "selected text: "+clientCtx.Selection.Text)
		}
	}
	return strings.Join(lines, "\n")
}

func buildPluginPrompt(catalogPrompt, userMessage string, isInitial bool, pluginDir string) string {
	if isInitial {
		return buildPluginPromptInitial(catalogPrompt, userMessage, pluginDir)
	}
	return buildPluginPromptFollowup(userMessage, pluginDir)
}

func buildPluginPromptFollowup(userMessage, pluginDir string) string {
	systemPrompt := strings.TrimSpace(strings.Join([]string{
		"You are still in view-plugin development mode.",
		"Continue editing/refining the plugin under " + filepath.ToSlash(pluginDir) + "/.",
		"",
		"Follow these strict constraints:",
		"- If the user explicitly asks to generate/update plugin code, output JS code only (no markdown fences, no explanation text).",
		"- If the user asks analysis/design/review questions, answer normally and do not output plugin code unless requested.",
		"- Use CommonJS: module.exports = { name, match, fileLoadMode, theme, process(file) { return { data?, tree } }, viewContext?(file) { return string | object } }.",
		"- fileLoadMode must be \"incremental\" or \"full\".",
		"- theme is required with all keys: overlayBg, surfaceBg, surfaceBgElevated, text, textMuted, border, primary, primaryText, radius, shadow, focusRing, danger, warning, success.",
		"- Optional viewContext(file) returns concise current-view context for agent conversations when this plugin view is active.",
		"- viewContext should describe current view state, not duplicate large visible content; selected text is attached separately by the app.",
		"- Do not modify framework CSS/TS code.",
		"- Do not output global CSS overrides.",
		"- For dynamic interactions, use action \"navigate\" with params { path?, cursor?, query? }.",
	}, "\n"))

	return strings.Join([]string{
		"[SYSTEM_PROMPT]",
		systemPrompt,
		"",
		"[USER_PROMPT]",
		userMessage,
	}, "\n")
}

func buildPluginPromptInitial(catalogPrompt, userMessage, pluginDir string) string {
	systemPrompt := strings.TrimSpace(strings.Join([]string{
		"You are in view-plugin development mode.",
		"The user will describe requirements. Generate a view plugin and write it under " + filepath.ToSlash(pluginDir) + "/.",
		"",
		"## Plugin Spec",
		"- Use CommonJS: module.exports = { name, match, fileLoadMode, theme, process(file) { return { data?, tree } }, viewContext?(file) { return string | object } }",
		"- fileLoadMode: \"incremental\" | \"full\".",
		"- fileLoadMode controls how file content is loaded before process(file).",
		"- Use \"full\" for views that need global understanding of the file (chapter TOC, CSV table pagination/sort/filter, whole-document search).",
		"- Use \"incremental\" only for very large plain-text streaming/append-like views where byte-window loading is acceptable.",
		"- In \"full\" mode, plugin should treat input as whole-file content and should not rely on cursor.",
		"- If interaction is query-based pagination (page/pageSize), prefer \"full\" and update only query.",
		"- theme is required and must include all keys:",
		"  overlayBg, surfaceBg, surfaceBgElevated, text, textMuted, border,",
		"  primary, primaryText, radius, shadow, focusRing, danger, warning, success.",
		"- Do not modify framework CSS/TS code.",
		"- Do not output global CSS overrides.",
		"- Style customization must be done via theme tokens only.",
		"- file input: { name, path, content, ext, mime, size, truncated, next_cursor, query }",
		"- query comes from URL plugin params. Plugin reads file.query.<key> directly.",
		"- query is for business state only; do NOT store cursor in query.",
		"- Plugin must treat query as plain keys and must NOT depend on URL encoding details.",
		"- process must be a pure function (no external IO/state).",
		"- Optional viewContext(file) should also be pure. It may return a string or object with concise current-view context for agent conversations.",
		"- Use viewContext for state such as current page/chapter/filter/sort. Do not include large visible content; selected text is attached separately by the app.",
		"- event bindings must use top-level `on` field, not inside `props`.",
		"- filename should be lowercase kebab-case, e.g. txt-novel.js",
		"",
		"## Match Rule",
		"- ext: \".txt\" or \".csv,.tsv\"",
		"- path: \"novels/**/*.txt\"",
		"- mime: \"text/*\"",
		"- name: \"README*\"",
		"- any/all for OR/AND composition",
		"",
		"## Output Requirement",
		"- Use available file-write tool(s) to write plugin file to " + filepath.ToSlash(pluginDir) + "/<name>.js",
		"- tree must be valid UITree: root points to an existing element id",
		"- For dynamic interactions (pagination/sort/filter), use action: \"navigate\"",
		"- navigate params: { path?, cursor?, query? }",
		"- path: target file path (relative path under current root).",
		"- cursor: byte cursor used when re-reading the file.",
		"- query: plugin state map; after navigate, plugin reads it from file.query.",
		"- navigate usage examples:",
		"  - Change query only: { action: \"navigate\", params: { query: { page: 2 } } }",
		"  - Change cursor only: { action: \"navigate\", params: { cursor: 131072 } }",
		"  - Change both: { action: \"navigate\", params: { path: \"a.txt\", cursor: 0, query: { chapter: 1 } } }",
		"  - Incremental next chunk: read next cursor from file.next_cursor, then set navigate.params.cursor to that value.",
		"  - Example: { action: \"navigate\", params: { cursor: file.next_cursor } }",
		"- Plugin should always read current plugin state from file.query.",
		"- Return only JS plugin code. No markdown fences. No explanation text.",
		"",
		"## Responsive Breakpoints (required)",
		"- mobile: width < 768",
		"- tablet: 768 <= width < 1024",
		"- desktop: width >= 1024",
		"- Prefer single-column, tighter spacing, and larger touch targets on mobile",
		"- For wide tables/code blocks on mobile, provide horizontal scrolling or condensed fallback",
		"- Avoid fixed-width layouts that overflow small screens",
		"",
		"## Example Plugin (TXT Novel Reader)",
		"module.exports = {",
		"  name: \"TXT Novel Reader\",",
		"  match: { ext: \".txt\" },",
		"  fileLoadMode: \"full\",",
		"  theme: {",
		"    overlayBg: \"rgba(2,6,23,0.62)\",",
		"    surfaceBg: \"#f8fafc\",",
		"    surfaceBgElevated: \"#ffffff\",",
		"    text: \"#0f172a\",",
		"    textMuted: \"#475569\",",
		"    border: \"rgba(15,23,42,0.12)\",",
		"    primary: \"#2563eb\",",
		"    primaryText: \"#ffffff\",",
		"    radius: \"10px\",",
		"    shadow: \"0 16px 40px rgba(2,6,23,.22)\",",
		"    focusRing: \"rgba(37,99,235,.4)\",",
		"    danger: \"#dc2626\",",
		"    warning: \"#d97706\",",
		"    success: \"#16a34a\"",
		"  },",
		"  process(file) {",
		"    const content = typeof file.content === \"string\" ? file.content.replace(/\\r\\n?/g, \"\\n\") : \"\";",
		"    const query = file.query || {};",
		"    const lines = content.split(\"\\n\");",
		"    const chapterTitles = lines.filter((line) => /^\\s*第.+[章节回卷篇部]/.test(line.trim()));",
		"    const chapters = chapterTitles.length ? chapterTitles.map((title) => ({ title: title.trim(), text: content })) : [{ title: file.name ? String(file.name).replace(/\\.txt$/i, \"\") : \"正文\", text: content }];",
		"    const total = Math.max(1, chapters.length);",
		"    const chapterIdx = Math.min(Math.max(1, parseInt(query.chapter || \"1\", 10) || 1), total) - 1;",
		"    const current = chapters[chapterIdx] || { title: \"正文\", text: content };",
		"    const paragraphs = (current.text || \"\").split(\"\\n\").map(s => s.trim()).filter(Boolean).slice(0, 500);",
		"    const tocValue = String(query.toc || \"0\");",
		"    const showToc = tocValue !== \"0\";",
		"    const nextTocValue = String((parseInt(tocValue, 10) || 0) + 1);",
		"    return {",
		"      data: { ui: { tocOpen: showToc } },",
		"      tree: {",
		"        root: \"root\",",
		"        elements: {",
		"          root: { type: \"Stack\", props: { direction: \"vertical\", gap: \"sm\" }, children: [\"header\", \"nav-top\", \"content-card\", \"nav-bottom\", \"toc-dialog\"] },",
		"          header: { type: \"Stack\", props: { direction: \"horizontal\", gap: \"sm\", justify: \"between\", align: \"center\" }, children: [\"title\"] },",
		"          title: { type: \"Heading\", props: { text: current.title, level: \"h4\" }, children: [] },",
		"          \"nav-top\": { type: \"Stack\", props: { direction: \"horizontal\", gap: \"sm\", justify: \"between\" }, children: [\"prev-t\", \"toc-t\", \"next-t\"] },",
		"          \"nav-bottom\": { type: \"Stack\", props: { direction: \"horizontal\", gap: \"sm\", justify: \"between\" }, children: [\"prev-b\", \"toc-b\", \"next-b\"] },",
		"          \"prev-t\": { type: \"Button\", props: { label: \"上一章\", disabled: chapterIdx <= 0 }, on: { press: { action: \"navigate\", params: { query: { chapter: chapterIdx, toc: \"0\" } } } } },",
		"          \"toc-t\": { type: \"Button\", props: { label: \"目录\" }, on: { press: { action: \"navigate\", params: { query: { toc: nextTocValue } } } } },",
		"          \"next-t\": { type: \"Button\", props: { label: \"下一章\", disabled: chapterIdx >= total - 1 }, on: { press: { action: \"navigate\", params: { query: { chapter: chapterIdx + 2, toc: \"0\" } } } } },",
		"          \"prev-b\": { type: \"Button\", props: { label: \"上一章\", disabled: chapterIdx <= 0 }, on: { press: { action: \"navigate\", params: { query: { chapter: chapterIdx, toc: \"0\" } } } } },",
		"          \"toc-b\": { type: \"Button\", props: { label: \"目录\" }, on: { press: { action: \"navigate\", params: { query: { toc: nextTocValue } } } } },",
		"          \"next-b\": { type: \"Button\", props: { label: \"下一章\", disabled: chapterIdx >= total - 1 }, on: { press: { action: \"navigate\", params: { query: { chapter: chapterIdx + 2, toc: \"0\" } } } } },",
		"          \"content-card\": { type: \"Card\", props: { title: null, description: null, maxWidth: \"full\" }, children: [\"para-stack\"] },",
		"          \"para-stack\": { type: \"Stack\", props: { direction: \"vertical\", gap: \"sm\" }, children: paragraphs.map((_, i) => `p-${i}`) },",
		"          ...Object.fromEntries(paragraphs.map((line, i) => [`p-${i}`, { type: \"Text\", props: { text: line, variant: \"body\" }, children: [] }])),",
		"          \"toc-dialog\": { type: \"Dialog\", props: { title: \"章节目录\", openPath: \"/ui/tocOpen\" }, children: [\"toc-list\", \"toc-close\"] },",
		"          \"toc-list\": { type: \"Stack\", props: { direction: \"vertical\", gap: \"sm\" }, children: chapters.slice(0, 16).map((_, i) => `c-${i}`) },",
		"          ...Object.fromEntries(chapters.slice(0, 16).map((ch, i) => [`c-${i}`, { type: \"Button\", props: { label: `${i + 1}. ${ch.title}`, variant: i === chapterIdx ? \"primary\" : \"secondary\" }, on: { press: { action: \"navigate\", params: { query: { chapter: i + 1, toc: \"0\" } } } }, children: [] }])),",
		"          \"toc-close\": { type: \"Button\", props: { label: \"关闭\", variant: \"secondary\" }, on: { press: { action: \"navigate\", params: { query: { toc: \"0\" } } } }, children: [] }",
		"        }",
		"      }",
		"    };",
		"  },",
		"  viewContext(file) {",
		"    const query = file.query || {};",
		"    return `文件：${file.path || file.name}\\n当前章节：${query.chapter || \"1\"}`;",
		"  }",
		"};",
		"",
		"## Available Components Catalog",
		catalogPrompt,
	}, "\n"))

	return strings.Join([]string{
		"[SYSTEM_PROMPT]",
		systemPrompt,
		"",
		"[USER_PROMPT]",
		userMessage,
	}, "\n")
}

func (s *Service) ensureAgentSession(
	ctx context.Context,
	pool *agent.Pool,
	manager *session.Manager,
	current *session.Session,
	agentName string,
	model string,
	mode string,
	effort string,
	fastService string,
	rootAbs string,
	developerInstructions string,
) (agenttypes.Session, *int, error) {
	poolSessionKey := agentPoolSessionKey(current.Key, agentName)
	nextModel := resolveRuntimeModel(current, nil, model)
	nextMode := resolveRuntimeMode(current, mode)
	nextEffort := resolveRuntimeEffort(agentName, current, effort)
	nextFastService := resolveRuntimeFastService(agentName, current, fastService)
	nextPlanMode := current != nil && current.PlanMode
	var binding *session.AgentBinding
	if manager != nil {
		var err error
		binding, err = manager.FindAgentBinding(ctx, current.Key, agentName)
		if err != nil {
			return nil, nil, err
		}
	}
	currentModel := ""
	currentMode := ""
	currentEffort := ""
	currentFastService := ""
	currentPlanMode := false
	if current != nil {
		currentModel = resolveSessionExchangeModel(current)
		if currentModel == "" {
			currentModel = strings.TrimSpace(current.Model)
		}
		currentMode = resolveSessionExchangeMode(current)
		currentEffort = session.InferEffortFromSession(current)
		currentFastService = inferFastServiceFromSession(current)
		currentPlanMode = current.PlanMode
	}
	if existing, ok := pool.Get(poolSessionKey); ok {
		if !shouldReopenSessionForSetting(pool, agentName, currentEffort, nextEffort) &&
			currentFastService == nextFastService {
			if current != nil && currentModel != nextModel {
				log.Printf("[session/model] switch.detected session=%s agent=%s from=%q to=%q action=set_runtime_model", current.Key, agentName, currentModel, nextModel)
				if err := existing.SetModel(ctx, nextModel); err != nil {
					if prober := s.Registry.GetProber(); prober != nil {
						prober.ReportRuntimeFailure(agentName, err)
					}
					log.Printf("[session/model] switch.error session=%s agent=%s model=%q pool_session=%s err=%v", current.Key, agentName, nextModel, poolSessionKey, err)
					return nil, nil, err
				}
				log.Printf("[session/model] switch.done session=%s agent=%s model=%q pool_session=%s", current.Key, agentName, nextModel, poolSessionKey)
			}
			if current != nil && currentMode != nextMode {
				log.Printf("[session/mode] switch.detected session=%s agent=%s from=%q to=%q action=set_runtime_mode", current.Key, agentName, currentMode, nextMode)
				if err := existing.SetMode(ctx, nextMode); err != nil {
					if prober := s.Registry.GetProber(); prober != nil {
						prober.ReportRuntimeFailure(agentName, err)
					}
					log.Printf("[session/mode] switch.error session=%s agent=%s mode=%q pool_session=%s err=%v", current.Key, agentName, nextMode, poolSessionKey, err)
					return nil, nil, err
				}
				log.Printf("[session/mode] switch.done session=%s agent=%s mode=%q pool_session=%s", current.Key, agentName, nextMode, poolSessionKey)
			}
			if current != nil && currentPlanMode != nextPlanMode {
				log.Printf("[session/plan] switch.detected session=%s agent=%s from=%t to=%t action=set_plan_mode", current.Key, agentName, currentPlanMode, nextPlanMode)
				if err := existing.SetPlanMode(ctx, nextPlanMode); err != nil {
					if prober := s.Registry.GetProber(); prober != nil {
						prober.ReportRuntimeFailure(agentName, err)
					}
					log.Printf("[session/plan] switch.error session=%s agent=%s plan_mode=%t pool_session=%s err=%v", current.Key, agentName, nextPlanMode, poolSessionKey, err)
					return nil, nil, err
				}
				log.Printf("[session/plan] switch.done session=%s agent=%s plan_mode=%t pool_session=%s", current.Key, agentName, nextPlanMode, poolSessionKey)
			}
			var currentSeq *int
			if current != nil {
				last := current.AgentCtxSeq[agentName]
				currentSeq = &last
			}
			return existing, currentSeq, nil
		}
		log.Printf("[session/settings] reopen.detected session=%s agent=%s effort_from=%q effort_to=%q fast_service_from=%q fast_service_to=%q action=resume_runtime_session", current.Key, agentName, currentEffort, nextEffort, currentFastService, nextFastService)
		pool.Close(poolSessionKey)
	}

	openCtx := pool.Context()
	if openCtx == nil {
		openCtx = ctx
	}

	openInput := agenttypes.OpenSessionInput{
		SessionKey:            poolSessionKey,
		AgentName:             agentName,
		Model:                 nextModel,
		Mode:                  nextMode,
		Effort:                nextEffort,
		FastService:           nextFastService,
		PlanMode:              nextPlanMode,
		RootPath:              rootAbs,
		DeveloperInstructions: developerInstructions,
		AgentSessionID: strings.TrimSpace(func() string {
			if binding == nil {
				return ""
			}
			return binding.AgentSessionID
		}()),
		AgentCtxSeq: func() int {
			if binding == nil {
				return 0
			}
			return binding.AgentCtxSeq
		}(),
	}
	if openInput.AgentSessionID != "" {
		log.Printf("[session/model] open session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s action=resume_runtime_session agent_session_id=%s agent_ctx_seq=%d", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey, openInput.AgentSessionID, openInput.AgentCtxSeq)
	} else {
		log.Printf("[session/model] open session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s action=open_new_runtime_session", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey)
	}
	sess, err := pool.GetOrCreate(openCtx, openInput)
	var ctxSeqOverride *int
	if err != nil {
		if openInput.AgentSessionID != "" {
			log.Printf("[session/model] resume.error session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s agent_session_id=%s err=%v fallback=open_new_runtime_session", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey, openInput.AgentSessionID, err)
			openInput.AgentSessionID = ""
			openInput.AgentCtxSeq = 0
			sess, err = pool.GetOrCreate(openCtx, openInput)
			if err == nil {
				zero := 0
				ctxSeqOverride = &zero
			}
		}
	}
	if err != nil {
		if prober := s.Registry.GetProber(); prober != nil {
			prober.ReportRuntimeFailure(agentName, err)
		}
		log.Printf("[session/model] open.error session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s err=%v", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey, err)
		return nil, nil, err
	}
	if ctxSeqOverride == nil && binding != nil && openInput.AgentSessionID != "" {
		last := binding.AgentCtxSeq
		ctxSeqOverride = &last
	}
	log.Printf("[session/model] open.done session=%s agent=%s model=%q mode=%q effort=%q fast_service=%q pool_session=%s", current.Key, agentName, nextModel, nextMode, nextEffort, nextFastService, poolSessionKey)
	return sess, ctxSeqOverride, nil
}

func shouldReopenSessionForSetting(pool *agent.Pool, agentName, currentValue, nextValue string) bool {
	currentValue = strings.TrimSpace(currentValue)
	nextValue = strings.TrimSpace(nextValue)
	if currentValue == nextValue {
		return false
	}
	if pool == nil {
		return false
	}
	def, ok := pool.Config().GetAgent(agentName)
	if !ok {
		return false
	}
	protocol := def.Protocol
	if protocol == "" {
		protocol = agent.DefaultProtocol(agentName)
	}
	return protocol == agent.ProtocolCodexSDK || protocol == agent.ProtocolClaudeSDK
}

func resolveRuntimeModel(current *session.Session, runtime agenttypes.Session, requested string) string {
	if model := strings.TrimSpace(requested); model != "" {
		return model
	}
	if runtime != nil {
		if model := strings.TrimSpace(runtime.CurrentModel()); model != "" {
			return model
		}
	}
	if model := resolveSessionExchangeModel(current); model != "" {
		return model
	}
	if current == nil {
		return ""
	}
	return strings.TrimSpace(current.Model)
}

func (s *Service) resolveExchangeModelDisplayName(agentName, model string, rootDir string) string {
	agentName = strings.TrimSpace(agentName)
	model = strings.TrimSpace(model)
	if agentName != "claude" || model == "" || s == nil || s.Registry == nil {
		return ""
	}
	// 同分层 env 解析：display(alias) → effective(真实上游模型)，与 session.go 发送边界同源。
	if claudeName := strings.TrimSpace(agentName); claudeName == "claude" {
		env := s.claudeEnvForExchange(rootDir)
		if effective := claude.ResolveClaudeModelArg(model, env); effective != "" {
			if claude.TierKey(effective) != claude.TierKey(model) {
				return effective // 第三方：直接展示真实上游 ID（如 deepseek-v4-flash[1M]）
			}
			// Anthropic 原生：effective 仍为 tier 名，回退到目录 Name 保持可读（如 Sonnet）
			if prober := s.Registry.GetProber(); prober != nil {
				if status, ok := prober.GetStatus(agentName); ok {
					for _, item := range status.Models {
						if strings.TrimSpace(item.ID) == model {
							if name := strings.TrimSpace(item.Name); name != "" {
								return name
							}
						}
					}
					for _, item := range status.Models {
						if claude.TierKey(item.ID) == claude.TierKey(model) {
							if name := strings.TrimSpace(item.Name); name != "" {
								return name
							}
						}
					}
				}
			}
			return effective
		}
	}
	prober := s.Registry.GetProber()
	if prober == nil {
		return ""
	}
	status, ok := prober.GetStatus(agentName)
	if !ok {
		return ""
	}
	for _, item := range status.Models {
		if strings.TrimSpace(item.ID) == model {
			return strings.TrimSpace(item.Name)
		}
	}
	return ""
}

func (s *Service) ResolveModelDisplayName(agentName, model, rootDir string) string {
	return s.resolveExchangeModelDisplayName(agentName, model, rootDir)
}

func (s *Service) claudeEnvForExchange(rootDir string) map[string]string {
	if s == nil || s.Registry == nil {
		return nil
	}
	var baseEnv map[string]string
	if pool := s.Registry.GetAgentPool(); pool != nil {
		if def, ok := pool.Config().GetAgent("claude"); ok {
			baseEnv = def.Env
		}
	}
	if strings.TrimSpace(rootDir) == "" {
		if roots := s.Registry.ListRoots(); len(roots) > 0 {
			if dir, err := roots[0].RootDir(); err == nil {
				rootDir = dir
			}
		}
	}
	return claude.ClaudeEffectiveEnv(baseEnv, rootDir)
}

func resolveRuntimeEffort(_ string, current *session.Session, requested string) string {
	if effort := strings.TrimSpace(requested); effort != "" {
		return effort
	}
	if effort := session.InferEffortFromSession(current); effort != "" {
		return effort
	}
	return ""
}

func resolveRuntimeFastService(agentName string, current *session.Session, requested string) string {
	if strings.TrimSpace(agentName) != "codex" {
		return ""
	}
	if value := strings.TrimSpace(requested); value != "" {
		return value
	}
	return inferFastServiceFromSession(current)
}

func inferFastServiceFromSession(current *session.Session) string {
	return session.InferFastServiceFromSession(current)
}

func resolveRuntimeMode(current *session.Session, requested string) string {
	if mode := strings.TrimSpace(requested); mode != "" {
		return mode
	}
	if mode := resolveSessionExchangeMode(current); mode != "" {
		return mode
	}
	return ""
}

func resolveSessionExchangeModel(current *session.Session) string {
	if current == nil || len(current.Exchanges) == 0 {
		return ""
	}
	for i := len(current.Exchanges) - 1; i >= 0; i-- {
		model := strings.TrimSpace(current.Exchanges[i].Model)
		if model != "" {
			return model
		}
	}
	return ""
}

func resolveSessionExchangeMode(current *session.Session) string {
	if current == nil || len(current.Exchanges) == 0 {
		return ""
	}
	for i := len(current.Exchanges) - 1; i >= 0; i-- {
		mode := strings.TrimSpace(current.Exchanges[i].Mode)
		if mode != "" {
			return mode
		}
	}
	return ""
}

// userExchangeRepeatTolerance：同一轮用户条目的两个写入者之间的时间窗。
// 外部转录同步会在回合进行中先导入一条（ts 带 Z、来自 transcript），回合结束时
// SendMessage 再写一条（ts 无 Z、time.Now()），实测差 21ms。落在窗内且 role+内容
// 相同 → 判为同一轮重复，不再落库。
// ponytail: 5s 窗。只挡同一次发言的双写，不吞用户隔久了的真实重发（「继续」「ok」必须保留），
// 也不按内容批量去重存量数据。
const userExchangeRepeatTolerance = 5 * time.Second

// exchangeNativeKey 是「这一行在 agent 侧的身份」：user 行看 native_prompt
// （agent 回显的 prompt message id），agent 行看 native_turn（投递单元 id）。
//
// 两把键刻意分开：同一个 id 空间里 user 与 agent 的消息 id 各自独立，
// 混用会让「agent 的回答」把「用户的提问」判成已写过。
func exchangeNativeKey(ex session.Exchange) string {
	if strings.EqualFold(strings.TrimSpace(ex.Role), "user") {
		return strings.TrimSpace(ex.NativePrompt)
	}
	return strings.TrimSpace(ex.NativeTurn)
}

// exchangeIdentityRecorded 判定 target 里是否已有**同一条 agent 原生消息**。
// nativeID 为空时返回 false（没有可证明的东西）。
func exchangeIdentityRecorded(target *session.Session, role, nativeID string) bool {
	nativeID = strings.TrimSpace(nativeID)
	if target == nil || nativeID == "" {
		return false
	}
	for _, ex := range target.Exchanges {
		if !strings.EqualFold(strings.TrimSpace(ex.Role), strings.TrimSpace(role)) {
			continue
		}
		if exchangeNativeKey(ex) == nativeID {
			return true
		}
	}
	return false
}

// exchangeAlreadyRecorded 判定 target 里是否已有这一行。导入侧与发送侧共用，
// 保证两个写入者判据对称（「两把尺」的教训：判据只能有一处实现）。
//
// 判据分两级，**身份优先**（方案 S2c / D10）：
//
//	① nativeID 相等 → 铁证，命中。内容可以不同：导入器会把**相邻同角色**条目合并
//	   （claude importer:1275 / acp importer:238），所以同一条 agent 消息在库里可能是
//	   分两行、在转录里是一行。纯内容判重对这种形态永远漏。
//	② nativeID 为空（历史数据 / 尚未实证的 agent）→ 退回「内容 + ±5s」，并且
//	   **每次会话记一次日志**，让「这里还在猜」这件事可观测（S2d 之后应当归零）。
//
// nativeID 非空但没匹配上 → **不是重复**，放行。这就是 D10 的「对不上跳过 + 日志，
// 永不猜」在写入侧的对偶：不许拿内容去认领一条身份不同的行。
//
// 为什么必须换掉纯内容判重（实测）：本机会话 `1791339665-fb5134041392` 有 9 对
// 「live 写一遍 + import 又写一遍」的 dsh 重复行。ACP 的更新时间戳是**合成**的
// （协议里 `SessionNotification` 没有时间字段），所以「内容 + ±5s」和 `TimestampFloor`
// 双双失效；同一个会话里还有 import + import 的重复（两次导入相隔 20s）同样漏掉。
func exchangeAlreadyRecorded(target *session.Session, role, nativeID, content string, ts time.Time) bool {
	if target == nil {
		return false
	}
	if exchangeIdentityRecorded(target, role, nativeID) {
		return true
	}
	if strings.TrimSpace(nativeID) != "" {
		// 有身份就不许猜 —— 否则「同一条消息分两行落库、其中一行 native id 被清掉
		// （repoint）」这类行会被内容误判吞掉。
		return false
	}
	if ts.IsZero() {
		return false
	}
	noteIdentitylessGuess(target.Key)
	// 空正文不参与内容判重：那是「这一轮只有工具调用、没有文本」的占位行，
	// 不是某一句具体的话，拿它去认领另一条空行等于把无主的行按无主吞掉。
	// 有 native id 的空行照样按身份判重 —— 那才是它唯一的真身份。
	if strings.TrimSpace(content) == "" {
		return false
	}
	for _, ex := range target.Exchanges {
		if ex.Role != role || ex.Timestamp.IsZero() {
			continue
		}
		if strings.TrimSpace(ex.Content) == "" {
			continue
		}
		if !sameRecordedExchangeContent(role, ex.Content, content) {
			continue
		}
		if diff := ex.Timestamp.Sub(ts); diff > -userExchangeRepeatTolerance && diff < userExchangeRepeatTolerance {
			return true
		}
	}
	return false
}

// identitylessGuessSessions 只为让「判重还在靠内容猜」每个会话最多打一次日志：
// 判重在每个导入行上都会调用，不能每行一条。
var identitylessGuessSessions sync.Map

func noteIdentitylessGuess(sessionKey string) {
	if strings.TrimSpace(sessionKey) == "" {
		return
	}
	if _, seen := identitylessGuessSessions.LoadOrStore(sessionKey, struct{}{}); !seen {
		log.Printf("[session/identity] session=%s 判重退回内容猜测（这一行没有 native id）——"+
			"历史数据或该 agent 尚未接线；S2d 之后应当归零", sessionKey)
	}
}

// persistUserTurnExchange 在**回合开始前**把本轮用户消息落盘，返回它真实的 seq 与 turn_id。
//
// 为什么必须早落：广播给客户端的 seq 若是「预测」而落盘发生在回合结束，就会出现
// 「客户端已认定该行持久化、而窗口 maxSeq 与读接口都还看不到它」的窗口期。前端
// overlay 的「seq<=latestSeq 即让位给窗口」规则（SessionViewer.tailOverlay）会在
// 任何一次重锚定/翻页时把这行丢掉，表现为 agent 执行中自己的消息从列表消失、
// 点同步也刷不回来（2026-09-28 实测）。早落盘让 seq 从预测变成事实，两条判据不再打架。
//
// 判重与导入侧共用 exchangeAlreadyRecorded：转录导入器是另一个写入者，判据必须对称。
// 命中判重（导入器已抢先写）时返回已存在那条的 seq。
//
// 返回的 turn_id 是本轮的 MindFS 权威身份（方案 §3.1）：**没有客户端入口可带**
// （Stream.Send 只收 string），所以这里服务端 mint UUIDv4（C2）。助手行与块流
// 必须复用同一个 turn_id，否则读侧挂不回轮次。
func persistUserTurnExchange(ctx context.Context, manager *session.Manager, current *session.Session, content, agent, mode, effort, fastService string, promptMessageID bool, ts time.Time) (int, string, error) {
	exchangeCtx := session.WithExchangeSource(ctx, session.ExchangeSourceLive)
	// mint turn_id 在判重**之后**：判重命中时这一轮已经有了自己的 id（要从已有行
	// 取回来），mint 一个新的只是浪费。判重不看身份而看内容，是因为实时侧此刻
	// 还不知道 agent 侧的 id —— 行还没发出去。
	if exchangeAlreadyRecorded(current, "user", "", content, ts) {
		return recordedUserExchangeSeq(current, content, ts), recordedUserExchangeTurnID(current, content, ts), nil
	}
	// 与 Manager.addExchangeForAgentAt 的 seq 分配同一公式（最大 seq + 1，不按缓存长度）。
	// 这里先算出来是为了把 turn_seq 一起写进身份 —— 它是「打开该轮的 user 行 seq」。
	userSeq := session.MaxExchangeSeq(current.Exchanges) + 1
	turnID := newTurnID()
	// native_prompt = turn_id，**只对有 prompt messageId 这层身份的 agent**
	// （promptMessageID 由 importer 的 UsesPromptMessageID 给出，见
	// Service.agentHasPromptMessageID）：本行写下去之后，ACP 侧会把同一个 id 当作
	// prompt messageId 递出去并原样回显，导入侧于是能从转录里读回同一个值 ——
	// 这就是实时写入者与导入写入者之间**唯一的共同键**。
	// claude/codex 没有这层（身份是转录条目的 uuid / 尚无），写了就是一个谁也
	// 对不上的假身份，宁可留空（方案 §3.1「可空、可后补」）。
	nativePrompt := ""
	if promptMessageID {
		nativePrompt = turnID
	}
	exchangeCtx = session.WithExchangeIdentity(exchangeCtx, session.ExchangeIdentity{
		TurnID:       turnID,
		TurnSeq:      userSeq,
		NativePrompt: nativePrompt,
	})
	if err := manager.AddExchangeForAgentAt(exchangeCtx, current, "user", content, agent, mode, effort, fastService, ts); err != nil {
		return 0, "", err
	}
	// 双写块流（方案 S2a）：用户消息也要有块 —— 块流是唯一正文载体（D1），
	// 少写这一条，S2d 删掉 Content 之后用户自己的话就没了。
	// 失败不拖垮本轮：主日志已经落过盘，块流可在 S2b 前重放补齐。
	if err := persistTurnBlocks(ctx, manager, current.Key, userSeq, turnID, content, nil); err != nil {
		log.Printf("[session] persist.user.blocks.error session=%s seq=%d err=%v", current.Key, userSeq, err)
	}
	// AddExchangeForAgentAt 内部重新取会话并回写缓存（manager.addExchangeForAgentAt），
	// current 与缓存是同一指针，所以新行已挂在 current.Exchanges 尾部。
	// 按内容回查而不是直接取尾部：尾部那条未必就是本函数刚写的那条——一旦将来出现
	// 不在 sendLock 下写同一会话的路径，广播出去就会变成别人的 seq，正是本次要修的那类 bug。
	return recordedUserExchangeSeq(current, content, ts), recordedUserExchangeTurnID(current, content, ts), nil
}

// recordedUserExchangeTurnID 返回判重命中的那条 user 行的 turn_id；没有则空串。
// 空串是合法的：历史行本来就没有 turn_id，不能因此报错（方案 §3.1「可空、可后补」）。
func recordedUserExchangeTurnID(target *session.Session, content string, ts time.Time) string {
	if target == nil {
		return ""
	}
	for i := len(target.Exchanges) - 1; i >= 0; i-- {
		ex := target.Exchanges[i]
		if ex.Role != "user" || ex.Timestamp.IsZero() {
			continue
		}
		if !sameRecordedExchangeContent("user", ex.Content, content) {
			continue
		}
		if diff := ex.Timestamp.Sub(ts); diff > -userExchangeRepeatTolerance && diff < userExchangeRepeatTolerance {
			return ex.TurnID
		}
	}
	return ""
}

// newTurnID mint 一个 UUIDv4 作为 turn_id（方案 D4/C2）。
//
// 自己拼而不引 google/uuid：只需要「格式正确的随机 UUID」，而 randomHex 已经在用
// 同一条 crypto/rand 路径。三处服务端发起的轮次（定时任务 / 看板阶段 / 子会话）
// 都经 SendMessage 走到这里，所以 mint 点只有一处。
func newTurnID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return ""
	}
	buf[6] = (buf[6] & 0x0f) | 0x40 // version 4
	buf[8] = (buf[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// turnBlockStep 是 S2a 双写期写在块上的步序。
//
// **这是占位值，不是最终语义。** 方案 §3.3 的 `step` 是「按 agent 的消息投递单元递增」
// （claude = 一条 AssistantMessage，dsh/ACP = 一次 messageId 变更）。S2a 的块在**轮末
// 一次性**落盘，此刻投递边界已经过去、没有被记录下来，所以整轮只能算作一个投递单元。
//
// 为什么不按 §3.3 写哨兵 -1：读取侧的规则是「忽略 step<0 的版本」，写了 -1 就等于
// 把 S2a 期间落下的块**全部作废**，S2b 切读路径的那一刻正文会凭空消失。
// 真按投递单元切分是 S2c 的工作（与 ACP messageId 接线一起做），届时用
// `(seq,pos)` + `revision+1` 覆盖成真值，不需要迁移历史。
const turnBlockStep = 1

// persistTurnBlocks 把一轮的正文 + aux 编译成块流追加进去（方案 S2a 双写）。
//
// 为什么放在 usecase 而不是 Manager.appendExchange 里：块序必须与前端渲染序一致
// （见 session.BuildTurnBlocks），而那需要「正文 + aux 的交错上下文」——
// appendExchange 只看到一段字符串，appendExchangeAux 只看到一条 aux，都拼不出完整顺序。
func persistTurnBlocks(ctx context.Context, manager *session.Manager, key string, seq int, turnID, content string, aux []session.ExchangeAux) error {
	if manager == nil || seq <= 0 {
		return nil
	}
	blocks := session.BuildTurnBlocks(seq, turnBlockStep, turnID, content, aux)
	if len(blocks) == 0 {
		return nil
	}
	return manager.AppendBlocks(ctx, key, blocks)
}

// recordedUserExchangeSeq 返回判重命中的那条已有 user 行的 seq；找不到时返回 0。
func recordedUserExchangeSeq(target *session.Session, content string, ts time.Time) int {
	if target == nil {
		return 0
	}
	for i := len(target.Exchanges) - 1; i >= 0; i-- {
		ex := target.Exchanges[i]
		if ex.Role != "user" || ex.Timestamp.IsZero() {
			continue
		}
		if !sameRecordedExchangeContent("user", ex.Content, content) {
			continue
		}
		if diff := ex.Timestamp.Sub(ts); diff > -userExchangeRepeatTolerance && diff < userExchangeRepeatTolerance {
			return ex.Seq
		}
	}
	return 0
}

// sameRecordedExchangeContent 判断两条同角色内容是否「同一条」。
// 默认要求完全相等；另容忍两类双写差异（同一轮由实时路径与转录导入各写一次）：
//
//  1. 空白归一化后相等 —— 两个写入者对段落空行的处理不同。实测 2026-09-14 16:18 的
//     一轮：实时版 1765 字、导入版 1761 字，逐字符 diff 只有 2 处 "\n\n" 之差
//     （相似度 0.9989），只比字面量必然漏判。
//  2. 助手侧的前缀关系 —— 两边抓到的快照长度可能差很多（实测实时 21 字 seq=128、
//     转录导入 556 字 seq=129＝前者 + "\n\nAPI Error: 502 …"）。
//
// 用户侧不吃前缀容忍：「继续」是「继续吧」的前缀，但那是两句不同的话。前缀关系还要求
// 较短一侧够长（≥16 字），避免「好」/「好的，我这就去…」这种短应答误判。
// 两条判据都只在 ±5s 容忍窗内生效（见 exchangeAlreadyRecorded）。
func sameRecordedExchangeContent(role, a, b string) bool {
	if a == b {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	na, nb := normalizeExchangeContent(a), normalizeExchangeContent(b)
	if na == nb {
		return true
	}
	if role != "agent" {
		return false
	}
	shorter := na
	if len([]rune(nb)) < len([]rune(shorter)) {
		shorter = nb
	}
	if len([]rune(shorter)) < 16 {
		return false
	}
	return strings.HasPrefix(na, nb) || strings.HasPrefix(nb, na)
}

// normalizeExchangeContent 用于「同一条消息的两种渲染」比较：先剥掉 CLI 自注入的标记
// （名单见 agenttypes.TranscriptNoisePrefixes，与导入侧共用），再抹掉所有空白。
//
// 抹掉而不是折叠：折叠成单空格是错的 —— 实时路径会把 \n\n 插进 Markdown 标记内部
// （实测 `**支具\n\n+康复总市场**` vs 导入版 `**支具+康复总市场**`），折叠后前者多一个
// 空格，仍判不相等。代价是 "a b" 与 "ab" 视为相同，但这只发生在 ±5s 容忍窗内，
// 同一轮的两个写入者本来就该是同一条。
func normalizeExchangeContent(s string) string {
	stripped := agenttypes.StripTranscriptNoisePrefixes(s)
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, stripped)
}

func (s *Service) SendMessage(ctx context.Context, in SendMessageInput) error {
	if err := s.ensureRegistry(); err != nil {
		return err
	}
	userTimestamp := sendMessageUserTimestamp(in, time.Now().UTC())
	sendLock := getSessionSendLock(in.Key)
	sendLock.Lock()
	defer sendLock.Unlock()
	turnCtx, turnCancel := context.WithCancel(ctx)
	registerActiveTurn(in.RootID, in.Key, turnCancel)
	defer unregisterActiveTurn(in.RootID, in.Key)
	s.applyMessageRuntimeDefaults(&in)
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return err
	}
	current, err := manager.Get(ctx, in.Key, 0)
	if err != nil {
		return err
	}
	if in.PlanMode != nil && current.PlanMode != *in.PlanMode {
		if err := manager.UpdatePlanMode(ctx, current, *in.PlanMode); err != nil {
			return err
		}
	}
	root := manager.Root()
	managedRootAbs, _ := root.RootDir()
	rootAbs := managedRootAbs
	if runtimeRootPath := strings.TrimSpace(in.RuntimeRootPath); runtimeRootPath != "" {
		rootAbs = filepath.Clean(runtimeRootPath)
	}
	resolvedMode := resolveRuntimeMode(current, in.Mode)
	resolvedFastService := resolveRuntimeFastService(in.Agent, current, in.FastService)
	// 可在任何东西落盘之前就知道它会失败的校验，必须先跑。模型不被支持时本轮压根不会
	// 开始，user 行也就没有存在过——否则会留下一条用户没等到任何回复的孤立消息。
	if current.Type != session.TypeCommand {
		if err := s.validateAgentModel(in.Agent, in.Model); err != nil {
			log.Printf("[session/model] validate.error root=%s session=%s agent=%s model=%q err=%v", in.RootID, in.Key, strings.TrimSpace(in.Agent), strings.TrimSpace(in.Model), err)
			return err
		}
	}
	// 本轮用户消息**在回合开始前**落盘：广播出去的 seq 因此是「已落库的真值」而不是预测。
	// 晚落盘会留下一个窗口期——客户端已按 seq 认定该行持久化，而窗口 maxSeq 与读接口还
	// 看不到它，前端 overlay 会在任意一次重锚定/翻页时把这行丢掉（2026-09-28 实测：
	// agent 执行中自己的消息从列表消失，点同步也刷不回来，回合结束才自愈）。
	isInitial := len(current.Exchanges) == 0
	linesBeforeThisTurn := len(current.Exchanges)
	baseExchangeSeq := session.MaxExchangeSeq(current.Exchanges)
	// 从这一刻起这个会话的持久化归实时路径（"谁驱动，谁落盘"），导入器不再常规增量
	// 同步——判据是推导的，见 session.SessionIsLiveOwned。
	userExchangeSeq, turnID, err := persistUserTurnExchange(ctx, manager, current, in.Content, in.Agent, resolvedMode, resolveRuntimeEffort(in.Agent, current, in.Effort), resolvedFastService, s.agentHasPromptMessageID(in.Agent), userTimestamp)
	if err != nil {
		log.Printf("[session] persist.user.error root=%s session=%s agent=%s err=%v", in.RootID, in.Key, in.Agent, err)
		return err
	}
	// 把本轮的 MindFS turn_id 递给 agent（方案 D3）：ACP 的 `PromptRequest.MessageId`
	// 就是它，agent 应当原样回显成 `userMessageId`。于是实时路径写的 user 行
	// （`native_prompt = turn_id`）与导入路径从转录读回的 id 是同一个，两边能对上，
	// 判重才不必猜内容。没有这层时导入侧拿到的 user 行与实时侧没有任何共同键，
	// 同一轮只能靠「内容相等 + ±5s」猜 —— 实测 2026-10-07 某 live-owned 会话
	// Full 同步把 9 对轮次又写了一遍，投影只能在展示时折叠。
	turnCtx = agenttypes.WithPromptTurnID(turnCtx, turnID)
	// 中途换 agent 时，新 agent 的绑定要等本轮跑完才由 UpdateAgentState 写——这中间徽标
	// 会落回「AI」占位。这里补一条，已存在绑定则跳过，不碰已有转录 id。
	if err := manager.EnsureAgentBinding(ctx, current.Key, in.Agent); err != nil {
		log.Printf("[session] ensure.binding.error root=%s session=%s agent=%s err=%v", in.RootID, in.Key, in.Agent, err)
	}
	if in.OnStart != nil {
		in.OnStart(MessageStart{
			Model:            in.Model,
			ModelDisplayName: s.resolveExchangeModelDisplayName(in.Agent, in.Model, rootAbs),
			Mode:             resolvedMode,
			Effort:           in.Effort,
			FastService:      resolvedFastService,
			BaseExchangeSeq:  baseExchangeSeq,
			UserExchangeSeq:  userExchangeSeq,
		})
	}
	if current.Type == session.TypeCommand {
		return s.sendCommandMessage(turnCtx, in, manager, current, turnID, userExchangeSeq)
	}
	agentPool := s.Registry.GetAgentPool()
	if agentPool == nil {
		return nil
	}
	watcher, _ := s.Registry.GetFileWatcher(in.RootID, manager)
	if watcher != nil {
		watcher.RegisterSession(current.Key)
		watcher.MarkSessionActive(current.Key)
	}
	planMode := current != nil && current.PlanMode

	developerInstructions := ""
	includeReplyTipsInUserMessage := isInitial
	if isInitial && agentPool.SupportsDeveloperInstructions(in.Agent) {
		developerInstructions = replyTips
		includeReplyTipsInUserMessage = false
	}
	sess, agentCtxSeq, err := s.ensureAgentSession(turnCtx, agentPool, manager, current, in.Agent, in.Model, in.Mode, in.Effort, in.FastService, rootAbs, developerInstructions)
	if err != nil {
		return err
	}
	setActiveTurnSession(in.RootID, current.Key, sess)

	prompt := s.BuildPrompt(BuildPromptInput{
		Session:                       current,
		Manager:                       manager,
		Agent:                         in.Agent,
		Message:                       in.Content,
		ClientContext:                 in.ClientCtx,
		AgentCtxSeq:                   agentCtxSeq,
		IsInitial:                     isInitial,
		IncludeReplyTipsInUserMessage: includeReplyTipsInUserMessage,
		LinesBeforeThisTurn:           &linesBeforeThisTurn,
	})
	var responseText string
	sawAssistantChunk := false
	var lastContextWindow agenttypes.ContextWindow
	var turnTokenUsage *agenttypes.TokenUsage
	// turnNativeMessageID 是本轮最后一个 agent 原生消息 id（ACP `messageId`），
	// 回合结束时写进助手行的 `native_turn`。没有它，导入器重放同一轮时只能靠内容
	// 认领 —— 而导入器会把相邻同角色条目合并，内容必然不同（实测 2026-10-07 某
	// live-owned 会话 Full 同步写出 9 对重复轮次）。claude/codex 不提供这层，保持空串。
	var turnNativeMessageID string
	// 用户行已在上方落盘并占位，所以助手行是 max(seq)+1（不是 +2）。
	// 文件与缓存漂移时长度 ≠ 最大 seq，按长度预测会把 aux 挂到别的 exchange 上。
	plannedAssistantSeq := session.MaxExchangeSeq(current.Exchanges) + 1
	auxBuffer := make([]session.ExchangeAux, 0, 8)
	defer manager.ClearPendingExchangeAux(context.Background(), current.Key)
	var thoughtBuffer strings.Builder
	currentThoughtID := ""
	flushThought := func() {
		thought := thoughtBuffer.String()
		if strings.TrimSpace(thought) == "" {
			thoughtBuffer.Reset()
			currentThoughtID = ""
			return
		}
		thoughtID := currentThoughtID
		thoughtBuffer.Reset()
		currentThoughtID = ""
		auxBuffer = append(auxBuffer, session.ExchangeAux{
			Seq:       plannedAssistantSeq,
			Line:      currentAssistantLine(responseText),
			Thought:   thought,
			ThoughtID: thoughtID,
		})
	}
	lastResponseUpdateType := ""
	claudeSubagents := newClaudeSubagentRouter(subagentSessionInput{
		RootID:      in.RootID,
		Parent:      current,
		Agent:       in.Agent,
		Model:       in.Model,
		Mode:        in.Mode,
		Effort:      in.Effort,
		FastService: in.FastService,
		RootAbs:     rootAbs,
		Manager:     manager,
		OnCreated:   in.OnSubSessionCreated,
		OnUpdate:    in.OnSubSessionUpdate,
	})
	attachSessionUpdates := func(runtime agenttypes.Session) {
		runtime.OnUpdate(func(update agenttypes.Event) {
			update = normalizeAgentUpdatePaths(root, update)
			if claudeSubagents.Handle(context.Background(), update) {
				return
			}
			switch update.Type {
			case agenttypes.EventTypeThoughtChunk:
				if chunk, ok := update.Data.(agenttypes.ThoughtChunk); ok && chunk.Content != "" {
					if currentThoughtID == "" {
						currentThoughtID = "thought-" + randomHex(8)
					}
					chunk.ID = currentThoughtID
					update.Data = chunk
					thoughtBuffer.WriteString(chunk.Content)
				}
			case agenttypes.EventTypeToolCall, agenttypes.EventTypeToolUpdate, agenttypes.EventTypeTodoUpdate, agenttypes.EventTypePlanUpdate, agenttypes.EventTypeCompact, agenttypes.EventTypeMessageChunk, agenttypes.EventTypeMessageDone:
				flushThought()
			}
			clientUpdate := compactAgentUpdate(update)
			if update.Type == agenttypes.EventTypeToolCall || update.Type == agenttypes.EventTypeToolUpdate {
				if toolCall, ok := update.Data.(agenttypes.ToolCall); ok && toolCall.IsWriteOperation() {
					for _, path := range toolCall.GetAffectedPaths() {
						if watcher == nil {
							continue
						}
						recordPath := relatedFileRecordPath(managedRootAbs, rootAbs, path)
						if update.Type == agenttypes.EventTypeToolCall && toolCall.Status == "running" {
							watcher.RecordPendingWrite(current.Key, recordPath)
						}
						if update.Type == agenttypes.EventTypeToolUpdate || toolCall.Status == "complete" {
							watcher.RecordSessionFile(current.Key, recordPath)
						}
					}
				}
				if toolCall, ok := update.Data.(agenttypes.ToolCall); ok {
					s.ensureSubagentSessions(context.Background(), subagentSessionInput{
						RootID:      in.RootID,
						Parent:      current,
						Agent:       in.Agent,
						Model:       in.Model,
						Mode:        in.Mode,
						Effort:      in.Effort,
						FastService: in.FastService,
						RootAbs:     rootAbs,
						Pool:        agentPool,
						Manager:     manager,
						ToolCall:    toolCall,
						OnCreated:   in.OnSubSessionCreated,
						OnUpdate:    in.OnSubSessionUpdate,
					})
					if shouldPersistToolCallAux(toolCall) {
						toolCallCopy := toolCall
						auxBuffer = append(auxBuffer, session.ExchangeAux{
							Seq:      plannedAssistantSeq,
							Line:     currentAssistantLine(responseText),
							ToolCall: &toolCallCopy,
						})
						_ = manager.UpsertPendingExchangeAux(context.Background(), current.Key, session.ExchangeAux{ToolCall: &toolCallCopy})
					}
				}
			}
			if update.Type == agenttypes.EventTypePlanUpdate {
				if plan, ok := update.Data.(agenttypes.PlanUpdate); ok && strings.TrimSpace(plan.Content) != "" {
					planCopy := plan
					auxBuffer = append(auxBuffer, session.ExchangeAux{
						Seq:  plannedAssistantSeq,
						Line: currentAssistantLine(responseText),
						Plan: &planCopy,
					})
				}
			}
			if update.Type == agenttypes.EventTypeTodoUpdate {
				if todo, ok := update.Data.(agenttypes.TodoUpdate); ok && len(todo.Items) > 0 {
					todoCopy := todo
					auxBuffer = append(auxBuffer, session.ExchangeAux{
						Seq:  plannedAssistantSeq,
						Line: currentAssistantLine(responseText),
						Todo: &todoCopy,
					})
				}
			}
			if update.Type == agenttypes.EventTypeCompact {
				if compact, ok := update.Data.(agenttypes.CompactNotice); ok {
					compactCopy := compact
					auxBuffer = append(auxBuffer, session.ExchangeAux{
						Seq:     plannedAssistantSeq,
						Line:    currentAssistantLine(responseText),
						Compact: &compactCopy,
					})
				}
			}
			if update.Type == agenttypes.EventTypeMessageChunk {
				if chunk, ok := update.Data.(agenttypes.MessageChunk); ok {
					sawAssistantChunk = true
					// 记住**最后一个**带 messageId 的文本块的 id（方案 S2c）：一轮里
					// agent 会发多条原生消息，块流按它切 step；行级身份取**最后一条** ——
					// 与导入侧取法一致（ACP importer 的 append 同样在 messageId 变化时
					// 开新行）。两端取法必须一样，否则同一轮永远「对不上」。
					if strings.TrimSpace(chunk.MessageID) != "" {
						turnNativeMessageID = strings.TrimSpace(chunk.MessageID)
					}
					responseText = appendResponseChunk(responseText, lastResponseUpdateType, chunk.Content)
					lastResponseUpdateType = string(update.Type)
				}
			} else if update.Type == agenttypes.EventTypeMessageDone {
				if done, ok := update.Data.(agenttypes.MessageDone); ok {
					lastContextWindow = done.ContextWindow
					turnTokenUsage = done.TokenUsage
				}
			} else if update.Type == agenttypes.EventTypeThoughtChunk ||
				update.Type == agenttypes.EventTypeToolCall ||
				update.Type == agenttypes.EventTypeToolUpdate ||
				update.Type == agenttypes.EventTypeTodoUpdate ||
				update.Type == agenttypes.EventTypePlanUpdate ||
				update.Type == agenttypes.EventTypeCompact {
				lastResponseUpdateType = string(update.Type)
			}
			if watcher != nil {
				watcher.MarkSessionActive(current.Key)
			}
			if in.OnUpdate != nil {
				in.OnUpdate(clientUpdate)
			}
		})
	}
	sendWithAttachedUpdates := func(runtime agenttypes.Session, content string) error {
		attachSessionUpdates(runtime)
		finishUse := agentPool.BeginSessionUse(agentPoolSessionKey(current.Key, in.Agent))
		defer finishUse()
		return runtime.SendMessage(turnCtx, content)
	}
	sendErr := sendWithAttachedUpdates(sess, prompt)
	if sendErr != nil && !isCanceledTurnError(sendErr) {
		if isNonRecoverableAgentError(sendErr) {
			log.Printf("[session] turn.send.non_recoverable root=%s session=%s agent=%s action=fail_without_recovery err=%v", in.RootID, current.Key, in.Agent, sendErr)
			cancelRuntimeAfterNonRecoverableError(sess, agentPool, in.Agent, sendErr)
		} else if !sawAssistantChunk {
			log.Printf("[session] turn.send.no_response root=%s session=%s agent=%s action=fail_without_recovery", in.RootID, current.Key, in.Agent)
		} else {
			if in.OnUpdate != nil {
				in.OnUpdate(agenttypes.Event{
					Type: agenttypes.EventTypeRecovery,
					Data: agenttypes.RecoveryStatus{Message: "遇到错误，重试中..."},
				})
			}
			recoveredSess, recoveredErr := s.recoverAgentTurn(turnCtx, SendRecoveryInput{
				RootID:             in.RootID,
				SessionKey:         current.Key,
				Manager:            manager,
				Current:            current,
				AgentName:          in.Agent,
				Model:              in.Model,
				Mode:               in.Mode,
				Effort:             in.Effort,
				FastService:        in.FastService,
				PlanMode:           planMode,
				RootAbs:            rootAbs,
				CurrentSession:     sess,
				Prompt:             prompt,
				SawAssistantChunk:  sawAssistantChunk,
				SendWithAttachment: sendWithAttachedUpdates,
			})
			if recoveredErr != nil {
				sendErr = recoveredErr
			} else {
				sess = recoveredSess
				sendErr = nil
			}
		}
	}
	flushThought()
	claudeSubagents.FinishAll()
	if sendErr != nil && !isCanceledTurnError(sendErr) {
		log.Printf("[session] turn.send.error root=%s session=%s agent=%s err=%v", in.RootID, current.Key, in.Agent, sendErr)
	}
	resolvedModel := resolveRuntimeModel(current, sess, in.Model)
	resolvedEffort := resolveRuntimeEffort(in.Agent, current, in.Effort)
	resolvedFastService = resolveRuntimeFastService(in.Agent, current, in.FastService)
	if prefs := s.Registry.GetPreferences(); prefs != nil {
		if changed, err := prefs.UpdateAgentDefaultsIfChanged(in.Agent, resolvedModel, resolvedEffort, resolvedFastService); err != nil {
			log.Printf("[preferences] agent_defaults.update.error agent=%s err=%v", strings.TrimSpace(in.Agent), err)
		} else if changed {
			log.Printf("[preferences] agent_defaults.update.done agent=%s model=%q effort=%q fast_service=%q", strings.TrimSpace(in.Agent), resolvedModel, resolvedEffort, resolvedFastService)
			if in.OnAgentDefaultsChanged != nil {
				in.OnAgentDefaultsChanged(strings.TrimSpace(in.Agent))
			}
		}
	}
	modelDisplayName := s.resolveExchangeModelDisplayName(in.Agent, resolvedModel, rootAbs)
	exchangeCtx := session.WithExchangeModelDisplayName(ctx, modelDisplayName)
	exchangeCtx = session.WithExchangeSource(exchangeCtx, session.ExchangeSourceLive)
	// 助手行复用本轮 user 行 mint 的 turn_id（方案 §3.3「turn_id 落盘即定」）：
	// 两行必须同身份，块流才能把正文挂回轮次。
	exchangeCtx = session.WithExchangeIdentity(exchangeCtx, session.ExchangeIdentity{
		TurnID:  turnID,
		TurnSeq: userExchangeSeq,
		// native_turn = 本轮最后一个 agent 原生消息 id。这是助手行在 agent 侧的身份，
		// 导入器重放同一条消息时读回的就是它 —— 两个写入者由此对上（方案 S2c/D3）。
		// claude/codex 不提供这层，保持空串（§3.1「可空、可后补」）。
		NativeTurn: turnNativeMessageID,
	})
	agentExchangeCtx := session.WithExchangeTokenUsage(exchangeCtx, turnTokenUsage)
	if err := manager.UpdateModel(ctx, current, resolvedModel); err != nil {
		return err
	}
	// 用户行已在回合开始前落盘（persistUserTurnExchange），这里只补助手行。
	if err := manager.AddExchangeForAgent(agentExchangeCtx, current, "agent", responseText, in.Agent, resolvedMode, resolvedEffort, resolvedFastService); err != nil {
		log.Printf("[session] persist.agent.error root=%s session=%s agent=%s err=%v", in.RootID, current.Key, in.Agent, err)
		return err
	}
	if err := manager.UpdateLastContextWindow(ctx, current, lastContextWindow); err != nil {
		return err
	}
	persistedAux := dedupeExchangeAuxBuffer(auxBuffer)
	for i := range persistedAux {
		persistedAux[i] = hydratePendingToolCallAux(ctx, manager, current.Key, persistedAux[i])
		if err := manager.AddExchangeAux(ctx, current.Key, persistedAux[i]); err != nil {
			return err
		}
	}
	// 双写块流（方案 S2a）：块流与旧 Content/aux **并存**，读路径仍走旧格式。
	// 顺序：先落盘（AddExchangeForAgent 已写主日志）再写块，读侧才不会看到「有轮次没正文」。
	// 失败不拖垮本轮 —— 正文已经落过盘，块流可在 S2b 前重放补齐。
	agentSeq := session.MaxExchangeSeq(current.Exchanges)
	if err := persistTurnBlocks(ctx, manager, current.Key, agentSeq, turnID, responseText, persistedAux); err != nil {
		log.Printf("[session] persist.blocks.error root=%s session=%s agent=%s seq=%d err=%v", in.RootID, current.Key, in.Agent, agentSeq, err)
	}
	// AgentCtxSeq 记的是「agent 已经看到多少行会话历史」，口径是**已落盘的总行数**
	// （与 external_sessions.go:415 的 len(latest.Exchanges)、
	// 子会话的 contextLineCount(child.Exchanges) 一致）。此刻 user 行（persistUserTurnExchange）
	// 与助手行（AddExchangeForAgent）都已落盘，所以是 linesBeforeThisTurn+2。
	// 早先只记 linesBeforeThisTurn，两行都漏：下一轮 prependSwitchHint 的
	// total=linesBeforeThisTurn、last=上一轮记的值，delta 恒为 2 > 0，切 agent 提示
	// 于是在每个正常回合都被重复注入（日志实测 12 小时 140 次），而不是只在真切换时出现。
	if err := manager.UpdateAgentState(ctx, current, in.Agent, linesBeforeThisTurn+2, sess.SessionID()); err != nil {
		return err
	}

	prober := s.Registry.GetProber()
	if sendErr != nil && !isCanceledTurnError(sendErr) {
		if prober != nil {
			prober.ReportRuntimeFailure(in.Agent, sendErr)
		}
		return sendErr
	} else if prober != nil {
		prober.ReportSuccess(in.Agent)
	}
	return nil
}

func (s *Service) RunTransientSlashCommand(ctx context.Context, in RunTransientSlashCommandInput) error {
	if err := s.ensureRegistry(); err != nil {
		return err
	}
	command := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(in.Command)), "/")
	agentName := strings.TrimSpace(in.Agent)
	if agentName != "codex" || (command != "status" && command != "login") {
		return errors.New("unsupported transient slash command")
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		key = fmt.Sprintf("transient-%s-%d", command, time.Now().UnixNano())
	}
	sendLock := getSessionSendLock(key)
	sendLock.Lock()
	defer sendLock.Unlock()

	turnCtx, turnCancel := context.WithCancel(ctx)
	registerActiveTurn(in.RootID, key, turnCancel)
	defer unregisterActiveTurn(in.RootID, key)

	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return err
	}
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		current = &session.Session{
			Key:         key,
			Type:        session.TypeChat,
			Model:       strings.TrimSpace(in.Model),
			AgentCtxSeq: map[string]int{},
			Name:        "Transient slash command",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
	} else {
		if current.Type == session.TypeCommand {
			return errors.New("slash commands are not supported for command sessions")
		}
		if currentAgent := strings.TrimSpace(session.InferAgentFromSession(current)); currentAgent != "" && currentAgent != agentName {
			return errors.New("slash command agent does not match session agent")
		}
	}
	if err := s.validateAgentModel(agentName, in.Model); err != nil {
		return err
	}
	agentPool := s.Registry.GetAgentPool()
	if agentPool == nil {
		return errors.New("agent pool not configured")
	}
	root := manager.Root()
	rootAbs, _ := root.RootDir()
	sess, _, err := s.ensureAgentSession(turnCtx, agentPool, manager, current, agentName, in.Model, in.Mode, in.Effort, in.FastService, rootAbs, "")
	if err != nil {
		return err
	}
	setActiveTurnSession(in.RootID, current.Key, sess)

	sess.OnUpdate(func(update agenttypes.Event) {
		update = normalizeAgentUpdatePaths(root, update)
		clientUpdate := compactAgentUpdate(update)
		if in.OnUpdate != nil {
			in.OnUpdate(clientUpdate)
		}
	})

	if command == "login" {
		loginSess, ok := sess.(codexDeviceCodeLoginSession)
		if !ok {
			return errors.New("codex login is not supported by this runtime")
		}
		err = loginSess.LoginChatGPTDeviceCode(turnCtx)
	} else {
		finishUse := agentPool.BeginSessionUse(agentPoolSessionKey(current.Key, agentName))
		defer finishUse()
		err = sess.SendMessage(turnCtx, "/"+command)
	}
	if err != nil && !isCanceledTurnError(err) {
		if prober := s.Registry.GetProber(); prober != nil {
			prober.ReportRuntimeFailure(agentName, err)
		}
		return err
	}
	return err
}

type subagentSessionInput struct {
	RootID      string
	Parent      *session.Session
	Agent       string
	Model       string
	Mode        string
	Effort      string
	FastService string
	RootAbs     string
	Pool        *agent.Pool
	Manager     *session.Manager
	ToolCall    agenttypes.ToolCall
	OnCreated   func(*session.Session)
	OnUpdate    func(sessionKey string, update agenttypes.Event)
}

type claudeSubagentRouter struct {
	in       subagentSessionInput
	byRef    map[string]*claudeSyntheticSubagent
	children []*claudeSyntheticSubagent
}

type claudeSyntheticSubagent struct {
	key     string
	child   *session.Session
	runtime *syntheticAgentSession
	done    func()
	closed  bool
}

func newClaudeSubagentRouter(in subagentSessionInput) *claudeSubagentRouter {
	return &claudeSubagentRouter{
		in:    in,
		byRef: make(map[string]*claudeSyntheticSubagent),
	}
}

func (r *claudeSubagentRouter) Handle(ctx context.Context, update agenttypes.Event) bool {
	if r == nil || r.in.Parent == nil || r.in.Manager == nil {
		return false
	}
	if r.handleSubagentTerminalSummary(ctx, update) {
		return false
	}
	ref := claudeSubagentRefFromUpdate(update)
	if !ref.hasRef() {
		return false
	}
	if isClaudeParentTaskLifecycle(update) {
		return false
	}
	if strings.TrimSpace(ref.ParentToolUseID) == "" && r.find(ref) == nil {
		return false
	}
	child, err := r.ensure(ctx, ref, toolCallFromUpdate(update))
	if err != nil {
		log.Printf("[subagent/claude] session.ensure.error root=%s parent=%s ref=%s err=%v", r.in.RootID, r.in.Parent.Key, ref.key(), err)
		return false
	}
	if child == nil || child.closed {
		return false
	}
	child.runtime.emit(update)
	if update.Type == agenttypes.EventTypeMessageDone {
		child.closed = true
	}
	return true
}

func (r *claudeSubagentRouter) FinishAll() {
	if r == nil {
		return
	}
	for _, child := range r.children {
		if child == nil || child.closed {
			continue
		}
		child.done()
		child.closed = true
	}
}

func (r *claudeSubagentRouter) handleSubagentTerminalSummary(ctx context.Context, update agenttypes.Event) bool {
	if update.Type != agenttypes.EventTypeToolUpdate {
		return false
	}
	toolCall, ok := update.Data.(agenttypes.ToolCall)
	if !ok {
		return false
	}
	summary := ""
	if toolCall.RawType == "subagent_result" {
		for _, item := range toolCall.Content {
			if strings.TrimSpace(item.Text) != "" {
				summary = item.Text
				break
			}
		}
	} else if toolCall.RawType == "claude_task" && strings.TrimSpace(stringMeta(toolCall.Meta, "subtype")) == "task_notification" {
		summary = stringMeta(toolCall.Meta, "summary")
	} else {
		return false
	}
	child := r.find(claudeSubagentRefFromUpdate(update))
	if child == nil && toolCall.RawType == "subagent_result" {
		child = r.onlyOpenChild()
	}
	if child == nil {
		return false
	}
	if strings.TrimSpace(summary) != "" {
		child.runtime.emit(agenttypes.Event{
			Type:      agenttypes.EventTypeMessageChunk,
			SessionID: update.SessionID,
			Data:      agenttypes.MessageChunk{Content: summary},
		})
	}
	child.runtime.emit(agenttypes.Event{Type: agenttypes.EventTypeMessageDone, SessionID: update.SessionID, Data: agenttypes.MessageDone{}})
	child.closed = true
	_ = ctx
	return true
}

func (r *claudeSubagentRouter) onlyOpenChild() *claudeSyntheticSubagent {
	var found *claudeSyntheticSubagent
	for _, child := range r.children {
		if child == nil || child.closed {
			continue
		}
		if found != nil {
			return nil
		}
		found = child
	}
	return found
}

func (r *claudeSubagentRouter) ensure(ctx context.Context, ref claudeSubagentRef, source *agenttypes.ToolCall) (*claudeSyntheticSubagent, error) {
	keys := ref.keys()
	if child := r.find(ref); child != nil {
		r.bindKeys(child, keys)
		return child, nil
	}
	primary := ref.key()
	if primary == "" {
		return nil, nil
	}
	if existing, err := r.in.Manager.FindAgentBindingByAgentSession(ctx, r.in.Agent, "claude-subagent:"+primary); err != nil {
		return nil, err
	} else if existing != nil {
		loaded, err := r.in.Manager.Get(ctx, existing.SessionKey, 0)
		if err != nil {
			return nil, err
		}
		child := r.attach(loaded, ref)
		r.bindKeys(child, keys)
		return child, nil
	}
	parentToolCallID := firstNonEmptyString(ref.ParentToolUseID, sourceCallID(source), ref.TaskID)
	child, err := r.in.Manager.Create(ctx, session.CreateInput{
		Type:             session.TypeChat,
		ParentSessionKey: r.in.Parent.Key,
		ParentToolCallID: parentToolCallID,
		Agent:            r.in.Agent,
		Model:            firstNonEmptyString(sourceModel(source), r.in.Model),
		PlanMode:         false,
		Name:             claudeSubagentSessionName(ref, source),
	})
	if err != nil {
		return nil, err
	}
	if err := r.in.Manager.UpsertAgentBinding(ctx, session.AgentBinding{
		SessionKey:     child.Key,
		Agent:          r.in.Agent,
		AgentSessionID: "claude-subagent:" + primary,
	}); err != nil {
		return nil, err
	}
	attached := r.attach(child, ref)
	r.bindKeys(attached, keys)
	if r.in.OnCreated != nil {
		r.in.OnCreated(child)
	}
	return attached, nil
}

func (r *claudeSubagentRouter) find(ref claudeSubagentRef) *claudeSyntheticSubagent {
	for _, key := range ref.keys() {
		if child := r.byRef[key]; child != nil {
			return child
		}
	}
	return nil
}

func (r *claudeSubagentRouter) attach(child *session.Session, ref claudeSubagentRef) *claudeSyntheticSubagent {
	runtime := &syntheticAgentSession{id: firstNonEmptyString(ref.ParentToolUseID, ref.TaskID, child.Key)}
	in := r.in
	done := attachBackgroundSessionUpdates(context.Background(), in, child, runtime)
	attached := &claudeSyntheticSubagent{
		key:     child.Key,
		child:   child,
		runtime: runtime,
		done:    done,
	}
	r.children = append(r.children, attached)
	return attached
}

func (r *claudeSubagentRouter) bindKeys(child *claudeSyntheticSubagent, keys []string) {
	for _, key := range keys {
		if key != "" {
			r.byRef[key] = child
		}
	}
}

type claudeSubagentRef struct {
	ParentToolUseID string
	TaskID          string
	SubagentType    string
	TaskDescription string
}

func (r claudeSubagentRef) hasRef() bool {
	return strings.TrimSpace(r.ParentToolUseID) != "" || strings.TrimSpace(r.TaskID) != ""
}

func (r claudeSubagentRef) key() string {
	return firstNonEmptyString(r.ParentToolUseID, r.TaskID)
}

func (r claudeSubagentRef) keys() []string {
	keys := make([]string, 0, 2)
	if value := strings.TrimSpace(r.ParentToolUseID); value != "" {
		keys = append(keys, "tool:"+value)
	}
	if value := strings.TrimSpace(r.TaskID); value != "" {
		keys = append(keys, "task:"+value)
	}
	return keys
}

func claudeSubagentRefFromUpdate(update agenttypes.Event) claudeSubagentRef {
	switch data := update.Data.(type) {
	case agenttypes.MessageChunk:
		return claudeSubagentRef{
			ParentToolUseID: data.ParentToolUseID,
			TaskID:          data.TaskID,
			SubagentType:    data.SubagentType,
			TaskDescription: data.TaskDescription,
		}
	case agenttypes.ThoughtChunk:
		return claudeSubagentRef{
			ParentToolUseID: data.ParentToolUseID,
			TaskID:          data.TaskID,
			SubagentType:    data.SubagentType,
			TaskDescription: data.TaskDescription,
		}
	case agenttypes.MessageDone:
		return claudeSubagentRef{
			ParentToolUseID: data.ParentToolUseID,
			TaskID:          data.TaskID,
			SubagentType:    data.SubagentType,
			TaskDescription: data.TaskDescription,
		}
	case agenttypes.ToolCall:
		return claudeSubagentRef{
			ParentToolUseID: stringMeta(data.Meta, "parentToolUseId"),
			TaskID:          stringMeta(data.Meta, "taskId"),
			SubagentType:    stringMeta(data.Meta, "subagentType"),
			TaskDescription: stringMeta(data.Meta, "taskDescription"),
		}
	default:
		return claudeSubagentRef{}
	}
}

func isClaudeParentTaskLifecycle(update agenttypes.Event) bool {
	if update.Type != agenttypes.EventTypeToolCall && update.Type != agenttypes.EventTypeToolUpdate {
		return false
	}
	toolCall, ok := update.Data.(agenttypes.ToolCall)
	return ok && toolCall.Kind == agenttypes.ToolKindTask && toolCall.RawType == "claude_task"
}

func toolCallFromUpdate(update agenttypes.Event) *agenttypes.ToolCall {
	toolCall, ok := update.Data.(agenttypes.ToolCall)
	if !ok {
		return nil
	}
	return &toolCall
}

func sourceCallID(toolCall *agenttypes.ToolCall) string {
	if toolCall == nil {
		return ""
	}
	return strings.TrimSpace(toolCall.CallID)
}

func sourceModel(toolCall *agenttypes.ToolCall) string {
	if toolCall == nil {
		return ""
	}
	return stringMeta(toolCall.Meta, "model")
}

func claudeSubagentSessionName(ref claudeSubagentRef, source *agenttypes.ToolCall) string {
	if value := strings.TrimSpace(ref.TaskDescription); value != "" {
		return truncateRunes(value, 48)
	}
	if source != nil {
		if value := strings.TrimSpace(source.Title); value != "" {
			return truncateRunes(value, 48)
		}
		if value := stringMeta(source.Meta, "prompt"); value != "" {
			return truncateRunes(value, 48)
		}
	}
	if value := strings.TrimSpace(ref.SubagentType); value != "" {
		return truncateRunes(value, 48)
	}
	return truncateRunes(ref.key(), 16)
}

type syntheticAgentSession struct {
	id       string
	onUpdate func(agenttypes.Event)
}

func (s *syntheticAgentSession) SendMessage(context.Context, string) error { return nil }
func (s *syntheticAgentSession) AnswerQuestion(context.Context, agenttypes.AskUserAnswer) error {
	return nil
}
func (s *syntheticAgentSession) CurrentModel() string                   { return "" }
func (s *syntheticAgentSession) SetModel(context.Context, string) error { return nil }
func (s *syntheticAgentSession) ListModels(context.Context) (agenttypes.ModelList, error) {
	return agenttypes.ModelList{}, nil
}
func (s *syntheticAgentSession) SetMode(context.Context, string) error   { return nil }
func (s *syntheticAgentSession) SetPlanMode(context.Context, bool) error { return nil }
func (s *syntheticAgentSession) ListModes(context.Context) (agenttypes.ModeList, error) {
	return agenttypes.ModeList{}, nil
}
func (s *syntheticAgentSession) ListCommands(context.Context) (agenttypes.CommandList, error) {
	return agenttypes.CommandList{}, nil
}
func (s *syntheticAgentSession) CancelCurrentTurn() error { return nil }
func (s *syntheticAgentSession) OnUpdate(onUpdate func(agenttypes.Event)) {
	s.onUpdate = onUpdate
}
func (s *syntheticAgentSession) SessionID() string { return s.id }
func (s *syntheticAgentSession) ContextWindow(context.Context) (agenttypes.ContextWindow, error) {
	return agenttypes.ContextWindow{}, nil
}
func (s *syntheticAgentSession) Close() error { return nil }
func (s *syntheticAgentSession) emit(event agenttypes.Event) {
	if s.onUpdate != nil {
		s.onUpdate(event)
	}
}

func (s *Service) ensureSubagentSessions(ctx context.Context, in subagentSessionInput) {
	if in.Parent == nil || in.Pool == nil || in.Manager == nil || in.ToolCall.RawType != "collabToolCall" {
		return
	}
	for _, receiverThreadID := range stringSliceMeta(in.ToolCall.Meta, "receiverThreadIds") {
		receiverThreadID = strings.TrimSpace(receiverThreadID)
		if receiverThreadID == "" {
			continue
		}
		child, err := s.ensureSubagentSession(ctx, in, receiverThreadID)
		if err != nil {
			log.Printf("[subagent] session.ensure.error root=%s parent=%s receiver=%s err=%v", in.RootID, in.Parent.Key, receiverThreadID, err)
			continue
		}
		if child != nil {
			s.startSubagentSubscription(in, child, receiverThreadID)
		}
	}
}

func (s *Service) ensureSubagentSession(ctx context.Context, in subagentSessionInput, receiverThreadID string) (*session.Session, error) {
	if existing, err := in.Manager.FindAgentBindingByAgentSession(ctx, in.Agent, receiverThreadID); err != nil {
		return nil, err
	} else if existing != nil {
		return in.Manager.Get(ctx, existing.SessionKey, 0)
	}
	child, err := in.Manager.Create(ctx, session.CreateInput{
		Type:             session.TypeChat,
		ParentSessionKey: in.Parent.Key,
		ParentToolCallID: in.ToolCall.CallID,
		Agent:            in.Agent,
		Model:            firstNonEmptyString(stringMeta(in.ToolCall.Meta, "model"), in.Model),
		PlanMode:         false,
		Name:             subagentSessionName(in.ToolCall, receiverThreadID),
	})
	if err != nil {
		return nil, err
	}
	if err := in.Manager.UpsertAgentBinding(ctx, session.AgentBinding{
		SessionKey:     child.Key,
		Agent:          in.Agent,
		AgentSessionID: receiverThreadID,
	}); err != nil {
		return nil, err
	}
	if in.OnCreated != nil {
		in.OnCreated(child)
	}
	return child, nil
}

func (s *Service) startSubagentSubscription(in subagentSessionInput, child *session.Session, receiverThreadID string) {
	key := in.RootID + ":" + child.Key + ":" + receiverThreadID
	if _, loaded := activeSubagentSubscriptions.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	go func() {
		defer activeSubagentSubscriptions.Delete(key)
		ctx, cancel := context.WithCancel(in.Pool.Context())
		registerActiveTurn(in.RootID, child.Key, cancel)
		defer unregisterActiveTurn(in.RootID, child.Key)
		runtime, err := in.Pool.GetOrCreate(ctx, agenttypes.OpenSessionInput{
			SessionKey:     agentPoolSessionKey(child.Key, in.Agent),
			AgentName:      in.Agent,
			Model:          firstNonEmptyString(stringMeta(in.ToolCall.Meta, "model"), in.Model),
			Mode:           in.Mode,
			Effort:         firstNonEmptyString(stringMeta(in.ToolCall.Meta, "reasoningEffort"), in.Effort),
			FastService:    in.FastService,
			PlanMode:       false,
			RootPath:       in.RootAbs,
			AgentSessionID: receiverThreadID,
			AgentCtxSeq:    child.AgentCtxSeq[in.Agent],
		})
		if err != nil {
			log.Printf("[subagent] subscription.open.error root=%s session=%s receiver=%s err=%v", in.RootID, child.Key, receiverThreadID, err)
			return
		}
		finishUse := in.Pool.BeginSessionUse(agentPoolSessionKey(child.Key, in.Agent))
		defer finishUse()
		setActiveTurnSession(in.RootID, child.Key, runtime)
		subscriber, ok := runtime.(agenttypes.ThreadEventSubscriber)
		if !ok {
			log.Printf("[subagent] subscription.unsupported root=%s session=%s receiver=%s", in.RootID, child.Key, receiverThreadID)
			return
		}
		markDone := attachBackgroundSessionUpdates(ctx, in, child, runtime)
		if err := subscriber.SubscribeThreadEvents(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[subagent] subscription.error root=%s session=%s receiver=%s err=%v", in.RootID, child.Key, receiverThreadID, err)
		}
		markDone()
	}()
}

func attachBackgroundSessionUpdates(ctx context.Context, in subagentSessionInput, child *session.Session, runtime agenttypes.Session) func() {
	var responseText string
	plannedAssistantSeq := session.MaxExchangeSeq(child.Exchanges) + 1
	// 子会话是三条服务端发起的轮次路径之一（方案 C2）：它没有 user 行
	// （子会话的转录就是子代理自己的），所以 TurnSeq 留 0，只 mint turn_id。
	// 一个订阅 = 一条助手行 = 一轮，故 turn_id 在这里 mint。
	subTurnID := newTurnID()
	auxBuffer := make([]session.ExchangeAux, 0, 8)
	var thoughtBuffer strings.Builder
	lastResponseUpdateType := ""
	var doneMu sync.Mutex
	doneSent := false
	currentThoughtID := ""
	flushThought := func() {
		thought := thoughtBuffer.String()
		if strings.TrimSpace(thought) == "" {
			thoughtBuffer.Reset()
			currentThoughtID = ""
			return
		}
		thoughtID := currentThoughtID
		thoughtBuffer.Reset()
		currentThoughtID = ""
		auxBuffer = append(auxBuffer, session.ExchangeAux{
			Seq:       plannedAssistantSeq,
			Line:      currentAssistantLine(responseText),
			Thought:   thought,
			ThoughtID: thoughtID,
		})
	}
	finish := func(emit bool) {
		doneMu.Lock()
		if doneSent {
			doneMu.Unlock()
			return
		}
		doneSent = true
		doneMu.Unlock()
		ctx = session.WithExchangeSource(ctx, session.ExchangeSourceLive)
		ctx = session.WithExchangeIdentity(ctx, session.ExchangeIdentity{TurnID: subTurnID})
		defer in.Manager.ClearPendingExchangeAux(context.Background(), child.Key)
		flushThought()
		if err := in.Manager.AddExchangeForAgent(ctx, child, "agent", responseText, in.Agent, in.Mode, in.Effort, in.FastService); err != nil {
			log.Printf("[subagent] persist.agent.error root=%s session=%s err=%v", in.RootID, child.Key, err)
			return
		}
		subAux := dedupeExchangeAuxBuffer(auxBuffer)
		for i := range subAux {
			subAux[i] = hydratePendingToolCallAux(ctx, in.Manager, child.Key, subAux[i])
			if err := in.Manager.AddExchangeAux(ctx, child.Key, subAux[i]); err != nil {
				log.Printf("[subagent] persist.aux.error root=%s session=%s err=%v", in.RootID, child.Key, err)
				return
			}
		}
		// 双写块流（方案 S2a）。子代理块带 parentToolUseId / taskId / subagentType（D16）由
		// aux 的既有归属字段承载，S2c 接线；这里先保证正文不丢。
		subAgentSeq := session.MaxExchangeSeq(child.Exchanges)
		if err := persistTurnBlocks(ctx, in.Manager, child.Key, subAgentSeq, subTurnID, responseText, subAux); err != nil {
			log.Printf("[subagent] persist.blocks.error root=%s session=%s seq=%d err=%v", in.RootID, child.Key, subAgentSeq, err)
		}
		if err := in.Manager.UpdateAgentState(ctx, child, in.Agent, contextLineCount(child.Exchanges), runtime.SessionID()); err != nil {
			log.Printf("[subagent] persist.agent_state.error root=%s session=%s err=%v", in.RootID, child.Key, err)
		}
		if emit && in.OnUpdate != nil {
			in.OnUpdate(child.Key, agenttypes.Event{Type: agenttypes.EventTypeMessageDone, Data: agenttypes.MessageDone{}})
		}
	}
	runtime.OnUpdate(func(update agenttypes.Event) {
		switch update.Type {
		case agenttypes.EventTypeThoughtChunk:
			if chunk, ok := update.Data.(agenttypes.ThoughtChunk); ok && chunk.Content != "" {
				if currentThoughtID == "" {
					currentThoughtID = "thought-" + randomHex(8)
				}
				chunk.ID = currentThoughtID
				update.Data = chunk
				thoughtBuffer.WriteString(chunk.Content)
			}
		case agenttypes.EventTypeToolCall, agenttypes.EventTypeToolUpdate, agenttypes.EventTypeTodoUpdate, agenttypes.EventTypePlanUpdate, agenttypes.EventTypeCompact, agenttypes.EventTypeMessageChunk, agenttypes.EventTypeMessageDone:
			flushThought()
		}
		clientUpdate := compactAgentUpdate(update)
		if update.Type == agenttypes.EventTypeToolCall || update.Type == agenttypes.EventTypeToolUpdate {
			if toolCall, ok := update.Data.(agenttypes.ToolCall); ok {
				if shouldPersistToolCallAux(toolCall) {
					toolCallCopy := toolCall
					auxBuffer = append(auxBuffer, session.ExchangeAux{
						Seq:      plannedAssistantSeq,
						Line:     currentAssistantLine(responseText),
						ToolCall: &toolCallCopy,
					})
					_ = in.Manager.UpsertPendingExchangeAux(context.Background(), child.Key, session.ExchangeAux{ToolCall: &toolCallCopy})
				}
			}
		}
		if update.Type == agenttypes.EventTypePlanUpdate {
			if plan, ok := update.Data.(agenttypes.PlanUpdate); ok && strings.TrimSpace(plan.Content) != "" {
				planCopy := plan
				auxBuffer = append(auxBuffer, session.ExchangeAux{
					Seq:  plannedAssistantSeq,
					Line: currentAssistantLine(responseText),
					Plan: &planCopy,
				})
			}
		}
		if update.Type == agenttypes.EventTypeTodoUpdate {
			if todo, ok := update.Data.(agenttypes.TodoUpdate); ok && len(todo.Items) > 0 {
				todoCopy := todo
				auxBuffer = append(auxBuffer, session.ExchangeAux{
					Seq:  plannedAssistantSeq,
					Line: currentAssistantLine(responseText),
					Todo: &todoCopy,
				})
			}
		}
		if update.Type == agenttypes.EventTypeCompact {
			if compact, ok := update.Data.(agenttypes.CompactNotice); ok {
				compactCopy := compact
				auxBuffer = append(auxBuffer, session.ExchangeAux{
					Seq:     plannedAssistantSeq,
					Line:    currentAssistantLine(responseText),
					Compact: &compactCopy,
				})
			}
		}
		if update.Type == agenttypes.EventTypeMessageChunk {
			if chunk, ok := update.Data.(agenttypes.MessageChunk); ok {
				responseText = appendResponseChunk(responseText, lastResponseUpdateType, chunk.Content)
				lastResponseUpdateType = string(update.Type)
			}
		} else if update.Type == agenttypes.EventTypeThoughtChunk ||
			update.Type == agenttypes.EventTypeToolCall ||
			update.Type == agenttypes.EventTypeToolUpdate ||
			update.Type == agenttypes.EventTypeTodoUpdate ||
			update.Type == agenttypes.EventTypePlanUpdate ||
			update.Type == agenttypes.EventTypeCompact {
			lastResponseUpdateType = string(update.Type)
		}
		if update.Type != agenttypes.EventTypeMessageDone {
			if in.OnUpdate != nil {
				in.OnUpdate(child.Key, clientUpdate)
			}
			return
		}
		finish(false)
		if in.OnUpdate != nil {
			in.OnUpdate(child.Key, clientUpdate)
		}
	})
	return func() { finish(true) }
}

func (s *Service) sendCommandMessage(ctx context.Context, in SendMessageInput, manager *session.Manager, current *session.Session, turnID string, userExchangeSeq int) error {
	if strings.TrimSpace(in.Content) == "" {
		return errors.New("command required")
	}
	root := manager.Root()
	rootAbs, err := root.RootDir()
	if err != nil {
		return err
	}
	callID := "cmd-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	startTool := agenttypes.ToolCall{
		CallID:  callID,
		Title:   in.Content,
		Status:  "running",
		Kind:    agenttypes.ToolKindExecute,
		RawType: "commandExecution",
		Meta: map[string]any{
			"source":       "userShell",
			"phase":        "start",
			"command":      in.Content,
			"cwd":          ".",
			"terminalCols": in.TerminalCols,
		},
	}
	if in.OnUpdate != nil {
		in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeToolCall, Data: startTool})
	}

	proc, err := commandexec.StartInSession(ctx, commandexec.Options{
		Command:      in.Content,
		Cwd:          rootAbs,
		Shells:       configuredShells(s.Registry),
		Shell:        in.Shell,
		RootID:       in.RootID,
		Session:      current.Key,
		TerminalCols: in.TerminalCols,
	})
	if err != nil {
		log.Printf("[command] start.error root=%s session=%s command=%q err=%v", in.RootID, current.Key, in.Content, err)
		final := startTool
		final.Status = "failed"
		final.Meta = cloneMeta(final.Meta)
		final.Meta["phase"] = "final"
		final.Meta["exitCode"] = -1
		final.Meta["error"] = err.Error()
		final.Content = []agenttypes.ToolCallContentItem{{Type: "text", Text: err.Error()}}
		if in.OnUpdate != nil {
			in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeToolUpdate, Data: final})
			in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeMessageDone, Data: agenttypes.MessageDone{}})
		}
		if persistErr := persistCommandTurn(ctx, manager, current, final, turnID, userExchangeSeq); persistErr != nil {
			return persistErr
		}
		return err
	}

	limiter := commandexec.NewOutputLimiter()
	ticker := time.NewTicker(limiter.FlushEvery())
	defer ticker.Stop()
	done := make(chan commandexec.Result, 1)
	go func() {
		done <- proc.Wait()
	}()

	var result commandexec.Result
	var haveResult bool
	cancelStarted := false
	outputCh := proc.Output()
	for !haveResult {
		select {
		case chunk, ok := <-outputCh:
			if !ok {
				outputCh = nil
				continue
			}
			limiter.Write(chunk)
		case <-ticker.C:
			flushCommandOutput(in.OnUpdate, startTool, limiter)
			ticker.Reset(limiter.FlushEvery())
		case result = <-done:
			haveResult = true
		case <-ctx.Done():
			if !cancelStarted {
				cancelStarted = true
				go stopCommandProcess(proc)
			}
		}
	}
	drainCommandOutput(proc.Output(), limiter, 250*time.Millisecond)
	flushCommandOutput(in.OnUpdate, startTool, limiter)

	final := startTool
	final.Status = "success"
	if cancelStarted || ctx.Err() != nil {
		final.Status = "cancelled"
	} else if result.ExitCode != 0 {
		final.Status = "failed"
	}
	tail := limiter.Tail()
	persistedBytes := limiter.TailBytes()
	outputBytes := limiter.TotalBytes()
	text := string(tail)
	if strings.TrimSpace(result.Shell) != "" {
		if err := manager.UpdateShell(context.Background(), current, result.Shell); err != nil {
			log.Printf("[command] shell.update.error root=%s session=%s shell=%q err=%v", in.RootID, current.Key, result.Shell, err)
		}
	}
	if outputBytes > persistedBytes {
		text = fmt.Sprintf("[output truncated: showing last %d bytes of %d bytes]\n%s", persistedBytes, outputBytes, text)
	}
	final.Meta = map[string]any{
		"source":         "userShell",
		"phase":          "final",
		"command":        in.Content,
		"cwd":            ".",
		"shell":          result.Shell,
		"exitCode":       result.ExitCode,
		"durationMs":     result.Duration.Milliseconds(),
		"outputBytes":    outputBytes,
		"persistedBytes": persistedBytes,
		"truncated":      outputBytes > persistedBytes,
		"truncation":     "tail",
		"terminalCols":   in.TerminalCols,
	}
	if cancelStarted || ctx.Err() != nil {
		final.Meta["cancelled"] = true
	}
	final.Content = []agenttypes.ToolCallContentItem{{Type: "text", Text: text}}
	if in.OnUpdate != nil {
		in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeToolUpdate, Data: final})
		in.OnUpdate(agenttypes.Event{Type: agenttypes.EventTypeMessageDone, Data: agenttypes.MessageDone{}})
	}
	if err := persistCommandTurn(context.Background(), manager, current, final, turnID, userExchangeSeq); err != nil {
		log.Printf("[command] persist.error root=%s session=%s call=%s err=%v", in.RootID, current.Key, callID, err)
		return err
	}
	if final.Status == "success" || final.Status == "cancelled" {
		if err := UpsertCommandSuggestion(manager, CommandSuggestion{
			Command:        in.Content,
			Cwd:            ".",
			Shell:          result.Shell,
			RootID:         in.RootID,
			LastExitCode:   result.ExitCode,
			LastDurationMs: result.Duration.Milliseconds(),
			LastUsedAt:     result.FinishedAt,
		}); err != nil {
			log.Printf("[command/history] upsert.error root=%s session=%s err=%v", in.RootID, current.Key, err)
		}
	}
	return nil
}

func flushCommandOutput(onUpdate func(agenttypes.Event), base agenttypes.ToolCall, limiter *commandexec.OutputLimiter) {
	if onUpdate == nil || limiter == nil {
		return
	}
	chunk, ok := limiter.Flush()
	if !ok {
		return
	}
	update := base
	update.Status = "running"
	update.Meta = map[string]any{
		"source":       "userShell",
		"phase":        "stream",
		"outputMode":   "ring",
		"skippedBytes": chunk.SkippedBytes,
	}
	update.Content = []agenttypes.ToolCallContentItem{{Type: "text", Text: chunk.Text}}
	onUpdate(agenttypes.Event{Type: agenttypes.EventTypeToolUpdate, Data: update})
}

func drainCommandOutput(output <-chan []byte, limiter *commandexec.OutputLimiter, maxWait time.Duration) {
	if output == nil || limiter == nil {
		return
	}
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	for {
		select {
		case chunk, ok := <-output:
			if !ok {
				return
			}
			if len(chunk) > 0 {
				limiter.Write(chunk)
			}
		case <-timer.C:
			return
		}
	}
}

func stopCommandProcess(proc commandexec.Process) {
	if proc == nil {
		return
	}
	_ = proc.Interrupt()
	time.Sleep(2 * time.Second)
	_ = proc.Terminate()
	time.Sleep(3 * time.Second)
	_ = proc.KillTree()
}

func configuredShells(registry Registry) []commandexec.ShellSpec {
	if registry == nil {
		return nil
	}
	pool := registry.GetAgentPool()
	if pool == nil {
		return nil
	}
	cfg := pool.Config()
	shells := make([]commandexec.ShellSpec, 0, len(cfg.Shells))
	for _, shell := range cfg.Shells {
		shells = append(shells, commandexec.ShellSpec{
			Command:       shell.Command,
			Args:          append([]string(nil), shell.Args...),
			LongShellArgs: append([]string(nil), shell.LongShellArgs...),
			CommandPrefix: shell.CommandPrefix,
		})
	}
	return shells
}

// persistCommandTurn 只补助手行与其 aux：用户行已在 SendMessage 起手、命令真正执行前
// 落盘（与聊天会话同一条规则），这里再写一次会落重复。
//
// 助手行必须复用本轮 user 行 mint 的 turn_id（方案 C2）：命令会话是三条服务端发起的
// 轮次路径之一（另两条是 SendMessage 与子会话订阅），少了它命令轮的正文在 S2b 之后
// 挂不回轮次。助手正文为空，块的正文全部来自 aux（tool_call）。
func persistCommandTurn(ctx context.Context, manager *session.Manager, current *session.Session, final agenttypes.ToolCall, turnID string, userExchangeSeq int) error {
	ctx = session.WithExchangeSource(ctx, session.ExchangeSourceLive)
	ctx = session.WithExchangeIdentity(ctx, session.ExchangeIdentity{TurnID: turnID, TurnSeq: userExchangeSeq})
	if err := manager.AddExchangeForAgent(ctx, current, "agent", "", "", "", "", ""); err != nil {
		return err
	}
	// 助手行真实 seq 从缓存取，而不是调用方预测的 plannedAssistantSeq：
	// 预测值一旦与分配值漂移，aux 会挂到别人的 seq 上，块流同理。
	agentSeq := session.MaxExchangeSeq(current.Exchanges)
	aux := session.ExchangeAux{
		Seq:      agentSeq,
		Line:     0,
		ToolCall: &final,
	}
	if err := manager.AddExchangeAux(ctx, current.Key, aux); err != nil {
		return err
	}
	if err := persistTurnBlocks(ctx, manager, current.Key, agentSeq, turnID, "", []session.ExchangeAux{aux}); err != nil {
		log.Printf("[command] persist.blocks.error session=%s seq=%d err=%v", current.Key, agentSeq, err)
	}
	return nil
}

func cloneMeta(meta map[string]any) map[string]any {
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	return out
}

type SendRecoveryInput struct {
	RootID             string
	SessionKey         string
	Manager            *session.Manager
	Current            *session.Session
	AgentName          string
	Model              string
	Mode               string
	Effort             string
	FastService        string
	PlanMode           bool
	RootAbs            string
	CurrentSession     agenttypes.Session
	Prompt             string
	SawAssistantChunk  bool
	SendWithAttachment func(agenttypes.Session, string) error
}

func (s *Service) recoverAgentTurn(ctx context.Context, in SendRecoveryInput) (agenttypes.Session, error) {
	if s == nil {
		return nil, errors.New("services not configured")
	}
	if in.Current == nil {
		return nil, errors.New("session required")
	}
	if in.Manager == nil {
		return nil, errors.New("session manager required")
	}
	if in.SendWithAttachment == nil {
		return nil, errors.New("send function required")
	}
	if in.CurrentSession == nil {
		return nil, errors.New("current session required")
	}

	var lastErr error
	for attempt := 1; attempt <= sessionRecoveryAttempts; attempt++ {
		if attempt > 1 {
			log.Printf("[session/recovery] wait root=%s session=%s agent=%s attempt=%d/%d delay=%s", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, sessionRecoveryDelay)
			if err := waitForRecoveryDelay(ctx, sessionRecoveryDelay); err != nil {
				return nil, err
			}
		}

		sess := in.CurrentSession
		recoveryMessage := in.Prompt
		recoveryAction := "resend_prompt"
		if in.SawAssistantChunk {
			recoveryMessage = "continue"
			recoveryAction = "continue"
		}
		log.Printf("[session/recovery] send.start root=%s session=%s agent=%s attempt=%d/%d action=%s", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, recoveryAction)
		if err := in.SendWithAttachment(sess, recoveryMessage); err != nil {
			if isCanceledTurnError(err) || ctx.Err() != nil {
				return nil, err
			}
			if isNonRecoverableAgentError(err) {
				log.Printf("[session/recovery] send.non_recoverable root=%s session=%s agent=%s attempt=%d/%d action=%s err=%v", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, recoveryAction, err)
				return nil, err
			}
			lastErr = err
			log.Printf("[session/recovery] send.failed root=%s session=%s agent=%s attempt=%d/%d action=%s err=%v", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, recoveryAction, err)
			continue
		}
		log.Printf("[session/recovery] send.done root=%s session=%s agent=%s attempt=%d/%d action=%s", in.RootID, in.SessionKey, in.AgentName, attempt, sessionRecoveryAttempts, recoveryAction)
		return sess, nil
	}
	if lastErr == nil {
		lastErr = errors.New("agent recovery failed")
	}
	return nil, lastErr
}

func waitForRecoveryDelay(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) AnswerQuestion(ctx context.Context, in AnswerQuestionInput) error {
	if err := s.ensureRegistry(); err != nil {
		return err
	}
	sessionKey := strings.TrimSpace(in.SessionKey)
	if sessionKey == "" {
		return errors.New("session key required")
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return err
	}
	agentName := strings.TrimSpace(in.Agent)
	if agentName == "" {
		current, err := manager.Get(ctx, sessionKey, 0)
		if err != nil {
			return err
		}
		agentName = strings.TrimSpace(session.InferAgentFromSession(current))
	}
	if agentName == "" {
		return errors.New("agent required")
	}
	pool := s.Registry.GetAgentPool()
	if pool == nil {
		return errors.New("agent pool unavailable")
	}
	sess, ok := pool.Get(agentPoolSessionKey(sessionKey, agentName))
	if !ok {
		return errors.New("agent session not found")
	}
	answer := agenttypes.AskUserAnswer{
		ToolUseID: strings.TrimSpace(in.ToolUseID),
		Answers:   in.Answers,
	}
	if err := manager.MarkPendingAskUserAnswered(ctx, sessionKey, answer.ToolUseID, answer.Answers, time.Now()); err != nil {
		return err
	}
	if err := sess.AnswerQuestion(ctx, answer); err != nil {
		return err
	}
	return nil
}

func currentAssistantLine(responseText string) int {
	// Anchor auxiliary events after the last text line, before trailing empty
	// lines that may be filled by subsequent chunks. Leave the response intact.
	responseText = strings.TrimRight(responseText, "\n")
	if responseText == "" {
		return 0
	}
	return strings.Count(responseText, "\n") + 1
}

func hydratePendingToolCallAux(ctx context.Context, manager *session.Manager, sessionKey string, aux session.ExchangeAux) session.ExchangeAux {
	if manager == nil || aux.ToolCall == nil || strings.TrimSpace(aux.ToolCall.CallID) == "" {
		return aux
	}
	if aux.ToolCall.Kind != agenttypes.ToolKindAskUser {
		return aux
	}
	latest, err := manager.GetFullToolCall(ctx, sessionKey, aux.ToolCall.CallID)
	if err != nil || latest == nil {
		return aux
	}
	if latest.Kind != agenttypes.ToolKindAskUser {
		return aux
	}
	toolCall := *latest
	aux.ToolCall = &toolCall
	return aux
}

func dedupeExchangeAuxBuffer(items []session.ExchangeAux) []session.ExchangeAux {
	if len(items) == 0 {
		return nil
	}
	seenToolCallIDs := make(map[string]int, len(items))
	seenPlanIDs := make(map[string]struct{}, len(items))
	seenCompactIDs := make(map[string]struct{}, len(items))
	seenTodo := false
	out := make([]session.ExchangeAux, 0, len(items))
	for i := len(items) - 1; i >= 0; i-- {
		item := items[i]
		callID := ""
		if item.ToolCall != nil {
			callID = strings.TrimSpace(item.ToolCall.CallID)
		}
		if callID != "" {
			if existingIndex, exists := seenToolCallIDs[callID]; exists {
				if out[existingIndex].ToolCall != nil {
					merged := mergeBufferedToolCall(*item.ToolCall, *out[existingIndex].ToolCall)
					out[existingIndex].ToolCall = &merged
				}
				continue
			}
			seenToolCallIDs[callID] = len(out)
		}
		isTodo := item.Todo != nil || (item.ToolCall != nil && item.ToolCall.Kind == agenttypes.ToolKindTodo)
		if isTodo {
			if seenTodo {
				continue
			}
			seenTodo = true
		}
		if item.Plan != nil {
			planID := strings.TrimSpace(item.Plan.ID)
			if planID != "" {
				if _, exists := seenPlanIDs[planID]; exists {
					continue
				}
				seenPlanIDs[planID] = struct{}{}
			}
		}
		if item.Compact != nil {
			compactID := strings.TrimSpace(item.Compact.ID)
			if compactID != "" {
				if _, exists := seenCompactIDs[compactID]; exists {
					continue
				}
				seenCompactIDs[compactID] = struct{}{}
			}
		}
		out = append(out, item)
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}

func shouldPersistToolCallAux(toolCall agenttypes.ToolCall) bool {
	if toolCall.RawType != "claude_task" {
		return true
	}
	return strings.TrimSpace(stringMeta(toolCall.Meta, "subtype")) != "task_progress"
}

func mergeBufferedToolCall(base, next agenttypes.ToolCall) agenttypes.ToolCall {
	merged := base
	if strings.TrimSpace(next.CallID) != "" {
		merged.CallID = next.CallID
	}
	if strings.TrimSpace(next.Title) != "" {
		merged.Title = next.Title
	}
	if strings.TrimSpace(next.Status) != "" {
		merged.Status = next.Status
	}
	if next.Kind != "" {
		merged.Kind = next.Kind
	}
	if len(next.Content) > 0 {
		merged.Content = append([]agenttypes.ToolCallContentItem(nil), next.Content...)
	}
	if len(next.Locations) > 0 {
		merged.Locations = append([]agenttypes.ToolCallLocation(nil), next.Locations...)
	}
	if strings.TrimSpace(next.RawType) != "" {
		merged.RawType = next.RawType
	}
	if len(base.Meta) > 0 || len(next.Meta) > 0 {
		merged.Meta = make(map[string]any, len(base.Meta)+len(next.Meta))
		for key, value := range base.Meta {
			merged.Meta[key] = value
		}
		for key, value := range next.Meta {
			merged.Meta[key] = value
		}
		if stringMeta(base.Meta, "taskTool") == "TaskCreate" && stringMeta(next.Meta, "taskTool") == "TaskUpdate" {
			merged.Meta["taskTool"] = "TaskCreate"
		}
	}
	return merged
}

func appendResponseChunk(responseText, lastResponseUpdateType, chunk string) string {
	if responseText != "" &&
		(lastResponseUpdateType == string(agenttypes.EventTypeThoughtChunk) ||
			lastResponseUpdateType == string(agenttypes.EventTypeToolCall) ||
			lastResponseUpdateType == string(agenttypes.EventTypeToolUpdate) ||
			lastResponseUpdateType == string(agenttypes.EventTypeTodoUpdate) ||
			lastResponseUpdateType == string(agenttypes.EventTypePlanUpdate) ||
			lastResponseUpdateType == string(agenttypes.EventTypeCompact)) &&
		!strings.HasSuffix(responseText, "\n\n") &&
		!strings.HasSuffix(responseText, "\n") {
		responseText += "\n\n"
	}
	return responseText + chunk
}

func randomHex(bytes int) string {
	if bytes <= 0 {
		return ""
	}
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func subagentSessionName(toolCall agenttypes.ToolCall, receiverThreadID string) string {
	if prompt := stringMeta(toolCall.Meta, "prompt"); prompt != "" {
		return truncateRunes(prompt, 48)
	}
	return truncateRunes(receiverThreadID, 16)
}

func stringMeta(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	value, _ := meta[key].(string)
	return strings.TrimSpace(value)
}

func stringSliceMeta(meta map[string]any, key string) []string {
	if meta == nil {
		return nil
	}
	switch value := meta[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, strings.TrimSpace(text))
			}
		}
		return out
	default:
		return nil
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if limit <= 0 || len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}

type pathNormalizer interface {
	NormalizePath(string) (string, error)
}

func normalizeAgentUpdatePaths(root pathNormalizer, update agenttypes.Event) agenttypes.Event {
	if root == nil {
		return update
	}
	toolCall, ok := update.Data.(agenttypes.ToolCall)
	if !ok {
		return update
	}

	for i := range toolCall.Locations {
		toolCall.Locations[i].Path = normalizeToolPath(root, toolCall.Locations[i].Path)
	}
	for i := range toolCall.Content {
		toolCall.Content[i].Path = normalizeToolPath(root, toolCall.Content[i].Path)
		if toolCall.Content[i].Type == "text" {
			toolCall.Content[i].Text = normalizeDiffTextPaths(root, toolCall.Content[i].Text)
		}
	}
	if toolCall.Meta != nil {
		if filePath, ok := toolCall.Meta["filePath"].(string); ok {
			toolCall.Meta["filePath"] = normalizeToolPath(root, filePath)
		}
		if path, ok := toolCall.Meta["path"].(string); ok {
			toolCall.Meta["path"] = normalizeToolPath(root, path)
		}
	}
	update.Data = toolCall
	return update
}

func compactAgentUpdate(update agenttypes.Event) agenttypes.Event {
	switch update.Type {
	case agenttypes.EventTypeToolCall, agenttypes.EventTypeToolUpdate:
		if toolCall, ok := update.Data.(agenttypes.ToolCall); ok {
			update.Data = session.CompactToolCall(toolCall)
		}
	}
	return update
}

func normalizeToolPath(root pathNormalizer, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	normalized, err := root.NormalizePath(path)
	if err != nil {
		return path
	}
	return normalized
}

func relatedFileRecordPath(managedRootAbs, runtimeRootAbs, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	slashPath := filepath.ToSlash(path)
	if strings.HasPrefix(slashPath, ".worktree/task-") {
		return filepath.Clean(filepath.Join(managedRootAbs, filepath.FromSlash(slashPath)))
	}
	runtimeRootAbs = strings.TrimSpace(runtimeRootAbs)
	managedRootAbs = strings.TrimSpace(managedRootAbs)
	if runtimeRootAbs != "" && managedRootAbs != "" && !sameManagedDirPath(runtimeRootAbs, managedRootAbs) {
		return filepath.Clean(filepath.Join(runtimeRootAbs, filepath.FromSlash(slashPath)))
	}
	return path
}

func normalizeDiffTextPaths(root pathNormalizer, text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	changed := false
	for i, line := range lines {
		next, ok := normalizeDiffLine(root, line)
		if !ok || next == line {
			continue
		}
		lines[i] = next
		changed = true
	}
	if !changed {
		return text
	}
	return strings.Join(lines, "\n")
}

func normalizeDiffLine(root pathNormalizer, line string) (string, bool) {
	switch {
	case strings.HasPrefix(line, "diff --git "):
		rest := strings.TrimPrefix(line, "diff --git ")
		parts := strings.SplitN(rest, " ", 2)
		if len(parts) != 2 {
			return line, false
		}
		left, leftOK := normalizeDiffRef(root, parts[0])
		right, rightOK := normalizeDiffRef(root, parts[1])
		if !leftOK && !rightOK {
			return line, false
		}
		return "diff --git " + left + " " + right, true
	case strings.HasPrefix(line, "--- "):
		next, ok := normalizeDiffRef(root, strings.TrimPrefix(line, "--- "))
		if !ok {
			return line, false
		}
		return "--- " + next, true
	case strings.HasPrefix(line, "+++ "):
		next, ok := normalizeDiffRef(root, strings.TrimPrefix(line, "+++ "))
		if !ok {
			return line, false
		}
		return "+++ " + next, true
	default:
		return line, false
	}
}

func normalizeDiffRef(root pathNormalizer, ref string) (string, bool) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" || trimmed == "/dev/null" {
		return ref, false
	}

	prefix := ""
	path := trimmed
	switch {
	case strings.HasPrefix(trimmed, "a/"), strings.HasPrefix(trimmed, "b/"):
		prefix = trimmed[:2]
		path = trimmed[2:]
	}

	normalized := normalizeToolPath(root, path)
	if normalized == path || normalized == "" {
		return ref, false
	}
	return prefix + normalized, true
}

func isClaudeAliasModelForValidate(model string) bool {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return false
	}
	has := len(trimmed) >= 4 && strings.EqualFold(trimmed[len(trimmed)-4:], "[1m]")
	base := trimmed
	if has {
		base = strings.TrimSpace(trimmed[:len(trimmed)-4])
	}
	lower := strings.ToLower(strings.TrimSpace(base))
	switch lower {
	case "fable", "opus", "sonnet", "haiku", "of", "op", "os", "ok", "default":
		return true
	default:
		return false
	}
}

func canonicalClaudeModelForValidate(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}
	if strings.EqualFold(trimmed, "default") {
		return trimmed
	}
	if !isClaudeAliasModelForValidate(trimmed) {
		return trimmed
	}
	has := len(trimmed) >= 4 && strings.EqualFold(trimmed[len(trimmed)-4:], "[1m]")
	base := trimmed
	if has {
		base = strings.TrimSpace(trimmed[:len(trimmed)-4])
	}
	lower := strings.ToLower(strings.TrimSpace(base))
	switch lower {
	case "fable":
		lower = "of"
	case "opus":
		lower = "op"
	case "sonnet":
		lower = "os"
	case "haiku":
		lower = "ok"
	}
	if has {
		return lower + "[1m]"
	}
	return lower
}

func claudeModelBaseForValidate(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}
	if strings.EqualFold(trimmed, "default") {
		return "default"
	}
	has := len(trimmed) >= 4 && strings.EqualFold(trimmed[len(trimmed)-4:], "[1m]")
	base := trimmed
	if has {
		base = strings.TrimSpace(trimmed[:len(trimmed)-4])
	}
	if !isClaudeAliasModelForValidate(trimmed) {
		return strings.TrimSpace(base)
	}
	lower := strings.ToLower(strings.TrimSpace(base))
	switch lower {
	case "fable":
		lower = "of"
	case "opus":
		lower = "op"
	case "sonnet":
		lower = "os"
	case "haiku":
		lower = "ok"
	}
	return lower
}

func (s *Service) validateAgentModel(agentName, model string) error {
	agentName = strings.TrimSpace(agentName)
	model = strings.TrimSpace(model)
	if agentName == "" || model == "" || s.Registry == nil {
		return nil
	}
	isClaude := strings.EqualFold(agentName, "claude")
	if isClaude {
		model = canonicalClaudeModelForValidate(model)
	}
	prober := s.Registry.GetProber()
	if prober == nil {
		return nil
	}
	status, ok := prober.GetStatus(agentName)
	if !ok || len(status.Models) == 0 {
		return nil
	}
	for _, item := range status.Models {
		candidate := strings.TrimSpace(item.ID)
		if isClaude {
			candidate = canonicalClaudeModelForValidate(candidate)
		}
		if candidate == model {
			return nil
		}
		// 1M suffix is an explicit user toggle, not a separate model entry.
		// Prober advertises base ids without [1m] (both Anthropic aliases and generic provider ids like glm-*/deepseek-*),
		// so base and base[1m] are the same model.
		if isClaude && claudeModelBaseForValidate(candidate) != "" && claudeModelBaseForValidate(candidate) == claudeModelBaseForValidate(model) {
			return nil
		}
	}
	supported := make([]string, 0, len(status.Models))
	for i, item := range status.Models {
		if i >= 12 {
			break
		}
		if id := strings.TrimSpace(item.ID); id != "" {
			supported = append(supported, id)
		}
	}
	return fmt.Errorf("model %q is not supported by agent %q (supported models: %s)", model, agentName, strings.Join(supported, ", "))
}

// staleCancelTarget 判断一个取消请求是否已经过期：调用方点名要停的那一轮不是此刻
// 在跑的这一轮（含「当时根本没在跑」）。
func staleCancelTarget(in CancelSessionTurnInput, active *activeTurnState) bool {
	if !in.TargetPinned {
		return false
	}
	if active != nil && active.id == in.TargetTurnID && in.TargetTurnID != 0 {
		return false
	}
	log.Printf("[session] turn.cancel.stale target_turn=%d active_turn=%v action=drop",
		in.TargetTurnID, activeTurnIDOrNil(active))
	return true
}

func activeTurnIDOrNil(active *activeTurnState) any {
	if active == nil {
		return "none"
	}
	return active.id
}

func (s *Service) CancelSessionTurn(ctx context.Context, in CancelSessionTurnInput) error {
	if err := s.ensureRegistry(); err != nil {
		return err
	}
	manager, err := s.Registry.GetSessionManager(in.RootID)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(in.Key)
	if key == "" {
		return errors.New("session key required")
	}
	if strings.HasPrefix(key, "transient-") {
		active := getActiveTurn(in.RootID, key)
		if active == nil || staleCancelTarget(in, active) {
			return nil
		}
		active.cancel()
		if active.session != nil {
			if err := active.session.CancelCurrentTurn(); err != nil {
				log.Printf("[session] turn.cancel.error root=%s session=%s err=%v", in.RootID, key, err)
			}
		}
		return nil
	}
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		return err
	}
	active := getActiveTurn(in.RootID, current.Key)
	if active == nil || staleCancelTarget(in, active) {
		return nil
	}
	if active.session != nil {
		// Let the runtime emit its own turn boundary after interrupt. Canceling
		// turnCtx first can dequeue the current waiter, so a late ResultMessage
		// may be delivered to the next queued turn.
		if err := active.session.CancelCurrentTurn(); err != nil {
			log.Printf("[session] turn.cancel.error root=%s session=%s err=%v", in.RootID, current.Key, err)
			active.cancel()
			return err
		}
		return nil
	}
	active.cancel()
	return nil
}

// SessionTurnActive lets background orchestration defer while the user is chatting.
// SendMessage's existing per-session lock remains the final serialization guard.
func SessionTurnActive(rootID, sessionKey string) bool {
	return getActiveTurn(rootID, sessionKey) != nil
}
