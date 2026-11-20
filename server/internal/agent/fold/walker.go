package fold

import (
	"bytes"
	"encoding/json"
	"errors"
)

// A byte-offset JSON walker shared by every adapter.
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
//
// What lives here is byte traversal only. What a wire shape *means* — which
// part is a tool result, which round it belongs to — stays in each adapter,
// because the two protocols disagree on exactly that.

// ErrScanMalformed reports a body the walker cannot traverse. Adapters treat it
// as "this request cannot be folded": the body is forwarded untouched.
var ErrScanMalformed = errors.New("live-compact: malformed json")

const scanMaxDepth = 64

// Span is a byte range [Start, End) into the request body.
type Span struct{ Start, End int }

// Valid reports whether the span covers at least one byte.
func (s Span) Valid() bool { return s.End > s.Start }

// Raw returns the bytes the span covers.
func (s Span) Raw(b []byte) []byte { return b[s.Start:s.End] }

func isJSONDelimiter(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', ',', ':', '}', ']':
		return true
	}
	return false
}

// ScanSkipSpace returns the index of the first non-whitespace byte at or after i.
func ScanSkipSpace(b []byte, i int) int {
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
		return 0, ErrScanMalformed
	}
	j := i + 1
	for j < len(b) {
		offset := bytes.IndexByte(b[j:], '"')
		if offset < 0 {
			return 0, ErrScanMalformed
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
	return 0, ErrScanMalformed
}

// ScanValueEnd returns the index just past the JSON value starting at b[i].
func ScanValueEnd(b []byte, i, depth int) (int, error) {
	if depth > scanMaxDepth || i >= len(b) {
		return 0, ErrScanMalformed
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
			return 0, ErrScanMalformed
		}
		return i, nil
	}
}

func scanObjectEnd(b []byte, open, depth int) (int, error) {
	i := open + 1
	for {
		i = ScanSkipSpace(b, i)
		if i >= len(b) {
			return 0, ErrScanMalformed
		}
		if b[i] == '}' {
			return i + 1, nil
		}
		keyEnd, err := scanStringEnd(b, i)
		if err != nil {
			return 0, err
		}
		i = ScanSkipSpace(b, keyEnd)
		if i >= len(b) || b[i] != ':' {
			return 0, ErrScanMalformed
		}
		i, err = ScanValueEnd(b, ScanSkipSpace(b, i+1), depth+1)
		if err != nil {
			return 0, err
		}
		i = ScanSkipSpace(b, i)
		if i >= len(b) {
			return 0, ErrScanMalformed
		}
		switch b[i] {
		case ',':
			i++
		case '}':
			return i + 1, nil
		default:
			return 0, ErrScanMalformed
		}
	}
}

func scanArrayEnd(b []byte, open, depth int) (int, error) {
	i := open + 1
	for {
		i = ScanSkipSpace(b, i)
		if i >= len(b) {
			return 0, ErrScanMalformed
		}
		if b[i] == ']' {
			return i + 1, nil
		}
		var err error
		i, err = ScanValueEnd(b, i, depth+1)
		if err != nil {
			return 0, err
		}
		i = ScanSkipSpace(b, i)
		if i >= len(b) {
			return 0, ErrScanMalformed
		}
		switch b[i] {
		case ',':
			i++
		case ']':
			return i + 1, nil
		default:
			return 0, ErrScanMalformed
		}
	}
}

// ScanEachMember visits every member of the object starting at b[open].
// memberKey is the raw quoted key including its quotes. Returning stop=true
// ends the walk at that member.
func ScanEachMember(b []byte, open int, visit func(memberKey []byte, valueStart, valueEnd int) (stop bool, err error)) error {
	if open >= len(b) || b[open] != '{' {
		return ErrScanMalformed
	}
	i := open + 1
	for {
		i = ScanSkipSpace(b, i)
		if i >= len(b) {
			return ErrScanMalformed
		}
		if b[i] == '}' {
			return nil
		}
		keyStart := i
		keyEnd, err := scanStringEnd(b, i)
		if err != nil {
			return err
		}
		i = ScanSkipSpace(b, keyEnd)
		if i >= len(b) || b[i] != ':' {
			return ErrScanMalformed
		}
		valueStart := ScanSkipSpace(b, i+1)
		valueEnd, err := ScanValueEnd(b, valueStart, 0)
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
		i = ScanSkipSpace(b, valueEnd)
		if i >= len(b) {
			return ErrScanMalformed
		}
		switch b[i] {
		case ',':
			i++
		case '}':
			return nil
		default:
			return ErrScanMalformed
		}
	}
}

// ScanEachElement visits every element of the array starting at b[open].
func ScanEachElement(b []byte, open int, visit func(valueStart, valueEnd int) error) error {
	if open >= len(b) || b[open] != '[' {
		return ErrScanMalformed
	}
	i := open + 1
	for {
		i = ScanSkipSpace(b, i)
		if i >= len(b) {
			return ErrScanMalformed
		}
		if b[i] == ']' {
			return nil
		}
		valueStart := i
		valueEnd, err := ScanValueEnd(b, valueStart, 0)
		if err != nil {
			return err
		}
		if visit != nil {
			if err := visit(valueStart, valueEnd); err != nil {
				return err
			}
		}
		i = ScanSkipSpace(b, valueEnd)
		if i >= len(b) {
			return ErrScanMalformed
		}
		switch b[i] {
		case ',':
			i++
		case ']':
			return nil
		default:
			return ErrScanMalformed
		}
	}
}

// MemberKeyIs matches a raw quoted object key against name. Keys are short and
// ASCII on both wire shapes; an escaped key simply does not match and the block
// is treated as unknown, which is the fail-open direction.
func MemberKeyIs(memberKey []byte, name string) bool {
	return len(memberKey) == len(name)+2 && string(memberKey[1:len(memberKey)-1]) == name
}

// JSONStringAt decodes the string literal at valueStart. The fast path skips
// encoding/json entirely when the literal has no escapes.
func JSONStringAt(b []byte, valueStart, valueEnd int) (string, error) {
	if valueStart >= valueEnd || b[valueStart] != '"' {
		return "", ErrScanMalformed
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
