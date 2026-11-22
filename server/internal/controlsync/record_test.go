package controlsync

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCanonicalJSONStableAcrossKeyOrderAndWhitespace(t *testing.T) {
	inputs := []string{
		`{"b":2,"a":1}`,
		"{\n  \"b\": 2,\n  \"a\": 1\n}",
		`{ "a" : 1 , "b" : 2 }`,
	}
	var want []byte
	for i, in := range inputs {
		got, err := CanonicalJSON(json.RawMessage(in))
		if err != nil {
			t.Fatalf("CanonicalJSON(%q): %v", in, err)
		}
		if i == 0 {
			want = got
			continue
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("CanonicalJSON(%q) = %s, want %s", in, got, want)
		}
	}
	if string(want) != `{"a":1,"b":2}` {
		t.Fatalf("canonical form = %s, want keys sorted", want)
	}
}

func TestCanonicalJSONTreatsStructAndMapAlike(t *testing.T) {
	type pair struct {
		B int `json:"b"`
		A int `json:"a"`
	}
	fromStruct, err := CanonicalJSON(pair{B: 2, A: 1})
	if err != nil {
		t.Fatalf("CanonicalJSON(struct): %v", err)
	}
	fromMap, err := CanonicalJSON(map[string]int{"b": 2, "a": 1})
	if err != nil {
		t.Fatalf("CanonicalJSON(map): %v", err)
	}
	if !bytes.Equal(fromStruct, fromMap) {
		t.Fatalf("struct = %s, map = %s; 同一对象换了类型就换 hash", fromStruct, fromMap)
	}
}

func TestCanonicalJSONPreservesBigIntegers(t *testing.T) {
	// 2^53+1：走 float64 会被舍入成 2^53，hash 就会随负载大小漂移。
	const big = int64(9007199254740993)
	got, err := CanonicalJSON(map[string]any{"n": big})
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if !strings.Contains(string(got), "9007199254740993") {
		t.Fatalf("big int lost precision: %s", got)
	}
}

func TestHashPayloadIgnoresTombstoneFlag(t *testing.T) {
	payload := json.RawMessage(`{"username":"alice"}`)
	live := Record{Domain: "users", Key: "alice", Payload: payload, UpdatedAt: time.Unix(1, 0)}
	dead := live
	dead.Deleted = true
	liveHash, err := HashRecord(live)
	if err != nil {
		t.Fatalf("HashRecord(live): %v", err)
	}
	deadHash, err := HashRecord(dead)
	if err != nil {
		t.Fatalf("HashRecord(dead): %v", err)
	}
	if liveHash != deadHash {
		t.Fatalf("删除改变了 payload hash：%s vs %s", liveHash, deadHash)
	}
	if !strings.HasPrefix(liveHash, "sha256:") {
		t.Fatalf("hash = %q, want sha256: 前缀", liveHash)
	}
}

func TestValidateRecordKey(t *testing.T) {
	valid := []string{"alice", "wf:abc:meta", "http://192.168.1.6:7331", strings.Repeat("k", 256)}
	for _, key := range valid {
		if err := ValidateRecordKey(key); err != nil {
			t.Errorf("ValidateRecordKey(%q) = %v, want nil", key, err)
		}
	}
	invalid := []string{"", " ", strings.Repeat("k", 257), "a\nb", "a\x00b", "a\x7fb"}
	for _, key := range invalid {
		if err := ValidateRecordKey(key); err == nil {
			t.Errorf("ValidateRecordKey(%q) = nil, want error", key)
		}
	}
}

func TestValidateRecordRejectsUnknownDomainShape(t *testing.T) {
	ok := Record{Domain: "users", Key: "alice", Payload: json.RawMessage(`{}`)}
	if err := ValidateRecord(ok); err != nil {
		t.Fatalf("ValidateRecord(ok) = %v", err)
	}
	for _, bad := range []Record{
		{Domain: "", Key: "alice"},
		{Domain: "Users", Key: "alice"},
		{Domain: "us ers", Key: "alice"},
		{Domain: "users", Key: ""},
	} {
		if err := ValidateRecord(bad); err == nil {
			t.Errorf("ValidateRecord(%+v) = nil, want error", bad)
		}
	}
}
