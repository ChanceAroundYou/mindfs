package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"unicode/utf8"
)

func (m *Manager) Search(_ context.Context, opts SearchOptions) ([]SearchHit, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" || utf8.RuneCountInString(query) < 2 {
		return []SearchHit{}, nil
	}
	limit := normalizeSearchLimit(opts.Limit)
	qLower := strings.ToLower(query)

	m.mu.Lock()
	sessions, err := m.listSessionMetasUnsafe()
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}

	nameHits := make([]SearchHit, 0, limit)
	hitsByKey := make(map[string]SearchHit, limit)
	for _, item := range sessions {
		score := scoreSessionName(item.Name, qLower)
		if score <= 0 {
			continue
		}
		hit := buildSearchHit(item, "name", score, 0, item.Name)
		nameHits = append(nameHits, hit)
		hitsByKey[item.Key] = hit
	}
	sortSearchHits(nameHits)
	if len(nameHits) >= limit {
		return append([]SearchHit(nil), nameHits[:limit]...), nil
	}

	for _, item := range sessions {
		if len(hitsByKey) >= limit {
			break
		}
		if _, exists := hitsByKey[item.Key]; exists {
			continue
		}
		hit, ok, err := m.searchSessionContent(item, qLower)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		hitsByKey[item.Key] = hit
	}

	results := make([]SearchHit, 0, len(hitsByKey))
	for _, hit := range hitsByKey {
		results = append(results, hit)
	}
	sortSearchHits(results)
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func normalizeSearchLimit(limit int) int {
	switch {
	case limit <= 0:
		return 20
	case limit > 50:
		return 50
	default:
		return limit
	}
}

func scoreSessionName(name, qLower string) int {
	name = strings.TrimSpace(name)
	if name == "" || qLower == "" {
		return 0
	}
	nameLower := strings.ToLower(name)
	switch {
	case nameLower == qLower:
		return 120
	case strings.HasPrefix(nameLower, qLower):
		return 100
	case strings.Contains(nameLower, qLower):
		return 80
	default:
		return 0
	}
}

func buildSearchHit(s *Session, matchType string, score, seq int, snippet string) SearchHit {
	return SearchHit{
		Key:              s.Key,
		Type:             s.Type,
		ParentSessionKey: s.ParentSessionKey,
		ParentToolCallID: s.ParentToolCallID,
		Agent:            InferAgentFromSession(s),
		Model:            s.Model,
		Shell:            s.Shell,
		Name:             s.Name,
		CreatedAt:        s.CreatedAt,
		UpdatedAt:        s.UpdatedAt,
		ClosedAt:         s.ClosedAt,
		MatchType:        matchType,
		MatchScore:       score,
		Seq:              seq,
		Snippet:          strings.TrimSpace(snippet),
	}
}

func sortSearchHits(items []SearchHit) {
	sort.SliceStable(items, func(i, j int) bool {
		left := items[i]
		right := items[j]
		if left.MatchType != right.MatchType {
			return searchMatchTypeRank(left.MatchType) < searchMatchTypeRank(right.MatchType)
		}
		if left.MatchScore != right.MatchScore {
			return left.MatchScore > right.MatchScore
		}
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		return left.Key < right.Key
	})
}

