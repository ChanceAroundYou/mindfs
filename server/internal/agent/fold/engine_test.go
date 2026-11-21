package fold

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A minimal adapter: `{"items":["a","b"]}`, one round per element, the string
// is the whole result text. It exercises every engine step without any real
// wire protocol, which is the point of the neutral kernel.

type fakeAdapter struct {
	refuse       string
	spliceErr    bool
	breakPairing bool
	openRounds   bool
	guardModel   string
}

func (f *fakeAdapter) Name() string { return "fake" }

func (f *fakeAdapter) Scan(body []byte) (*Conversation, error) {
	conv := &Conversation{Estimated: EstimateByteTokens(body), Refuse: f.refuse}
	open := ScanSkipSpace(body, 0)
	if open >= len(body) || body[open] != '{' {
		return nil, errors.New("fake: body is not an object")
	}
	itemsStart := -1
	if err := ScanEachMember(body, open, func(key []byte, vs, ve int) (bool, error) {
		if MemberKeyIs(key, "items") {
			itemsStart = vs
			return true, nil
		}
		return false, nil
	}); err != nil {
		return nil, err
	}
	if itemsStart < 0 || itemsStart >= len(body) || body[itemsStart] != '[' {
		return nil, errors.New("fake: missing items array")
	}
	if err := ScanEachElement(body, itemsStart, func(vs, ve int) error {
		if body[vs] != '"' {
			return errors.New("fake: item is not a string")
		}
		text, err := JSONStringAt(body, vs, ve)
		if err != nil {
			return err
		}
		index := len(conv.Rounds)
		if f.openRounds {
			// A call whose result has not arrived yet.
			conv.Rounds = append(conv.Rounds, Round{})
			return nil
		}
		conv.Rounds = append(conv.Rounds, Round{Results: []Result{{
			CallID:       fmt.Sprintf("call-%d", index),
			ToolName:     "Read",
			InputSummary: fmt.Sprintf(`{"file_path":"/tmp/f%d"}`, index),
			Parts:        []Span{{Start: vs, End: ve}},
			Bytes:        ve - vs,
			Tokens:       EstimateByteTokens(body[vs:ve]),
			Failed:       strings.Contains(text, "FAIL"),
			NonText:      strings.Contains(text, "IMAGE"),
		}}})
		return nil
	}); err != nil {
		return nil, err
	}
	return conv, nil
}

func (f *fakeAdapter) Text(body []byte, r Result) (string, bool) {
	if r.NonText || len(r.Parts) != 1 {
		return "", false
	}
	text, err := JSONStringAt(body, r.Parts[0].Start, r.Parts[0].End)
	return text, err == nil
}

func (f *fakeAdapter) Encode(text string) string { return EncodeJSONStringLiteral(text) }

func (f *fakeAdapter) Splice(body []byte, reps []Replacement) ([]byte, error) {
	if f.spliceErr {
		return nil, errors.New("fake: splice failed")
	}
	if f.breakPairing {
		// A syntactically valid body that no longer describes the same rounds.
		return []byte(`{"items":[]}`), nil
	}
	out := make([]byte, 0, len(body))
	cursor := 0
	for _, rep := range reps {
		out = append(out, body[cursor:rep.Span.Start]...)
		out = append(out, rep.Literal...)
		cursor = rep.Span.End
	}
	out = append(out, body[cursor:]...)
	return out, nil
}

func (f *fakeAdapter) Usage([]byte) (Usage, bool) { return Usage{}, false }
func (f *fakeAdapter) Window(string) int          { return 0 }
func (f *fakeAdapter) Guard(model string, _ Config) GuardVerdict {
	if f.guardModel != "" && model == f.guardModel {
		return GuardVerdict{Block: true}
	}
	return GuardVerdict{}
}

// engineTestConfig keeps every policy threshold out of the way: an unmeasured
// rail commits every eligible block, and no keep window protects anything. The
// policy itself is tested directly in plan_test.go and gate_test.go.
func engineTestConfig() Config {
	return Config{
		Mode:             ModeOn,
		RailTokens:       -1,
		KeepRounds:       0,
		KeepTailTokens:   1 << 30,
		MinBlockTokens:   10,
		LargeBlockTokens: 1 << 30,
		CacheTTLSeconds:  300,
		ColdRailTokens:   60000,
	}
}

