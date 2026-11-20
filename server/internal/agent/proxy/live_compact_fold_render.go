package proxy

import (
	"os"

	"mindfs/server/internal/agent/fold"
)

// Pointer rendering for one fold candidate. Split out of live_compact_fold.go
// when that file crossed the 500-line hard limit; pure move, no behaviour change.

// renderPointer produces the pointer for one candidate, reusing the archive and
// the rendered literal when they are still valid.
func renderPointer(body []byte, candidate scanFoldCandidate, store *archiveStore, cache *pointerCache) (renderedFold, foldSkip) {
	spanLen := candidate.part.End - candidate.part.Start
	if entry, hit := cache.Get(candidate.key); hit {
		// The sweep can have removed the archive since the entry was written. A
		// pointer to a missing file is worse than a full-size result, so verify
		// before reusing it.
		if info, err := os.Stat(entry.Path); err == nil && info.Size() == int64(entry.Bytes) && len(entry.Literal) < spanLen {
			return renderedFold{
				replacement: foldReplacement{Start: candidate.part.Start, End: candidate.part.End, literal: entry.Literal},
				tokens:      entry.Tokens,
				textBytes:   entry.Bytes,
				savedBytes:  spanLen - len(entry.Literal),
				path:        entry.Path,
			}, foldOK
		}
	}
	text, err := jsonStringAt(body, candidate.part.Start, candidate.part.End)
	if err != nil || text == "" {
		return renderedFold{}, foldSkipDecode
	}
	tokens := estimateTokens(text)
	archivePath, err := store.Write(text)
	if err != nil {
		return renderedFold{}, foldSkipArchive
	}
	literal := encodeJSONStringLiteral(fold.RenderPointer(fold.PointerInput{
		ToolName:     candidate.toolName,
		InputSummary: candidate.inputSummary,
		Text:         text,
		ArchivePath:  archivePath,
		ErrorText:    candidate.failed || looksLikeErrorText(text),
		Failed:       candidate.failed,
	}))
	// A fold that is not smaller is not a fold. This is the backstop for the
	// excerpt bound above: whatever the reason, a pointer that costs more bytes
	// than the result it replaces would make every subsequent turn bigger,
	// permanently.
	if len(literal) >= spanLen {
		return renderedFold{}, foldSkipTooBig
	}
	cache.Put(candidate.key, cachedPointer{
		Literal: literal,
		Path:    archivePath,
		Tokens:  tokens,
		Bytes:   len(text),
	})
	return renderedFold{
		replacement: foldReplacement{Start: candidate.part.Start, End: candidate.part.End, literal: literal},
		tokens:      tokens,
		textBytes:   len(text),
		savedBytes:  spanLen - len(literal),
		path:        archivePath,
	}, foldOK
}
