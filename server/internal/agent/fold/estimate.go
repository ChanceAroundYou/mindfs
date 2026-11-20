package fold

import "unicode/utf8"

// EstimateTokens approximates the token count of a text block without a
// tokenizer. ASCII runs at roughly 4 bytes per token; every non-ASCII rune
// counts as one token, which is conservative for CJK (about 1 token per
// character) and for code.
//
// The fold thresholds are coarse (2k per block, 20k/32k per chunk), so a
// ±30% error is acceptable. Phase A compares this estimate against the real
// usage reported by the upstream so the constants can be calibrated.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	ascii := 0
	nonASCII := 0
	for i := 0; i < len(s); i++ {
		if s[i] < utf8.RuneSelf {
			ascii++
			continue
		}
		// Count rune starts, not bytes. Invalid continuation bytes cannot
		// start a rune, so a malformed UTF-8 sequence still contributes at
		// most one token per leading byte — an overestimate, never an
		// underestimate that would silently fold too little.
		if utf8.RuneStart(s[i]) {
			nonASCII++
		}
	}
	return (ascii+3)/4 + nonASCII
}

// EstimateByteTokens is EstimateTokens without the string copy. Request
// bodies can be tens of megabytes; Phase A estimates them on every large
// request.
func EstimateByteTokens(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	ascii := 0
	nonASCII := 0
	for _, c := range b {
		if c < utf8.RuneSelf {
			ascii++
			continue
		}
		if utf8.RuneStart(c) {
			nonASCII++
		}
	}
	return (ascii+3)/4 + nonASCII
}
