package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
)

// A byte-offset JSON walker for the Anthropic Messages request body.
//
// Why not encoding/json: the CLI resends the whole history every turn, so the
// body is multi-megabyte and everything in it is re-sent verbatim. Decoding it
// into Go values costs 436 ms and 58 MB of allocations per 8 MB of body
// (measured 2026-11-18, see design §12.2). Folding only needs to know where a
// handful of string literals are, so this walker records byte ranges and leaves
// every other byte untouched — which is also what makes invariant I6 (the
// prefix before the first modified block is byte-identical) true by
// construction rather than by careful re-encoding.
//
// String bodies are skipped with bytes.IndexByte plus a backslash-parity check,
// so unescaped runs cost one memchr instead of one branch per byte. Decoding a
// string (including escape processing) is deliberately NOT done here; callers
// decode only the literals they actually need, which in practice is a few
// tool_result texts per request.

var errScanMalformed = errors.New("live-compact: malformed json")

const scanMaxDepth = 64

type span struct{ start, end int }

func (s span) valid() bool { return s.end > s.start }

func (s span) raw(b []byte) []byte { return b[s.start:s.end] }

func isJSONDelimiter(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', ',', ':', '}', ']':
		return true
	}
	return false
}

func scanSkipSpace(b []byte, i int) int {
	for i < len(b) {
		switch b[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

// scanStringEnd returns the index just past the closing quote of the string
// literal starting at b[i]. b[i] must be '"'.
func scanStringEnd(b []byte, i int) (int, error) {
	if i >= len(b) || b[i] != '"' {
		return 0, errScanMalformed
	}
	j := i + 1
	for j < len(b) {
		offset := bytes.IndexByte(b[j:], '"')
		if offset < 0 {
			return 0, errScanMalformed
		}
		end := j + offset
		// An odd number of consecutive backslashes means this quote is escaped.
		backslashes := 0
		for p := end - 1; p > i && b[p] == '\\'; p-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return end + 1, nil
		}
		j = end + 1
	}
	return 0, errScanMalformed
}

// scanValueEnd returns the index just past the JSON value starting at b[i].
func scanValueEnd(b []byte, i, depth int) (int, error) {
	if depth > scanMaxDepth || i >= len(b) {
		return 0, errScanMalformed
	}
	switch b[i] {
	case '"':
		return scanStringEnd(b, i)
	case '{':
		return scanObjectEnd(b, i, depth)
	case '[':
		return scanArrayEnd(b, i, depth)
	default:
		start := i
		for i < len(b) && !isJSONDelimiter(b[i]) {
			i++
		}
		if i == start {
			return 0, errScanMalformed
		}
		return i, nil
	}
}

func scanObjectEnd(b []byte, open, depth int) (int, error) {
	i := open + 1
	for {
		i = scanSkipSpace(b, i)
		if i >= len(b) {
			return 0, errScanMalformed
		}
		if b[i] == '}' {
			return i + 1, nil
		}
		keyEnd, err := scanStringEnd(b, i)
		if err != nil {
			return 0, err
		}
		i = scanSkipSpace(b, keyEnd)
		if i >= len(b) || b[i] != ':' {
			return 0, errScanMalformed
		}
		i, err = scanValueEnd(b, scanSkipSpace(b, i+1), depth+1)
		if err != nil {
			return 0, err
		}
		i = scanSkipSpace(b, i)
		if i >= len(b) {
			return 0, errScanMalformed
		}
		switch b[i] {
		case ',':
			i++
		case '}':
			return i + 1, nil
		default:
			return 0, errScanMalformed
		}
	}
}

func scanArrayEnd(b []byte, open, depth int) (int, error) {
	i := open + 1
	for {
		i = scanSkipSpace(b, i)
		if i >= len(b) {
			return 0, errScanMalformed
		}
		if b[i] == ']' {
			return i + 1, nil
		}
		var err error
		i, err = scanValueEnd(b, i, depth+1)
		if err != nil {
			return 0, err
		}
		i = scanSkipSpace(b, i)
		if i >= len(b) {
			return 0, errScanMalformed
		}
		switch b[i] {
		case ',':
			i++
		case ']':
			return i + 1, nil
		default:
			return 0, errScanMalformed
		}
	}
}

// scanEachMember visits every member of the object starting at b[open].
// memberKey is the raw quoted key including its quotes. Returning stop=true
// ends the walk at that member.
func scanEachMember(b []byte, open int, visit func(memberKey []byte, valueStart, valueEnd int) (stop bool, err error)) error {
	if open >= len(b) || b[open] != '{' {
		return errScanMalformed
	}
	i := open + 1
	for {
		i = scanSkipSpace(b, i)
		if i >= len(b) {
			return errScanMalformed
		}
		if b[i] == '}' {
			return nil
		}
		keyStart := i
		keyEnd, err := scanStringEnd(b, i)
		if err != nil {
			return err
		}
		i = scanSkipSpace(b, keyEnd)
		if i >= len(b) || b[i] != ':' {
			return errScanMalformed
		}
		valueStart := scanSkipSpace(b, i+1)
		valueEnd, err := scanValueEnd(b, valueStart, 0)
		if err != nil {
			return err
		}
		if visit != nil {
			stop, err := visit(b[keyStart:keyEnd], valueStart, valueEnd)
			if err != nil {
				return err
			}
			if stop {
				return nil
			}
		}
		i = scanSkipSpace(b, valueEnd)
		if i >= len(b) {
			return errScanMalformed
		}
		switch b[i] {
		case ',':
			i++
		case '}':
			return nil
		default:
			return errScanMalformed
		}
	}
}

// scanEachElement visits every element of the array starting at b[open].
func scanEachElement(b []byte, open int, visit func(valueStart, valueEnd int) error) error {
	if open >= len(b) || b[open] != '[' {
		return errScanMalformed
	}
	i := open + 1
	for {
		i = scanSkipSpace(b, i)
		if i >= len(b) {
			return errScanMalformed
		}
		if b[i] == ']' {
			return nil
		}
		valueStart := i
		valueEnd, err := scanValueEnd(b, valueStart, 0)
		if err != nil {
			return err
		}
		if visit != nil {
			if err := visit(valueStart, valueEnd); err != nil {
				return err
			}
		}
		i = scanSkipSpace(b, valueEnd)
		if i >= len(b) {
			return errScanMalformed
		}
		switch b[i] {
		case ',':
			i++
		case ']':
			return nil
		default:
			return errScanMalformed
		}
	}
}

// memberKeyIs matches a raw quoted object key against name. Keys are short and
// ASCII on this wire shape; an escaped key simply does not match and the block
// is treated as unknown, which is the fail-open direction.
func memberKeyIs(memberKey []byte, name string) bool {
	return len(memberKey) == len(name)+2 && string(memberKey[1:len(memberKey)-1]) == name
}

// jsonStringAt decodes the string literal at valueStart. The fast path skips
// encoding/json entirely when the literal has no escapes.
func jsonStringAt(b []byte, valueStart, valueEnd int) (string, error) {
	if valueStart >= valueEnd || b[valueStart] != '"' {
		return "", errScanMalformed
	}
	raw := b[valueStart:valueEnd]
	if bytes.IndexByte(raw, '\\') < 0 {
		return string(raw[1 : len(raw)-1]), nil
	}
	var decoded string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", err
	}
	return decoded, nil
}
