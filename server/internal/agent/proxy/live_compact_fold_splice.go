package proxy

// Splicing and verification. Both operate on byte spans produced by
// scanWireRequest; neither knows how the fold was planned.

// applyFold copies body once, substituting each replacement span. It reports
// false if the spans are not strictly increasing and in range, which the caller
// must treat as "do not rewrite".
func applyFold(body []byte, replacements []foldReplacement) ([]byte, bool) {
	delta := 0
	for _, replacement := range replacements {
		delta += len(replacement.literal) - (replacement.End - replacement.Start)
	}
	if delta >= 0 && delta > len(body) {
		// A rewrite that more than doubles the body is not a fold; it is a
		// bug. Refuse rather than forward something the CLI never sent.
		return nil, false
	}
	total := len(body) + delta
	if total <= 0 {
		return nil, false
	}
	out := make([]byte, 0, total)
	cursor := 0
	for _, replacement := range replacements {
		if replacement.Start < cursor || replacement.End > len(body) || replacement.Start >= replacement.End {
			return nil, false
		}
		out = append(out, body[cursor:replacement.Start]...)
		out = append(out, replacement.literal...)
		cursor = replacement.End
	}
	out = append(out, body[cursor:]...)
	return out, true
}

// verifyFold asserts the invariants a fold must never break. The caller passes
// the scan of the original body it already has, so only the rewritten body is
// scanned again: the check costs one scan of the folded body and runs only when
// a fold actually happened.
func verifyFold(before *scanRequest, folded []byte) error {
	after, err := scanWireRequest(folded)
	if err != nil {
		return errFoldVerify("rewritten body does not scan")
	}
	if len(before.messages) != len(after.messages) {
		return errFoldVerify("message count changed")
	}
	for index := range before.messages {
		if before.messages[index].role != after.messages[index].role {
			return errFoldVerify("message role changed")
		}
		if len(before.messages[index].blocks) != len(after.messages[index].blocks) {
			return errFoldVerify("block count changed")
		}
		for blockIndex := range before.messages[index].blocks {
			oldBlock := before.messages[index].blocks[blockIndex]
			newBlock := after.messages[index].blocks[blockIndex]
			if oldBlock.typ != newBlock.typ {
				return errFoldVerify("block type changed")
			}
			switch oldBlock.typ {
			case "tool_use":
				if oldBlock.id != newBlock.id || oldBlock.name != newBlock.name {
					return errFoldVerify("tool_use identity changed")
				}
			case "tool_result":
				if oldBlock.toolUseID != newBlock.toolUseID || oldBlock.isError != newBlock.isError {
					return errFoldVerify("tool_result identity changed")
				}
				if oldBlock.nonText != newBlock.nonText {
					return errFoldVerify("tool_result text shape changed")
				}
			}
		}
	}
	return nil
}

type foldVerifyError string

func (e foldVerifyError) Error() string { return "live-compact: fold verify: " + string(e) }

func errFoldVerify(reason string) error { return foldVerifyError(reason) }
