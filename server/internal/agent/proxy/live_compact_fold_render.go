package proxy

import (
	"os"
)

// Pointer rendering for one fold candidate. Split out of live_compact_fold.go
// when that file crossed the 500-line hard limit; pure move, no behaviour change.

// renderPointer produces the pointer for one candidate, reusing the archive and
// the rendered literal when they are still valid.
func renderPointer(body []byte, candidate scanFoldCandidate, store *archiveStore, cache *pointerCache) (renderedFold, foldSkip) {
	spanLen := candidate.part.end - candidate.part.start
	if entry, hit := cache.get(candidate.key); hit {
		// The sweep can have removed the archive since the entry was written. A
		// pointer to a missing file is worse than a full-size result, so verify
		// before reusing it.
		if info, err := os.Stat(entry.path); err == nil && info.Size() == int64(entry.bytes) && len(entry.literal) < spanLen {
			return renderedFold{
				replacement: foldReplacement{start: candidate.part.start, end: candidate.part.end, literal: entry.literal},
				tokens:      entry.tokens,
				textBytes:   entry.bytes,
				savedBytes:  spanLen - len(entry.literal),
				path:        entry.path,
			}, foldOK
		}
	}
	text, err := jsonStringAt(body, candidate.part.start, candidate.part.end)
	if err != nil || text == "" {
		return renderedFold{}, foldSkipDecode
	}
	tokens := estimateTokens(text)
	archivePath, err := store.write(text)
	if err != nil {
		return renderedFold{}, foldSkipArchive
	}
	literal := encodeJSONStringLiteral(renderFoldPointer(pointerInput{
		toolName:     candidate.toolName,
		inputSummary: candidate.inputSummary,
		text:         text,
		tokens:       tokens,
		archivePath:  archivePath,
		errorText:    candidate.failed || looksLikeErrorText(text),
		failed:       candidate.failed,
	}))
	// A fold that is not smaller is not a fold. This is the backstop for the
	// excerpt bound above: whatever the reason, a pointer that costs more bytes
	// than the result it replaces would make every subsequent turn bigger,
	// permanently.
	if len(literal) >= spanLen {
		return renderedFold{}, foldSkipTooBig
	}
	cache.put(candidate.key, cachedPointer{
		literal: literal,
		path:    archivePath,
		tokens:  tokens,
		bytes:   len(text),
	})
	return renderedFold{
		replacement: foldReplacement{start: candidate.part.start, end: candidate.part.end, literal: literal},
		tokens:      tokens,
		textBytes:   len(text),
		savedBytes:  spanLen - len(literal),
		path:        archivePath,
	}, foldOK
}