func searchMatchTypeRank(matchType string) int {
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

// searchSessionContent 搜一个会话的正文。**先搜块流，再扫主日志**（CUSTOM(G-BH)，S2d-2）。
//
// 为什么顺序是这样：S2d-2 起新写入的行 `content` 恒空，正文只在块流里，所以块流是
// 主路径；主日志那一遍是为了**还没回填的老会话**（回填是读时惰性的，见
// `usecase.backfillTurnsFromWindow`，会话没被读过就还没块）。
//
// 两遍都不能省。省掉块流那遍 = 搜不到任何新内容（静默失效、无报错）；
// 省掉主日志那遍 = 回填之前的老会话搜不到，用户会觉得「以前能搜的词现在搜不到了」。
//
// 只搜块的 **text 与 reasoning**，不搜工具卡：工具卡的 title/callId/status 与正文
// 是两回事，把它们算成命中会让搜索结果与用户看到的正文对不上（实测里「用 grep 找」
// 这类工具名会命中一大片）。reasoning 要搜 —— 「只思考不出话」的轮次全部由它承载。
func (m *Manager) searchSessionContent(s *Session, qLower string) (SearchHit, bool, error) {
	best, found, err := m.bestHitIn(s, qLower, m.searchableBlockTexts)
	if err != nil || found {
		return best, found, err
	}
	return m.bestHitIn(s, qLower, m.searchableLegacyTexts)
}

// searchableBlockTexts 从块流取可搜文本。错误时返回 nil（退回主日志那遍）。
func (m *Manager) searchableBlockTexts(s *Session) ([]searchableText, error) {
	blocks, err := m.ReadBlocks(context.Background(), s.Key)
	if err != nil {
		return nil, err
	}
	out := make([]searchableText, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != BlockTypeText && block.Type != BlockTypeReasoning {
			continue
		}
		text := strings.TrimSpace(block.Text)
		if text == "" {
			continue
		}
		// 块自己不带角色，回查主日志那一行。user 行必须在那里 —— addExchangeForAgentAt
		// 的跳过 guard 不拦它们（否则 user 命中优先失效，见该 guard 的注释）。
		role := "agent"
		for _, ex := range s.Exchanges {
			if ex.Seq == block.Seq {
				role = strings.ToLower(strings.TrimSpace(ex.Role))
				break
			}
		}
		out = append(out, searchableText{Seq: block.Seq, Role: role, Text: text})
	}
	return out, nil
}

// searchableLegacyTexts 从主日志取老数据的正文（S2d-2 之后新写入的行这里是空的）。
func (m *Manager) searchableLegacyTexts(s *Session) ([]searchableText, error) {
	path, err := m.exchangePath(s.Key)
	if err != nil {
		return nil, err
	}
	file, err := m.root.OpenMetaFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	// 单行上限与主日志解析一致（maxExchangeLineBytes = 64MB）。曾经写死 1MB，
	// 于是任何含超长行的会话都会因 bufio.ErrTooLong 整个搜不到（Search 直接返回错误）。
	scanner.Buffer(make([]byte, 0, 64*1024), maxExchangeLineBytes)
	var out []searchableText
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry Exchange
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		text := strings.TrimSpace(entry.LegacyContent)
		if text == "" {
			continue
		}
		out = append(out, searchableText{Seq: entry.Seq, Role: entry.Role, Text: text})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// searchableText 是一条可搜文本。打分与择优沿用 searchSessionContent 原有的口径：
// user 行优先（65 分 / match_type=user），reply 次之（60 分）；同档时取命中位置更靠前
// 的、再取 seq 更小的 —— 于是「跳转到命中」落在最像用户意图的那一轮。
type searchableText struct {
	Seq  int
	Role string
	Text string
}

func (m *Manager) bestHitIn(s *Session, qLower string, load func(*Session) ([]searchableText, error)) (SearchHit, bool, error) {
	candidates, err := load(s)
	if err != nil {
		return SearchHit{}, false, err
	}
	best := SearchHit{}
	bestFound := false
	bestRoleUser := false
	bestPos := 0
	bestSeq := 0
	for _, candidate := range candidates {
		content := strings.TrimSpace(candidate.Text)
		if content == "" {
			continue
		}
		lowerContent := strings.ToLower(content)
		pos := strings.Index(lowerContent, qLower)
		if pos < 0 {
			continue
		}
		roleUser := strings.EqualFold(strings.TrimSpace(candidate.Role), "user")
		matchRunes := utf8.RuneCountInString(lowerContent[:pos])
		queryRunes := utf8.RuneCountInString(qLower)
		matchType := "reply"
		matchScore := 60
		if roleUser {
			matchType = "user"
			matchScore = 65
		}
		hit := buildSearchHit(s, matchType, matchScore, candidate.Seq, buildSearchSnippet(content, matchRunes, queryRunes))
		if !bestFound || roleUser && !bestRoleUser || roleUser == bestRoleUser && (pos < bestPos || pos == bestPos && candidate.Seq < bestSeq) {
			best = hit
			bestFound = true
			bestRoleUser = roleUser
			bestPos = pos
			bestSeq = candidate.Seq
		}
	}
	if !bestFound {
		return SearchHit{}, false, nil
	}
	return best, true, nil
}

func buildSearchSnippet(content string, matchRunes, queryRunes int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	runes := []rune(content)
	start := 0
	end := len(runes)
	if matchRunes >= 0 {
		const contextBefore = 10
		const contextAfter = 18
		start = matchRunes - contextBefore
		if start < 0 {
			start = 0
		}
		end = matchRunes + queryRunes + contextAfter
		if end > len(runes) {
			end = len(runes)
		}
	}
	snippet := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(runes) {
		snippet += "..."
	}
	return snippet
}
