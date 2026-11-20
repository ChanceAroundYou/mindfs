package anthropic

import "mindfs/server/internal/agent/fold"

// Pairing: one assistant tool_use message becomes one round. A round is closed
// only when every call in it has an answer; an open round is represented by
// zero results so the planner leaves it alone.

type pendingRound struct {
	results []fold.Result
	filled  []bool
	pending int
}

type callRef struct {
	round  int
	result int
}

// conversation turns a scanned request into the neutral shape the engine plans
// on. Byte spans stay untouched: the adapter never rewrites what it did not
// scan.
func conversation(body []byte, req *scanRequest) *fold.Conversation {
	conv := &fold.Conversation{
		Model:     req.model,
		Estimated: fold.EstimateByteTokens(body),
	}
	rounds := make([]*pendingRound, 0, 8)
	byCall := make(map[string]callRef)
	for _, msg := range req.messages {
		switch msg.role {
		case "assistant":
			var round *pendingRound
			for _, block := range msg.blocks {
				if block.typ != "tool_use" || block.id == "" {
					continue
				}
				if round == nil {
					round = &pendingRound{}
					rounds = append(rounds, round)
				}
				summary := ""
				if block.input.Valid() {
					summary = fold.SummarizeToolInput(body, block.input)
				}
				byCall[block.id] = callRef{round: len(rounds) - 1, result: len(round.results)}
				round.results = append(round.results, fold.Result{
					CallID:       block.id,
					ToolName:     block.name,
					InputSummary: summary,
				})
				round.filled = append(round.filled, false)
				round.pending++
			}
		case "user":
			for _, block := range msg.blocks {
				if block.typ != "tool_result" {
					continue
				}
				ref, ok := byCall[block.toolUseID]
				if !ok {
					continue
				}
				round := rounds[ref.round]
				if round.filled[ref.result] {
					continue
				}
				result := &round.results[ref.result]
				result.Parts = block.textParts
				result.NonText = block.nonText
				result.Failed = block.isError
				for _, part := range block.textParts {
					result.Bytes += part.End - part.Start
					result.Tokens += fold.EstimateByteTokens(body[part.Start:part.End])
				}
				round.filled[ref.result] = true
				round.pending--
			}
		}
	}
	for _, round := range rounds {
		if round.pending > 0 {
			conv.Rounds = append(conv.Rounds, fold.Round{})
			continue
		}
		conv.Rounds = append(conv.Rounds, fold.Round{Results: round.results})
	}
	return conv
}
