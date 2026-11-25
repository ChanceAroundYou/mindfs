package proxy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mindfs/server/internal/agent/fold"
)

// providerSwitchDirName holds one marker per Codex thread that was continued
// under a different model provider.
const providerSwitchDirName = "provider-switch"

// MarkProviderSwitch remembers that a Codex thread's history now spans two
// model providers. Codex carries `reasoning.encrypted_content` across the fork
// unchanged, and only the provider that produced it can decrypt it; the new
// provider rejects the whole request with `invalid_encrypted_content`. Dropping
// those items from the request is what keeps the conversation usable.
//
// The marker is written to disk because the fork's rollout keeps the old
// ciphertext forever: a process restart that forgot this decision would start
// forwarding invalid content again.
func (s *Server) MarkProviderSwitch(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if s == nil || threadID == "" {
		return
	}
	s.mu.Lock()
	if s.providerSwitched == nil {
		s.providerSwitched = make(map[string]struct{})
	}
	s.providerSwitched[threadID] = struct{}{}
	dir := s.providerSwitchDir
	s.mu.Unlock()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("[proxy] provider-switch.marker.mkdir dir=%s err=%v", dir, err)
		return
	}
	path := providerSwitchMarkerPath(dir, threadID)
	if err := os.WriteFile(path, []byte(threadID+"\n"), 0o600); err != nil {
		log.Printf("[proxy] provider-switch.marker.write thread=%s err=%v", ShortToken(threadID), err)
	}
}

// ProviderSwitchMarked reports whether this thread has been recorded as having
// crossed a model provider switch. It exists for callers outside the proxy
// (the pool) that need to verify the marker was written.
func (s *Server) ProviderSwitchMarked(threadID string) bool {
	return s.providerSwitchedThread(threadID)
}

// providerSwitchedThread reports whether this thread crossed a provider
// switch. The in-memory set answers the common case; a marker file makes the
// answer survive a restart.
func (s *Server) providerSwitchedThread(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if s == nil || threadID == "" {
		return false
	}
	s.mu.Lock()
	if _, ok := s.providerSwitched[threadID]; ok {
		s.mu.Unlock()
		return true
	}
	dir := s.providerSwitchDir
	s.mu.Unlock()
	if dir == "" {
		return false
	}
	if _, err := os.Stat(providerSwitchMarkerPath(dir, threadID)); err != nil {
		return false
	}
	s.mu.Lock()
	if s.providerSwitched == nil {
		s.providerSwitched = make(map[string]struct{})
	}
	s.providerSwitched[threadID] = struct{}{}
	s.mu.Unlock()
	return true
}

func providerSwitchMarkerPath(dir, threadID string) string {
	sum := sha256.Sum256([]byte(threadID))
	return filepath.Join(dir, hex.EncodeToString(sum[:16])+".switch")
}

// providerSwitchCiphertextPath holds the snapshot of ciphertext that was in
// flight when the thread crossed providers. See stripEncryptedInput.
func providerSwitchCiphertextPath(dir, threadID string) string {
	sum := sha256.Sum256([]byte(threadID))
	return filepath.Join(dir, hex.EncodeToString(sum[:16])+".ciphertext")
}

// fingerprintCiphertext identifies one ciphertext value. Legacy rollouts replay
// the exact same bytes on every request, so the fingerprint is stable; a value
// the new provider produces is different and therefore not in the snapshot.
func fingerprintCiphertext(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// providerSwitchCiphertextIDs loads the snapshot written on the first
// post-switch request. ok=false means no snapshot yet (first pass). An existing
// but empty snapshot is meaningful: it says the first post-switch request had
// nothing to strip, so later passes must not strip blindly.
func (s *Server) providerSwitchCiphertextIDs(threadID string) (map[string]struct{}, bool) {
	threadID = strings.TrimSpace(threadID)
	if s == nil || threadID == "" {
		return nil, false
	}
	s.mu.Lock()
	dir := s.providerSwitchDir
	s.mu.Unlock()
	if dir == "" {
		return nil, false
	}
	data, err := os.ReadFile(providerSwitchCiphertextPath(dir, threadID))
	if err != nil {
		return nil, false
	}
	ids := make(map[string]struct{})
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			ids[line] = struct{}{}
		}
	}
	return ids, true
}

