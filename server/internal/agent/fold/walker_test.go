package fold

import (
	"strings"
	"testing"
)

func TestScanEachMemberVisitsEveryMember(t *testing.T) {
	body := []byte(`{"a":1,"b":"x\"y","c":{"d":[1,2,{"e":null}]},"f":true}`)
	type member struct {
		key   string
		value string
	}
	var got []member
	err := ScanEachMember(body, 0, func(key []byte, vs, ve int) (bool, error) {
		got = append(got, member{key: string(key), value: string(body[vs:ve])})
		return false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("visited %d members, want 4: %+v", len(got), got)
	}
	if got[1].key != `"b"` || got[1].value != `"x\"y"` {
		t.Fatalf("escaped string member = %+v", got[1])
	}
	if got[2].value != `{"d":[1,2,{"e":null}]}` {
		t.Fatalf("nested object = %q", got[2].value)
	}
}

func TestScanEachMemberStopsWhenAsked(t *testing.T) {
	body := []byte(`{"a":1,"b":2,"c":3}`)
	seen := 0
	err := ScanEachMember(body, 0, func([]byte, int, int) (bool, error) {
		seen++
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Fatalf("visited %d members after stop, want 1", seen)
	}
}

func TestScanEachElementVisitsElements(t *testing.T) {
	body := []byte(`[{"a":1},"two",[3]]`)
	var got []string
	if err := ScanEachElement(body, 0, func(vs, ve int) error {
		got = append(got, string(body[vs:ve]))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != `{"a":1}|"two"|[3]` {
		t.Fatalf("elements = %v", got)
	}
}

func TestJSONStringAtDecodesEscapes(t *testing.T) {
	plain := []byte(`"hello"`)
	if got, err := JSONStringAt(plain, 0, len(plain)); err != nil || got != "hello" {
		t.Fatalf("plain = %q, %v", got, err)
	}
	escaped := []byte(`"a\nbé\"c"`)
	if got, err := JSONStringAt(escaped, 0, len(escaped)); err != nil || got != "a\nbé\"c" {
		t.Fatalf("escaped = %q, %v", got, err)
	}
	if _, err := JSONStringAt([]byte(`1`), 0, 1); err == nil {
		t.Fatal("non-string value decoded as a string")
	}
}

func TestScanValueEndHandlesNestingAndMalformed(t *testing.T) {
	body := []byte(`{"a":[1,{"b":"}"}],"c":2}`)
	end, err := ScanValueEnd(body, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if end != len(body) {
		t.Fatalf("value end = %d, want %d", end, len(body))
	}
	// A brace inside a string must not close the object.
	if _, err := ScanValueEnd([]byte(`{"a":"unterminated`), 0, 0); err == nil {
		t.Fatal("unterminated object scanned cleanly")
	}
	if _, err := ScanValueEnd([]byte(`{"a":`), 0, 0); err == nil {
		t.Fatal("truncated value scanned cleanly")
	}
}

func TestMemberKeyIsExactMatch(t *testing.T) {
	if !MemberKeyIs([]byte(`"file_path"`), "file_path") {
		t.Fatal("exact key did not match")
	}
	if MemberKeyIs([]byte(`"file_paths"`), "file_path") {
		t.Fatal("longer key matched")
	}
	if MemberKeyIs([]byte(`"file\u005fpath"`), "file_path") {
		t.Fatal("escaped key matched; escaped keys are treated as unknown")
	}
}

func TestScanSkipSpace(t *testing.T) {
	body := []byte(" \t\r\n x")
	if got := ScanSkipSpace(body, 0); got != 5 {
		t.Fatalf("skip space = %d, want 5", got)
	}
	if got := ScanSkipSpace(body, len(body)); got != len(body) {
		t.Fatalf("skip at end = %d", got)
	}
}
