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

// stripEncryptedReasoningInput removes Responses `reasoning` items that carry
// `encrypted_content` from the request's `input` array. Everything outside that
// array is copied verbatim: only the ciphertext the new provider cannot read
// disappears, the visible transcript and tool calls stay.
//
// It fails open. A body that is not a JSON object, has no array-shaped `input`,
// or does not parse is returned unchanged with changed=false, so the proxy
// never turns a malformed forward into a dropped request.
func stripEncryptedReasoningInput(body []byte) (out []byte, removed int, changed bool) {
	open := fold.ScanSkipSpace(body, 0)
	if open >= len(body) || body[open] != '{' {
		return body, 0, false
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
		return body, 0, false
	}

	type span struct{ start, end int }
	kept := make([]span, 0, 8)
	err = fold.ScanEachElement(body, inputStart, func(valueStart, valueEnd int) error {
		if responsesItemHasEncryptedReasoning(body, valueStart, valueEnd) {
			removed++
			return nil
		}
		kept = append(kept, span{start: valueStart, end: valueEnd})
		return nil
	})
	if err != nil || removed == 0 {
		return body, 0, false
	}

	var array bytes.Buffer
	array.Grow(inputEnd - inputStart)
	array.WriteByte('[')
	for i, item := range kept {
		if i > 0 {
			array.WriteByte(',')
		}
		array.Write(body[item.start:item.end])
	}
	array.WriteByte(']')

	result := make([]byte, 0, len(body)-(inputEnd-inputStart)+array.Len())
	result = append(result, body[:inputStart]...)
	result = append(result, array.Bytes()...)
	result = append(result, body[inputEnd:]...)
	return result, removed, true
}

// responsesItemHasEncryptedReasoning reports whether one `input` element is a
// reasoning item carrying non-empty ciphertext.
func responsesItemHasEncryptedReasoning(body []byte, valueStart, valueEnd int) bool {
	if valueStart >= len(body) || body[valueStart] != '{' {
		return false
	}
	kind := ""
	encrypted := false
	_ = fold.ScanEachMember(body, valueStart, func(key []byte, start, end int) (bool, error) {
		switch {
		case fold.MemberKeyIs(key, "type"):
			if value, err := fold.JSONStringAt(body, start, end); err == nil {
				kind = value
			}
		case fold.MemberKeyIs(key, "encrypted_content"):
			if value, err := fold.JSONStringAt(body, start, end); err == nil && value != "" {
				encrypted = true
			}
		}
		return false, nil
	})
	return kind == "reasoning" && encrypted
}

// invalidEncryptedContentMarker is the upstream error code that says a
// reasoning item's ciphertext could not be decrypted by the current provider.
const invalidEncryptedContentMarker = "invalid_encrypted_content"

// recoverInvalidEncryptedContent retries a 400 whose body reports that the
// provider could not decrypt reasoning ciphertext. This is the safety net for
// threads that were forked onto a new provider before the marker existed: the
// first turn fails validation, the retry drops the ciphertext, and the thread
// is remembered so later turns strip proactively.
//
// The retry reuses the same method, URL, and headers and is attempted at most
// once. Any deviation (different status, unreadable body, nothing to strip,
// second network error) returns the original response untouched.
func (s *Server) recoverInvalidEncryptedContent(resp *http.Response, original *http.Request, target *url.URL, threadID string, body []byte) *http.Response {
	if resp == nil || resp.StatusCode != http.StatusBadRequest || s.providerSwitchedThread(threadID) {
		return resp
	}
	peek, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(peek))
	if err != nil || !bytes.Contains(peek, []byte(invalidEncryptedContentMarker)) {
		return resp
	}
	stripped, removed, changed := stripEncryptedReasoningInput(body)
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
	log.Printf("[proxy] provider-switch.recover thread=%s items=%d status=%d", ShortToken(threadID), removed, retried.StatusCode)
	return retried
}