// recordProviderSwitchCiphertextIDs persists the snapshot, merging with the
// one already on disk so a concurrent first pass cannot erase it. Writing an
// empty set is deliberate: it initialises the snapshot so that later passes
// become value-scoped instead of stripping every ciphertext they see.
//
// Scoping exists because the marker is permanent. An unscoped strip would also
// delete ciphertext the *new* provider produced after the switch — its own
// encrypted reasoning, collaboration `agent_message` payloads and compaction
// summaries — silently shortening long conversations. Scoping by value instead
// of item id also covers compacted items, which carry no id of their own.
func (s *Server) recordProviderSwitchCiphertextIDs(threadID string, ids map[string]struct{}) {
	threadID = strings.TrimSpace(threadID)
	if s == nil || threadID == "" {
		return
	}
	s.mu.Lock()
	dir := s.providerSwitchDir
	s.mu.Unlock()
	if dir == "" {
		return
	}
	path := providerSwitchCiphertextPath(dir, threadID)
	merged := make(map[string]struct{}, len(ids))
	for id := range ids {
		merged[id] = struct{}{}
	}
	if existing, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(existing), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "#") {
				merged[line] = struct{}{}
			}
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("[proxy] provider-switch.ciphertext.mkdir dir=%s err=%v", dir, err)
		return
	}
	sorted := make([]string, 0, len(merged))
	for id := range merged {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	data := []byte("#v1\n" + strings.Join(sorted, "\n") + "\n")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		log.Printf("[proxy] provider-switch.ciphertext.write thread=%s err=%v", ShortToken(threadID), err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("[proxy] provider-switch.ciphertext.rename thread=%s err=%v", ShortToken(threadID), err)
	}
}

// stripEncryptedReasoningInput is the unscoped form: it removes every
// ciphertext value it finds. Callers that already have a snapshot use
// stripEncryptedInput with the allowed set instead.
func stripEncryptedReasoningInput(body []byte) (out []byte, removed int, changed bool) {
	out, removed, changed, _ = stripEncryptedInput(body, nil)
	return out, removed, changed
}

// stripEncryptedInput removes ciphertext the new provider cannot decrypt from
// the request's Responses `input` array. The predicate is type-independent: any
// member named `encrypted_content` anywhere under an item whose value is a
// non-empty string is removed, and an item (or array element) left with nothing
// readable is dropped with it.
//
// The old predicate only knew `type:"reasoning"` items with a top-level
// `encrypted_content`. That was the shapes seen when it was written, not the
// shapes that occur: a wsl rollout census on 2026-11-25 found four carriers —
// reasoning items (34859), `agent_message.content[]` elements (798, plus 14
// nested in guardian messages), compacted `guardian_history` reasoning items
// (2658) and `replacement_history` `compaction` items (52). The 400 that
// prompted this change named an `amsg_…` agent_message, whose ciphertext sits
// inside `content[]`, so the old strip (which reported `items=232` for the same
// request) still sent ciphertext and the turn failed.
//
// Scoping: allowed == nil strips everything, which is correct on the first
// post-switch request (the history at that moment is entirely old) and in the
// 400 recovery path (the provider just proved the body is unusable). Once the
// snapshot exists, only ciphertext values listed in it are removed, so the new
// provider's own ciphertext survives. Scoping by value instead of by item id
// also reaches compacted items, which carry no id of their own.
//
// stripped reports a fingerprint of every removed ciphertext value, so the
// caller can extend the snapshot. It is nil when nothing changed.
//
// Everything outside the `input` array is copied verbatim. It fails open: a
// body that is not a JSON object, has no array-shaped `input`, or does not
// parse is returned unchanged with changed=false, so the proxy never turns a
// malformed forward into a dropped request.
func stripEncryptedInput(body []byte, allowed map[string]struct{}) (out []byte, removed int, changed bool, stripped map[string]struct{}) {
	open := fold.ScanSkipSpace(body, 0)
	if open >= len(body) || body[open] != '{' {
		return body, 0, false, nil
	}
	inputStart, inputEnd := -1, -1
	err := fold.ScanEachMember(body, open, func(key []byte, valueStart, valueEnd int) (bool, error) {
		if fold.MemberKeyIs(key, "input") {
			inputStart, inputEnd = valueStart, valueEnd
			return true, nil
		}
		return false, nil
	})
	if err != nil || inputStart < 0 || body[inputStart] != '[' {
		return body, 0, false, nil
	}
	pass := &ciphertextStrip{allowed: allowed}
	array, changed := stripCiphertextArray(body, inputStart, pass)
	if !changed {
		return body, 0, false, nil
	}
	result := make([]byte, 0, len(body)-(inputEnd-inputStart)+len(array))
	result = append(result, body[:inputStart]...)
	result = append(result, array...)
	result = append(result, body[inputEnd:]...)
	return result, pass.removed, true, pass.found
}

// ciphertextStrip carries the decisions of one stripping pass.
type ciphertextStrip struct {
	allowed map[string]struct{} // nil: strip every ciphertext value
	removed int
	found   map[string]struct{}
}

