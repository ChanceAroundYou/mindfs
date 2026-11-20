package anthropic

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mindfs/server/internal/agent/fold"
)

// Equivalence tests for the Messages adapter.
//
// The rewrite is only allowed onto the wire if it changes exactly the bytes it
// means to. These tests pin the four properties that make that true: an empty
// splice is a no-op, the span scan agrees with a reference parser on every
// fixture, encoding round-trips, and a one-span splice leaves the rest alone.

func loadFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob("testdata/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 5 {
		t.Fatalf("expected at least 5 fixtures, found %d", len(paths))
	}
	bodies := make(map[string][]byte, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		bodies[filepath.Base(path)] = body
	}
	return bodies
}

func TestAdapterEmptySpliceIsByteIdentical(t *testing.T) {
	a := New()
	for name, body := range loadFixtures(t) {
		out, err := a.Splice(body, nil)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(out, body) {
			t.Fatalf("%s: empty splice changed bytes", name)
		}
	}
}

// referenceResult is what encoding/json says about one tool_result.
type referenceResult struct {
	callID  string
	failed  bool
	nonText bool
}

// referenceRounds returns the closed rounds a second, independent parser sees.
func referenceRounds(t *testing.T, body []byte) [][]referenceResult {
	t.Helper()
	var request struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("reference parse: %v", err)
	}
	type round struct {
		ids      []string
		results  []referenceResult
		answered map[string]int
	}
	var rounds []*round
	byCall := make(map[string]*round)
	for _, message := range request.Messages {
		var blocks []struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
			Content   json.RawMessage `json:"content"`
		}
		if len(message.Content) == 0 || message.Content[0] != '[' {
			continue
		}
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			t.Fatalf("reference parse blocks: %v", err)
		}
		switch message.Role {
		case "assistant":
			var current *round
			for _, block := range blocks {
				if block.Type != "tool_use" || block.ID == "" {
					continue
				}
				if current == nil {
					current = &round{answered: make(map[string]int)}
					rounds = append(rounds, current)
				}
				current.ids = append(current.ids, block.ID)
				byCall[block.ID] = current
			}
		case "user":
			for _, block := range blocks {
				if block.Type != "tool_result" {
					continue
				}
				owner, ok := byCall[block.ToolUseID]
				if !ok {
					continue
				}
				if _, done := owner.answered[block.ToolUseID]; done {
					continue
				}
				owner.answered[block.ToolUseID] = len(owner.results)
				result := referenceResult{callID: block.ToolUseID, failed: block.IsError}
				var parts []struct {
					Type string `json:"type"`
				}
				if len(block.Content) > 0 && block.Content[0] == '[' {
					if err := json.Unmarshal(block.Content, &parts); err != nil {
						t.Fatalf("reference parse content: %v", err)
					}
					for _, part := range parts {
						if part.Type != "text" {
							result.nonText = true
						}
					}
				}
				owner.results = append(owner.results, result)
			}
		}
	}
	closed := make([][]referenceResult, 0, len(rounds))
	for _, item := range rounds {
		if len(item.results) < len(item.ids) {
			continue
		}
		closed = append(closed, item.results)
	}
	return closed
}

func TestAdapterScanAgreesWithReference(t *testing.T) {
	a := New()
	for name, body := range loadFixtures(t) {
		conv, err := a.Scan(body)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if conv.Estimated <= 0 {
			t.Fatalf("%s: no estimate", name)
		}
		var closed [][]fold.Result
		for _, round := range conv.Rounds {
			if len(round.Results) > 0 {
				closed = append(closed, round.Results)
			}
		}
		want := referenceRounds(t, body)
		if len(closed) != len(want) {
			t.Fatalf("%s: closed rounds = %d, reference = %d", name, len(closed), len(want))
		}
		for index := range want {
			if len(closed[index]) != len(want[index]) {
				t.Fatalf("%s round %d: results = %d, reference = %d", name, index, len(closed[index]), len(want[index]))
			}
			for resultIndex := range want[index] {
				got := closed[index][resultIndex]
				expected := want[index][resultIndex]
				if got.CallID != expected.callID || got.Failed != expected.failed || got.NonText != expected.nonText {
					t.Fatalf("%s round %d result %d: %+v, reference %+v", name, index, resultIndex, got, expected)
				}
				if !got.NonText && len(got.Parts) != 1 {
					t.Fatalf("%s round %d result %d: parts = %d", name, index, resultIndex, len(got.Parts))
				}
			}
		}
	}
}

