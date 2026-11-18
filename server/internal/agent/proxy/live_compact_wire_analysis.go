package proxy

// Round pairing and the fold-candidate analysis. This is the reference
// implementation Phase A reports from; the span scanner in
// live_compact_scan*.go is a deliberately separate implementation of the same
// rules, and TestFoldScannerAgreesWithReferenceAnalysis pins the two together.

// pairToolRounds returns rounds in assistant order. A round stays open until
// every tool_use id has a tool_result in a later user message.
func pairToolRounds(messages []wireMessage) []*wireToolRound {
	rounds := make([]*wireToolRound, 0, 8)
	byToolID := make(map[string]*wireToolRound)
	for index, msg := range messages {
		if msg.role == "assistant" {
			var round *wireToolRound
			for _, block := range msg.blocks {
				if block.typ != "tool_use" || block.toolUseID == "" {
					continue
				}
				if round == nil {
					round = &wireToolRound{
						assistantIndex: index,
						toolNames:      make(map[string]string),
					}
				}
				round.toolNames[block.toolUseID] = block.toolName
				round.pending++
				byToolID[block.toolUseID] = round
			}
			if round != nil {
				rounds = append(rounds, round)
			}
			continue
		}
		if msg.role != "user" {
			continue
		}
		seen := make(map[int]bool)
		for _, block := range msg.blocks {
			if block.typ != "tool_result" {
				continue
			}
			round, ok := byToolID[block.toolUseID]
			if !ok || round.closed {
				continue
			}
			if !seen[round.assistantIndex] {
				round.resultIndexes = append(round.resultIndexes, index)
				seen[round.assistantIndex] = true
			}
			round.pending--
			if round.pending <= 0 {
				round.closed = true
				round.closeIndex = index
			}
		}
	}
	return rounds
}

// analyzeWire plans a fold without changing anything.
//
// Candidate rules (Phase B1):
//   - only closed tool rounds outside the newest keepRounds rounds;
//   - never a failed result (is_error or error-looking content);
//   - only pure-text results (images/documents keep the block untouched);
//   - only results estimated at or above minBlockTokens.
func analyzeWire(req *wireRequest, keepRounds, minBlockTokens int) wireAnalysis {
	if keepRounds < 0 {
		keepRounds = 0
	}
	if minBlockTokens < 0 {
		minBlockTokens = 0
	}
	analysis := wireAnalysis{
		Messages: len(req.messages),
	}
	rounds := pairToolRounds(req.messages)
	analysis.Rounds = len(rounds)

	closed := make([]*wireToolRound, 0, len(rounds))
	for _, round := range rounds {
		if round.closed {
			closed = append(closed, round)
		}
	}
	analysis.ClosedRounds = len(closed)
	if len(closed) <= keepRounds {
		return analysis
	}
	eligible := closed[:len(closed)-keepRounds]

	for _, round := range eligible {
		for _, messageIndex := range round.resultIndexes {
			msg := req.messages[messageIndex]
			for _, block := range msg.blocks {
				if block.typ != "tool_result" {
					continue
				}
				name, ok := round.toolNames[block.toolUseID]
				if !ok || block.isError || block.nonText || block.text == "" {
					continue
				}
				tokens := estimateTokens(block.text)
				if tokens < minBlockTokens {
					continue
				}
				errorText := looksLikeErrorText(block.text)
				if errorText {
					analysis.ErrorCandidates++
				}
				analysis.Candidates = append(analysis.Candidates, foldCandidate{
					MessageIndex: messageIndex,
					BlockIndex:   block.index,
					ToolUseID:    block.toolUseID,
					ToolName:     name,
					Tokens:       tokens,
					Bytes:        len(block.text),
					ErrorText:    errorText,
				})
				analysis.CandidateTokens += tokens
				analysis.CandidateBytes += len(block.text)
			}
		}
	}
	return analysis
}
