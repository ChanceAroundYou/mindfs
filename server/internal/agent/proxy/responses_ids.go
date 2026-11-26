package proxy

import (
	"bytes"
	"strings"

	"mindfs/server/internal/agent/fold"
)

// normalizeInputMessageIDs drops input-item ids the Responses API rejects on
// prefix grounds.
//
// The official API requires an input item of type `message` to carry an id
// beginning with `msg_`; any other prefix fails the whole request with
// `Invalid 'input[N].id': 'item_…'. Expected an ID that begins with 'msg'`.
// Codex synthesizes the messages it creates locally with `item_<hex>` ids (the
// pre-sampling compact prompt is the one seen in the wild, 2026-11-25 on wsl),
// and llmux tolerates that, so the request only starts failing after the thread
// is forked onto the official provider. Removing the id is exactly the shape
// Codex sends for ordinary user messages and input ids are optional, so the
// item itself survives untouched.
//
// Only top-level `input` elements are rewritten, and only message-shaped ones
// (`type:"message"`, or no `type` but a `role`). Everything else — reasoning,
// tool calls, compacted blobs — is copied byte for byte.
func normalizeInputMessageIDs(body []byte) (out []byte, removed int, changed bool) {
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
	var array bytes.Buffer
	array.Grow(len(body[inputStart:inputEnd]))
	array.WriteByte('[')
	first := true
	scanErr := fold.ScanEachElement(body, inputStart, func(valueStart, valueEnd int) error {
		value := body[valueStart:valueEnd]
		if valueStart < len(body) && body[valueStart] == '{' {
			if rewritten, ok := normalizeMessageIDElement(body, valueStart); ok {
				value = rewritten
				removed++
				changed = true
			}
		}
		if !first {
			array.WriteByte(',')
		}
		first = false
		array.Write(value)
		return nil
	})
	if scanErr != nil || !changed {
		return body, 0, false
	}
	array.WriteByte(']')
	result := make([]byte, 0, len(body)-(inputEnd-inputStart)+array.Len())
	result = append(result, body[:inputStart]...)
	result = append(result, array.Bytes()...)
	result = append(result, body[inputEnd:]...)
	return result, removed, true
}

// normalizeMessageIDElement rewrites one top-level input item if it is a
// message whose id the API would reject. ok=false means "leave the bytes
// alone".
func normalizeMessageIDElement(b []byte, start int) ([]byte, bool) {
	members, err := scanMemberSpans(b, start)
	if err != nil {
		return nil, false
	}
	isMessage := false
	hasType := false
	hasRole := false
	idIndex := -1
	for i, m := range members {
		name, err := fold.JSONStringAt(b, m.keyStart, m.keyEnd)
		if err != nil {
			return nil, false
		}
		switch name {
		case "type":
			hasType = true
			if value, err := fold.JSONStringAt(b, m.valueStart, m.valueEnd); err == nil && value == "message" {
				isMessage = true
			}
		case "role":
			hasRole = true
		case "id":
			idIndex = i
		}
	}
	// `type` is optional for input messages; without it the API still treats an
	// item with a role as one, and the same prefix rule applies.
	if !isMessage && !(hasRole && !hasType) {
		return nil, false
	}
	if idIndex < 0 {
		return nil, false
	}
	id, err := fold.JSONStringAt(b, members[idIndex].valueStart, members[idIndex].valueEnd)
	if err != nil || strings.HasPrefix(id, "msg_") {
		return nil, false
	}
	var obj bytes.Buffer
	obj.Grow(len(b))
	obj.WriteByte('{')
	first := true
	for i, m := range members {
		if i == idIndex {
			continue
		}
		if !first {
			obj.WriteByte(',')
		}
		first = false
		obj.Write(b[m.keyStart:m.keyEnd])
		obj.WriteByte(':')
		obj.Write(b[m.valueStart:m.valueEnd])
	}
	obj.WriteByte('}')
	return obj.Bytes(), true
}

// stripInputItemIDs removes the top-level `id` member from every element of
// the Responses `input` array.
//
// Codex 0.146+ preserves prefixed response-item ids when it replays history.
// Those ids are minted by the upstream that produced the item, and Codex always
// sends `store:false`. After a provider switch the new upstream cannot resolve
// an id minted by the old one, and there is no stored copy to fall back on, so
// the whole turn dies with:
//
//	404 Not Found: Item with id 'rs_…' not found. Items are not persisted when
//	`store` is set to false. Try again with `store` set to true, or remove this
//	item from your input.
//
// The item's readable content is already in the body; only the reference is
// unusable. Dropping `id` restores the stateless shape Codex used before 0.146,
// which every Responses upstream accepts. `call_id`/`call_*` pairing and ids
// nested inside content parts are untouched.
func stripInputItemIDs(body []byte) (out []byte, removed int, changed bool) {
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
	var array bytes.Buffer
	array.Grow(len(body[inputStart:inputEnd]))
	array.WriteByte('[')
	first := true
	scanErr := fold.ScanEachElement(body, inputStart, func(valueStart, valueEnd int) error {
		value := body[valueStart:valueEnd]
		if len(value) > 0 && value[0] == '{' {
			if rewritten, dropped, ok := stripItemIDElement(value); ok {
				removed += dropped
				changed = true
				if rewritten == nil {
					return nil // item_reference has no purpose without its id
				}
				value = rewritten
			}
		}
		if !first {
			array.WriteByte(',')
		}
		first = false
		array.Write(value)
		return nil
	})
	if scanErr != nil || !changed {
		return body, 0, false
	}
	array.WriteByte(']')
	result := make([]byte, 0, len(body)-(inputEnd-inputStart)+array.Len())
	result = append(result, body[:inputStart]...)
	result = append(result, array.Bytes()...)
	result = append(result, body[inputEnd:]...)
	return result, removed, true
}

// stripItemIDElement rebuilds one input item without its top-level `id`
// member. An item_reference has nothing left once the id is gone, so it is
// dropped instead of being forwarded as an empty reference.
func stripItemIDElement(value []byte) ([]byte, int, bool) {
	members, err := scanMemberSpans(value, 0)
	if err != nil {
		return nil, 0, false
	}
	idIndex := -1
	itemReference := false
	for i, m := range members {
		name, err := fold.JSONStringAt(value, m.keyStart, m.keyEnd)
		if err != nil {
			return nil, 0, false
		}
		switch name {
		case "id":
			idIndex = i
		case "type":
			if t, err := fold.JSONStringAt(value, m.valueStart, m.valueEnd); err == nil && t == "item_reference" {
				itemReference = true
			}
		}
	}
	if idIndex < 0 {
		return nil, 0, false
	}
	if itemReference {
		return nil, 1, true
	}
	var obj bytes.Buffer
	obj.Grow(len(value))
	obj.WriteByte('{')
	first := true
	for i, m := range members {
		if i == idIndex {
			continue
		}
		if !first {
			obj.WriteByte(',')
		}
		first = false
		obj.Write(value[m.keyStart:m.keyEnd])
		obj.WriteByte(':')
		obj.Write(value[m.valueStart:m.valueEnd])
	}
	obj.WriteByte('}')
	return obj.Bytes(), 1, true
}