func TestAdapterOpenRoundHasNoResults(t *testing.T) {
	body, err := os.ReadFile("testdata/open_round.json")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := New().Scan(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(conv.Rounds) != 1 || len(conv.Rounds[0].Results) != 0 {
		t.Fatalf("open round = %+v", conv.Rounds)
	}
}

func TestAdapterTextRejectsNonText(t *testing.T) {
	body, err := os.ReadFile("testdata/image_result.json")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := New().Scan(body)
	if err != nil {
		t.Fatal(err)
	}
	result := conv.Rounds[0].Results[0]
	if !result.NonText {
		t.Fatal("image result is not marked non-text")
	}
	if _, ok := New().Text(body, result); ok {
		t.Fatal("text of a non-text result must be refused")
	}
}

func TestAdapterEncodeRoundTripsAndKeepsAngles(t *testing.T) {
	// A control character is the discriminator that matters: strconv.Quote would
	// write it as \x01, which is not JSON; json.Encoder writes \u0001.
	text := "a<b>&c é \n end \x01 quote\" back\\slash"
	literal := New().Encode(text)
	// The CLI reads the request back as JSON, so HTML escaping would be legal
	// but wasteful; every extra backslash is bytes the pointer does not save.
	if strings.Contains(literal, `\u003c`) || strings.Contains(literal, `\u0026`) {
		t.Fatalf("literal escapes HTML: %s", literal)
	}
	decoded, err := fold.JSONStringAt([]byte(literal), 0, len(literal))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(literal)) {
		t.Fatalf("literal is not valid JSON: %s", literal)
	}
	if decoded != text {
		t.Fatalf("round trip = %q, want %q", decoded, text)
	}
}

func TestAdapterSpliceReplacesOnlyTheSpan(t *testing.T) {
	body, err := os.ReadFile("testdata/multi_round.json")
	if err != nil {
		t.Fatal(err)
	}
	a := New()
	conv, err := a.Scan(body)
	if err != nil {
		t.Fatal(err)
	}
	span := conv.Rounds[0].Results[0].Parts[0]
	literal := a.Encode("pointer")
	out, err := a.Splice(body, []fold.Replacement{{Span: span, Literal: literal}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out[:span.Start], body[:span.Start]) {
		t.Fatal("bytes before the span changed")
	}
	if !bytes.Equal(out[span.Start+len(literal):], body[span.End:]) {
		t.Fatal("bytes after the span changed")
	}
	if string(out[span.Start:span.Start+len(literal)]) != literal {
		t.Fatal("literal not spliced")
	}
	reparsed, err := a.Scan(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(reparsed.Rounds) != len(conv.Rounds) {
		t.Fatal("round count changed")
	}
	if reparsed.Rounds[0].Results[0].CallID != conv.Rounds[0].Results[0].CallID {
		t.Fatal("pairing changed")
	}
}

// bigResultBody builds a two-round Messages request whose tool results are big
// enough to clear every fold threshold.
func bigResultBody() []byte {
	text := strings.Repeat("x", 20000)
	call := func(id string) string {
		return fmt.Sprintf(`{"type":"tool_use","id":%q,"name":"Read","input":{"file_path":"/tmp/%s.txt"}}`, id, id)
	}
	result := func(id string) string {
		return fmt.Sprintf(`{"type":"tool_result","tool_use_id":%q,"content":%q}`, id, text)
	}
	return []byte(`{"model":"claude-sonnet-4-5","messages":[` +
		`{"role":"user","content":"go"},` +
		`{"role":"assistant","content":[` + call("toolu_1") + `]},` +
		`{"role":"user","content":[` + result("toolu_1") + `]},` +
		`{"role":"assistant","content":[` + call("toolu_2") + `]},` +
		`{"role":"user","content":[` + result("toolu_2") + `]}` +
		`]}`)
}

func TestAdapterFoldsWithEngine(t *testing.T) {
	cfg := fold.Config{
		Mode:             fold.ModeOn,
		RailTokens:       -1,
		KeepRounds:       0,
		KeepTailTokens:   1 << 30,
		MinBlockTokens:   10,
		LargeBlockTokens: 1 << 30,
		CacheTTLSeconds:  300,
		ColdRailTokens:   60000,
	}
	engine := fold.NewEngine(cfg, New(), fold.NewArchiveStore(t.TempDir(), time.Hour, fold.DefaultMaxArchiveBytes), fold.NewPointerCache())
	body := bigResultBody()
	out, err := engine.Fold(fold.Request{
		Body:        body,
		Session:     &fold.Session{Commits: fold.NewCommits()},
		Now:         time.Now(),
		FoldAllowed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Folded || out.NewBlocks != 2 {
		t.Fatalf("outcome = %+v", out)
	}
	if !json.Valid(out.Body) {
		t.Fatal("folded body is not valid JSON")
	}
	if bytes.Contains(out.Body, []byte(strings.Repeat("x", 20000))) {
		t.Fatal("a folded result is still present in full")
	}
	reparsed, err := New().Scan(out.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(reparsed.Rounds) != 2 {
		t.Fatalf("rounds after fold = %d", len(reparsed.Rounds))
	}
}

func TestAdapterUnknownModelSurfacesFromEngine(t *testing.T) {
	cfg := fold.Config{Mode: fold.ModeOn, RailTokens: -1, KeepTailTokens: 1 << 30, MinBlockTokens: 10, LargeBlockTokens: 1 << 30}
	engine := fold.NewEngine(cfg, New(), fold.NewArchiveStore(t.TempDir(), time.Hour, fold.DefaultMaxArchiveBytes), fold.NewPointerCache())
	body := []byte(`{"model":"ok","messages":[{"role":"user","content":"hi"}]}`)
	out, err := engine.Fold(fold.Request{Body: body, Session: &fold.Session{Commits: fold.NewCommits()}, FoldAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.GuardUnknownModel {
		t.Fatalf("unknown alias not reported: %+v", out)
	}
}