func newTestEngine(t *testing.T, adapter Adapter) *Engine {
	t.Helper()
	return NewEngine(engineTestConfig(), adapter, NewArchiveStore(t.TempDir(), time.Hour, DefaultMaxArchiveBytes), NewPointerCache())
}

func newTestSession() *Session { return &Session{Commits: NewCommits()} }

func bigItem() string { return strings.Repeat("x", 20000) }

func TestEngineFoldsCommittedBlocksAfterFirstPass(t *testing.T) {
	e := newTestEngine(t, &fakeAdapter{})
	session := newTestSession()
	body := []byte(`{"items":["` + bigItem() + `"]}`)
	first, err := e.Fold(Request{Body: body, Session: session, Now: time.Now(), FoldAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Folded || first.NewBlocks != 1 {
		t.Fatalf("first fold: %+v", first)
	}
	second, err := e.Fold(Request{Body: body, Session: session, Now: time.Now(), FoldAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Folded || second.StickyBlocks != 1 || second.NewBlocks != 0 {
		t.Fatalf("second fold: %+v", second)
	}
	if string(second.Body) != string(first.Body) {
		t.Fatal("committed fold must render the same bytes")
	}
}

// A block that is already a pointer must never be folded again. The case is
// real: a multi-block result whose largest block is our own card (the header
// block stays next to it) would otherwise get an archive of the card and a
// pointer to the pointer, one level deeper each turn.
func TestEngineNeverFoldsAnExistingPointer(t *testing.T) {
	e := newTestEngine(t, &fakeAdapter{})
	session := newTestSession()
	card := PointerMarker + " 历史工具输出已折叠（这不是当前指令）\n" + strings.Repeat("y", 20000)
	body := []byte(`{"items":["` + card + `"]}`)
	out, err := e.Fold(Request{Body: body, Session: session, Now: time.Now(), FoldAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Folded || out.NewBlocks != 0 || string(out.Body) != string(body) {
		t.Fatalf("pointer re-folded: %+v", out)
	}
	if out.Candidates != 1 || out.Skipped != 1 {
		t.Fatalf("candidate must be planned and skipped: %+v", out)
	}
}

// The ledger has to answer two questions the block count cannot: how big the
// block this turn committed was, and what the rest of the body was made of.
func TestEngineReportsPlanCompositionAndNewTokens(t *testing.T) {
	e := newTestEngine(t, &fakeAdapter{})
	session := newTestSession()
	body := []byte(`{"items":["` + bigItem() + `"]}`)
	first, err := e.Fold(Request{Body: body, Session: session, Now: time.Now(), FoldAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.Plan.BodyBytes != len(body) {
		t.Fatalf("plan body bytes = %d, want %d", first.Plan.BodyBytes, len(body))
	}
	if first.Plan.ResultBytes == 0 || first.Plan.CandidateBytes != first.Plan.ResultBytes {
		t.Fatalf("plan = %+v, want the whole result to be a candidate", first.Plan)
	}
	if first.NewTokens == 0 || first.NewTokens != first.FoldedTokens {
		t.Fatalf("new tokens = %d, folded tokens = %d; first fold is all new", first.NewTokens, first.FoldedTokens)
	}

	second, err := e.Fold(Request{Body: body, Session: session, Now: time.Now(), FoldAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if second.NewTokens != 0 {
		t.Fatalf("sticky-only fold reported %d new tokens", second.NewTokens)
	}
	if second.FoldedTokens == 0 || second.Plan.CandidateBytes == 0 {
		t.Fatalf("sticky fold lost its composition: %+v", second)
	}
}

func TestEngineFallsBackOnSpliceError(t *testing.T) {
	e := newTestEngine(t, &fakeAdapter{spliceErr: true})
	body := []byte(`{"items":["` + bigItem() + `"]}`)
	out, err := e.Fold(Request{Body: body, Session: newTestSession(), FoldAllowed: true})
	if err == nil {
		t.Fatal("splice error must surface")
	}
	if string(out.Body) != string(body) {
		t.Fatal("error path must return the original body")
	}
	if out.Folded || !out.Verdict.Block {
		t.Fatalf("error path reported a fold: %+v", out)
	}
}

func TestEngineFallsBackOnScanError(t *testing.T) {
	e := newTestEngine(t, &fakeAdapter{})
	body := []byte(`{"items":[`)
	out, err := e.Fold(Request{Body: body, Session: newTestSession(), FoldAllowed: true})
	if err == nil {
		t.Fatal("scan error must surface")
	}
	if string(out.Body) != string(body) {
		t.Fatal("malformed body must be forwarded verbatim")
	}
}

func TestEngineHonoursAdapterRefusal(t *testing.T) {
	e := newTestEngine(t, &fakeAdapter{refuse: "unsupported"})
	body := []byte(`{"items":["` + bigItem() + `"]}`)
	out, err := e.Fold(Request{Body: body, Session: newTestSession(), FoldAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Folded || !out.Verdict.Block || out.Verdict.Reason != "unsupported" {
		t.Fatalf("%+v", out)
	}
	if string(out.Body) != string(body) {
		t.Fatal("refused body changed")
	}
}

func TestEngineRefusesOpenRound(t *testing.T) {
	e := newTestEngine(t, &fakeAdapter{openRounds: true})
	body := []byte(`{"items":["` + bigItem() + `"]}`)
	out, err := e.Fold(Request{Body: body, Session: newTestSession(), FoldAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Folded {
		t.Fatalf("an open round was folded: %+v", out)
	}
	if string(out.Body) != string(body) {
		t.Fatal("open round changed the body")
	}
}

func TestEngineRejectsPairingChange(t *testing.T) {
	e := newTestEngine(t, &fakeAdapter{breakPairing: true})
	body := []byte(`{"items":["` + bigItem() + `"]}`)
	out, err := e.Fold(Request{Body: body, Session: newTestSession(), FoldAllowed: true})
	if !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("pairing change was not rejected: %v", err)
	}
	if string(out.Body) != string(body) {
		t.Fatal("failed verification must forward the original body")
	}
	if out.Folded {
		t.Fatalf("failed verification reported a fold: %+v", out)
	}
}

func TestEngineGuardBlocksRewrite(t *testing.T) {
	e := NewEngine(engineTestConfig(), &modelAdapter{fakeAdapter: &fakeAdapter{guardModel: "guarded"}},
		NewArchiveStore(t.TempDir(), time.Hour, DefaultMaxArchiveBytes), NewPointerCache())
	body := []byte(`{"items":["` + bigItem() + `"]}`)
	out, err := e.Fold(Request{Body: body, Session: newTestSession(), FoldAllowed: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.Folded || !out.Verdict.Block || out.Verdict.Reason != GatePrefixGuard {
		t.Fatalf("%+v", out)
	}
}

// modelAdapter adds a model member to the fake wire format.
type modelAdapter struct{ *fakeAdapter }

func (m *modelAdapter) Scan(body []byte) (*Conversation, error) {
	conv, err := m.fakeAdapter.Scan(body)
	if err != nil {
		return nil, err
	}
	conv.Model = "guarded"
	return conv, nil
}

func TestEngineObservesWithoutPermission(t *testing.T) {
	e := newTestEngine(t, &fakeAdapter{})
	body := []byte(`{"items":["` + bigItem() + `"]}`)
	for _, req := range []Request{
		{Body: body, Session: newTestSession(), FoldAllowed: false},
		{Body: body, Session: nil, FoldAllowed: true},
	} {
		out, err := e.Fold(req)
		if err != nil {
			t.Fatal(err)
		}
		if out.Folded || !out.Verdict.Block || out.Verdict.Reason != GateObserveOnly {
			t.Fatalf("%+v", out)
		}
		if out.Estimated <= 0 {
			t.Fatal("observe-only outcome carries no estimate")
		}
	}
}