// take decides whether one ciphertext value may be removed, and records it.
func (s *ciphertextStrip) take(value string) bool {
	fp := fingerprintCiphertext(value)
	if s.allowed != nil {
		if _, ok := s.allowed[fp]; !ok {
			return false
		}
	}
	if s.found == nil {
		s.found = make(map[string]struct{})
	}
	s.found[fp] = struct{}{}
	s.removed++
	return true
}

// stripCiphertextArray rebuilds a JSON array, recursively stripping ciphertext
// from every object element and dropping elements that end up unreadable.
func stripCiphertextArray(b []byte, open int, pass *ciphertextStrip) (out []byte, changed bool) {
	var array bytes.Buffer
	array.Grow(64)
	array.WriteByte('[')
	first := true
	err := fold.ScanEachElement(b, open, func(valueStart, valueEnd int) error {
		value, valueChanged, drop := stripCiphertextValue(b, valueStart, pass)
		if drop {
			changed = true
			return nil
		}
		if valueChanged {
			changed = true
		} else {
			value = b[valueStart:valueEnd]
		}
		if !first {
			array.WriteByte(',')
		}
		first = false
		array.Write(value)
		return nil
	})
	if err != nil || !changed {
		return nil, false
	}
	array.WriteByte(']')
	return array.Bytes(), true
}

// stripCiphertextValue rewrites one JSON value. Non-container values are left
// alone; objects are handled member by member. drop=true means the value should
// disappear entirely.
func stripCiphertextValue(b []byte, start int, pass *ciphertextStrip) (out []byte, changed, drop bool) {
	if start >= len(b) {
		return nil, false, false
	}
	switch b[start] {
	case '{':
		return stripCiphertextObject(b, start, pass)
	case '[':
		out, changed = stripCiphertextArray(b, start, pass)
		return out, changed, false
	}
	return nil, false, false
}

// stripCiphertextObject removes every strippable `encrypted_content` member
// from an object and recurses into nested objects/arrays. It never inspects or
// edits tool calls, tool results, or visible text.
//
// drop=true means this object carried ciphertext but nothing a provider could
// read is left; the caller drops the element or member rather than send a
// husk. Items without any ciphertext are returned untouched (changed=false).
func stripCiphertextObject(b []byte, start int, pass *ciphertextStrip) (out []byte, changed, drop bool) {
	members, err := scanMemberSpans(b, start)
	if err != nil {
		// Unparseable object: keep it verbatim (fail-open).
		return nil, false, false
	}
	values := make([][]byte, len(members))
	keep := make([]bool, len(members))
	for i, m := range members {
		keep[i] = true
		if fold.MemberKeyIs(b[m.keyStart:m.keyEnd], "encrypted_content") {
			if value, err := fold.JSONStringAt(b, m.valueStart, m.valueEnd); err == nil && value != "" && pass.take(value) {
				keep[i] = false
				changed = true
				continue
			}
			values[i] = b[m.valueStart:m.valueEnd]
			continue
		}
		nested, nestedChanged, nestedDrop := stripCiphertextValue(b, m.valueStart, pass)
		if nestedChanged || nestedDrop {
			changed = true
			if nestedDrop {
				keep[i] = false
				continue
			}
			values[i] = nested
			continue
		}
		values[i] = b[m.valueStart:m.valueEnd]
	}
	if !changed {
		return nil, false, false
	}
	if !objectHasReadablePayload(b, members, keep, values) {
		return nil, true, true
	}

	var obj bytes.Buffer
	obj.Grow(len(b))
	obj.WriteByte('{')
	first := true
	for i, m := range members {
		if !keep[i] {
			continue
		}
		if !first {
			obj.WriteByte(',')
		}
		first = false
		obj.Write(b[m.keyStart:m.keyEnd])
		obj.WriteByte(':')
		obj.Write(values[i])
	}
	obj.WriteByte('}')
	return obj.Bytes(), true, false
}

// jsonMemberSpans records one object member: the quoted key and its value.
type jsonMemberSpans struct {
	keyStart, keyEnd     int
	valueStart, valueEnd int
}

// scanMemberSpans is the offset-carrying counterpart of fold.ScanEachMember.
// Rebuilding a member list needs the key bytes too, which that walker only hands
// over as a slice, not as a position.
func scanMemberSpans(b []byte, open int) ([]jsonMemberSpans, error) {
	if open >= len(b) || b[open] != '{' {
		return nil, fold.ErrScanMalformed
	}
	members := make([]jsonMemberSpans, 0, 6)
	i := open + 1
	for {
		i = fold.ScanSkipSpace(b, i)
		if i >= len(b) {
			return nil, fold.ErrScanMalformed
		}
		if b[i] == '}' {
			return members, nil
		}
		keyEnd, err := fold.ScanValueEnd(b, i, 0)
		if err != nil {
			return nil, err
		}
		j := fold.ScanSkipSpace(b, keyEnd)
		if j >= len(b) || b[j] != ':' {
			return nil, fold.ErrScanMalformed
		}
		valueStart := fold.ScanSkipSpace(b, j+1)
		valueEnd, err := fold.ScanValueEnd(b, valueStart, 0)
		if err != nil {
			return nil, err
		}
		members = append(members, jsonMemberSpans{keyStart: i, keyEnd: keyEnd, valueStart: valueStart, valueEnd: valueEnd})
		i = fold.ScanSkipSpace(b, valueEnd)
		if i >= len(b) {
			return nil, fold.ErrScanMalformed
		}
		switch b[i] {
		case ',':
			i++
		case '}':
			return members, nil
		default:
			return nil, fold.ErrScanMalformed
		}
	}
}

// nonPayloadMembers are item keys that identify an item but carry no content a
// provider could use. An item left with only these after stripping has nothing
// readable to send.
var nonPayloadMembers = map[string]struct{}{
	"type":      {},
	"id":        {},
	"role":      {},
	"status":    {},
	"author":    {},
	"recipient": {},
	"internal_chat_message_metadata_passthrough": {},
	"guardian_metadata":                          {},
}

// objectHasReadablePayload reports whether a sanitized object still carries
// anything besides identity/bookkeeping.
func objectHasReadablePayload(b []byte, members []jsonMemberSpans, keep []bool, values [][]byte) bool {
	for i, m := range members {
		if !keep[i] {
			continue
		}
		name, err := fold.JSONStringAt(b, m.keyStart, m.keyEnd)
		if err != nil {
			return true // unknown shape: fail open
		}
		if _, bookkeeping := nonPayloadMembers[name]; bookkeeping {
			continue
		}
		if value := values[i]; len(value) > 0 && !responsesValueIsEmpty(value, 0, len(value)) {
			return true
		}
	}
	return false
}

// responsesValueIsEmpty reports whether a JSON value is null, an empty string,
// an empty array or an empty object.
func responsesValueIsEmpty(b []byte, start, end int) bool {
	start = fold.ScanSkipSpace(b, start)
	if start >= end {
		return true
	}
	switch b[start] {
	case 'n':
		return true
	case '"':
		value, err := fold.JSONStringAt(b, start, end)
		return err == nil && value == ""
	case '[', '{':
		closer := byte(']')
		if b[start] == '{' {
			closer = '}'
		}
		i := fold.ScanSkipSpace(b, start+1)
		return i < end && b[i] == closer
	}
	return false
}

// invalidEncryptedContentMarker is the upstream error code that says a
// reasoning item's ciphertext could not be decrypted by the current provider.
const invalidEncryptedContentMarker = "invalid_encrypted_content"

// recoverInvalidEncryptedContent retries a 400 whose body reports that the
// provider could not decrypt ciphertext. This is the safety net for a thread
// that crossed providers without the marker (or before it was persisted): the
// first turn fails validation, the retry drops the ciphertext, and the thread
// is remembered so later turns strip proactively.
//
// It does not gate on providerSwitchedThread: the marker only means the
// proactive strip ran, and "already stripped" is exactly what the
// changed=false check below detects — a second gate on the same fact only made
// the net harder to reason about. The retry is still attempted at most once,
// and only when stripping actually changes the bytes.
//
// The retry reuses the same method, URL, and headers. Any deviation (different
// status, unreadable body, nothing to strip, second network error) returns the
// original response untouched.
func (s *Server) recoverInvalidEncryptedContent(resp *http.Response, original *http.Request, target *url.URL, threadID string, body []byte) *http.Response {
	if resp == nil || resp.StatusCode != http.StatusBadRequest {
		return resp
	}
	peek, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(peek))
	if err != nil || !bytes.Contains(peek, []byte(invalidEncryptedContentMarker)) {
		return resp
	}
	stripped, removed, changed, found := stripEncryptedInput(body, nil)
	if !changed {
		return resp
	}
	retry, err := http.NewRequestWithContext(original.Context(), original.Method, target.String(), bytes.NewReader(stripped))
	if err != nil {
		return resp
	}
	retry.Header = original.Header.Clone()
	retry.Header.Del("Host")
	retried, err := http.DefaultTransport.RoundTrip(retry)
	if err != nil {
		log.Printf("[proxy] provider-switch.recover.retry_error thread=%s err=%v", ShortToken(threadID), err)
		return resp
	}
	s.MarkProviderSwitch(threadID)
	// The 400 proved this body was unusable, so the recovery strips every
	// ciphertext value and unions them into the snapshot: those values are old
	// by definition, and later turns can stay value-scoped.
	s.recordProviderSwitchCiphertextIDs(threadID, found)
	log.Printf("[proxy] provider-switch.recover thread=%s items=%d status=%d", ShortToken(threadID), removed, retried.StatusCode)
	return retried
}
