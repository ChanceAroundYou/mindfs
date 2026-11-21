package feishu

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeProjectCatalog struct {
	refs []ProjectRef
	err  error
}

func (f *fakeProjectCatalog) List(context.Context) ([]ProjectRef, error) {
	return append([]ProjectRef(nil), f.refs...), f.err
}

func TestResolvePrefersExplicitBinding(t *testing.T) {
	cat := &fakeProjectCatalog{refs: []ProjectRef{
		{NodeID: "fn", RootID: "docs", Name: "docs · fn"},
		{NodeID: "pc", RootID: "docs", Name: "docs · pc"},
		{NodeID: "home", RootID: "proj", Name: "proj"},
	}}
	got, err := ResolveProject("把 #proj 的部署修一下", "oc_1",
		[]Binding{{ChatID: "oc_1", NodeID: "fn", RootID: "docs"}}, cat, "proj", 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "project" {
		t.Fatalf("kind = %q, want project", got.Kind)
	}
	want := ProjectRef{NodeID: "fn", RootID: "docs", Name: "docs · fn"}
	if got.Project != want {
		t.Fatalf("project = %+v, want %+v", got.Project, want)
	}
	if len(got.Candidates) != 0 {
		t.Fatalf("binding must not offer candidates, got %+v", got.Candidates)
	}
}

func TestResolveParsesNodeAndNamePrefix(t *testing.T) {
	cat := &fakeProjectCatalog{refs: []ProjectRef{
		{NodeID: "fn", RootID: "docs", Name: "docs · fn"},
		{NodeID: "PC-WIN", RootID: "docs", Name: "docs · pc"},
		{NodeID: "home", RootID: "proj", Name: "proj"},
	}}
	cases := []struct {
		name string
		text string
		want ProjectRef
	}{
		{"node and root", "#fn/docs 修一下", ProjectRef{NodeID: "fn", RootID: "docs", Name: "docs · fn"}},
		{"node case insensitive", "#pc/docs 修一下", ProjectRef{NodeID: "PC-WIN", RootID: "docs", Name: "docs · pc"}},
		{"at name", "@proj 修一下", ProjectRef{NodeID: "home", RootID: "proj", Name: "proj"}},
		{"hash name", "#proj 修一下", ProjectRef{NodeID: "home", RootID: "proj", Name: "proj"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveProject(tc.text, "oc_2", nil, cat, "", 5)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != "project" || got.Project != tc.want {
				t.Fatalf("got %+v, want project %+v", got, tc.want)
			}
		})
	}
}

func TestResolveUniqueNameMatches(t *testing.T) {
	cases := []struct {
		name string
		text string
		refs []ProjectRef
		want ProjectRef
	}{
		{"local name", "请修 proj 的脚本", []ProjectRef{{NodeID: "home", RootID: "proj", Name: "proj"}},
			ProjectRef{NodeID: "home", RootID: "proj", Name: "proj"}},
		{"node decorated name", "看看 docs 怎么改", []ProjectRef{{NodeID: "fn", RootID: "docs", Name: "docs · fn"}},
			ProjectRef{NodeID: "fn", RootID: "docs", Name: "docs · fn"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveProject(tc.text, "oc_3", nil, &fakeProjectCatalog{refs: tc.refs}, "", 5)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != "project" || got.Project != tc.want {
				t.Fatalf("got %+v, want project %+v", got, tc.want)
			}
		})
	}
}

func TestResolveReturnsPickerCandidates(t *testing.T) {
	cat := &fakeProjectCatalog{refs: []ProjectRef{
		{NodeID: "pc", RootID: "docs", Name: "docs · pc"},
		{NodeID: "home", RootID: "proj", Name: "proj"},
		{NodeID: "fn", RootID: "docs", Name: "docs · fn"},
	}}
	got, err := ResolveProject("看看 docs 怎么改", "oc_4", nil, cat, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "picker" {
		t.Fatalf("kind = %q, want picker", got.Kind)
	}
	want := []ProjectRef{
		{NodeID: "fn", RootID: "docs", Name: "docs · fn"},
		{NodeID: "pc", RootID: "docs", Name: "docs · pc"},
	}
	if len(got.Candidates) != len(want) {
		t.Fatalf("candidates = %+v, want %+v", got.Candidates, want)
	}
	for i := range want {
		if got.Candidates[i] != want[i] {
			t.Fatalf("candidate %d = %+v, want %+v", i, got.Candidates[i], want[i])
		}
	}
}

func TestResolveNoGuess(t *testing.T) {
	refs := make([]ProjectRef, 0, 7)
	for i := 0; i < 7; i++ {
		name := "p" + string(rune('0'+i))
		refs = append(refs, ProjectRef{NodeID: "fn", RootID: name, Name: name})
	}
	cat := &fakeProjectCatalog{refs: refs}

	none, err := ResolveProject("现在几点", "oc_5", nil, cat, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if none.Kind != "answer" || len(none.Candidates) != 0 {
		t.Fatalf("unrelated text must not pick a project: %+v", none)
	}

	all := "p0 p1 p2 p3 p4 p5 p6"
	overCap, err := ResolveProject(all, "oc_5", nil, cat, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if overCap.Kind != "answer" || len(overCap.Candidates) != 0 {
		t.Fatalf("more candidates than the cap must be a plain answer, not a truncated picker: %+v", overCap)
	}
}

func TestResolvePickerAllowsAtMostFiveCandidates(t *testing.T) {
	refs := make([]ProjectRef, 0, 5)
	for i := 0; i < 5; i++ {
		name := "p" + string(rune('0'+i))
		refs = append(refs, ProjectRef{NodeID: "fn", RootID: name, Name: name})
	}
	got, err := ResolveProject("all p0 p1 p2 p3 p4", "oc_5b", nil, &fakeProjectCatalog{refs: refs}, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "picker" || len(got.Candidates) != 5 {
		t.Fatalf("five candidates within the cap must render a five-button picker: %+v", got)
	}
}

func TestResolveDoesNotGuessWhenCatalogHasOneProject(t *testing.T) {
	cat := &fakeProjectCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "docs", Name: "docs · fn"}}}
	got, err := ResolveProject("现在几点", "oc_6", nil, cat, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "answer" || len(got.Candidates) != 0 {
		t.Fatalf("sole catalog item must not be guessed: %+v", got)
	}
}

func TestResolveDefaultRootOnlyWhenKnown(t *testing.T) {
	cat := &fakeProjectCatalog{refs: []ProjectRef{
		{NodeID: "home", RootID: "proj", Name: "proj"},
		{NodeID: "fn", RootID: "docs", Name: "docs · fn"},
	}}
	cases := []struct {
		name string
		text string
		def  string
		kind string
		want ProjectRef
	}{
		{"known default", "现在几点", "proj", "project", ProjectRef{NodeID: "home", RootID: "proj", Name: "proj"}},
		{"unknown default ignored", "现在几点", "missing", "answer", ProjectRef{}},
		{"prefix beats default", "#fn/docs 修一下", "proj", "project", ProjectRef{NodeID: "fn", RootID: "docs", Name: "docs · fn"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveProject(tc.text, "oc_7", nil, cat, tc.def, 5)
			if err != nil {
				t.Fatal(err)
			}
			if got.Kind != tc.kind {
				t.Fatalf("kind = %q, want %q (%+v)", got.Kind, tc.kind, got)
			}
			if tc.kind == "project" && got.Project != tc.want {
				t.Fatalf("project = %+v, want %+v", got.Project, tc.want)
			}
		})
	}
}

func TestResolveDefaultRootWinsWhenPrefixMisses(t *testing.T) {
	cat := &fakeProjectCatalog{refs: []ProjectRef{
		{NodeID: "home", RootID: "proj", Name: "proj"},
		{NodeID: "fn", RootID: "docs", Name: "docs · fn"},
	}}
	got, err := ResolveProject("#unknown 修一下", "oc_14", nil, cat, "proj", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := ProjectRef{NodeID: "home", RootID: "proj", Name: "proj"}
	if got.Kind != "project" || got.Project != want {
		t.Fatalf("default root must win over answer when the prefix misses: %+v, want %+v", got, want)
	}
}

func TestResolveDefaultRootWinsWhenPrefixIsAmbiguous(t *testing.T) {
	cat := &fakeProjectCatalog{refs: []ProjectRef{
		{NodeID: "home", RootID: "proj", Name: "proj"},
		{NodeID: "fn", RootID: "docs", Name: "docs · fn"},
		{NodeID: "pc", RootID: "docs", Name: "docs · pc"},
	}}
	got, err := ResolveProject("#docs 修一下", "oc_15", nil, cat, "proj", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := ProjectRef{NodeID: "home", RootID: "proj", Name: "proj"}
	if got.Kind != "project" || got.Project != want {
		t.Fatalf("default root must win over picker when the prefix is ambiguous: %+v, want %+v", got, want)
	}
}

func TestResolvePrefixWithNoMatchDoesNotGuess(t *testing.T) {
	cat := &fakeProjectCatalog{refs: []ProjectRef{{NodeID: "fn", RootID: "docs", Name: "docs · fn"}}}
	got, err := ResolveProject("#unknown 修一下", "oc_8", nil, cat, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "answer" || len(got.Candidates) != 0 {
		t.Fatalf("unknown prefix must not fall back to substring matching: %+v", got)
	}
}

func TestResolveReturnsCatalogError(t *testing.T) {
	cat := &fakeProjectCatalog{err: errors.New("catalog offline")}
	if _, err := ResolveProject("现在几点", "oc_9", nil, cat, "", 5); err == nil {
		t.Fatal("catalog failure must be reported instead of guessing")
	}
}

func TestResolveUniqueNameBeatsDefaultRoot(t *testing.T) {
	cat := &fakeProjectCatalog{refs: []ProjectRef{
		{NodeID: "home", RootID: "default", Name: "default"},
		{NodeID: "fn", RootID: "docs", Name: "docs · fn"},
	}}
	got, err := ResolveProject("请修 docs 的脚本", "oc_11", nil, cat, "default", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := ProjectRef{NodeID: "fn", RootID: "docs", Name: "docs · fn"}
	if got.Kind != "project" || got.Project != want {
		t.Fatalf("named project must beat default root: %+v, want %+v", got, want)
	}
}

func TestResolveDefaultRootWinsWhenNoNameMatches(t *testing.T) {
	cat := &fakeProjectCatalog{refs: []ProjectRef{
		{NodeID: "home", RootID: "default", Name: "default"},
		{NodeID: "fn", RootID: "docs", Name: "docs · fn"},
	}}
	got, err := ResolveProject("现在几点", "oc_12", nil, cat, "default", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := ProjectRef{NodeID: "home", RootID: "default", Name: "default"}
	if got.Kind != "project" || got.Project != want {
		t.Fatalf("existing default must win when no name matches: %+v, want %+v", got, want)
	}
}

func TestResolveDefaultRootWinsWhenNameIsAmbiguous(t *testing.T) {
	cat := &fakeProjectCatalog{refs: []ProjectRef{
		{NodeID: "home", RootID: "default", Name: "default"},
		{NodeID: "fn", RootID: "docs", Name: "docs · fn"},
		{NodeID: "pc", RootID: "docs", Name: "docs · pc"},
	}}
	got, err := ResolveProject("看看 docs", "oc_13", nil, cat, "default", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := ProjectRef{NodeID: "home", RootID: "default", Name: "default"}
	if got.Kind != "project" || got.Project != want {
		t.Fatalf("existing default must beat an ambiguous picker: %+v, want %+v", got, want)
	}
}

func TestResolveBindingDoesNotNeedCatalog(t *testing.T) {
	got, err := ResolveProject("现在几点", "oc_10",
		[]Binding{{ChatID: "oc_10", NodeID: "fn", RootID: "docs"}}, nil, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "project" || got.Project.NodeID != "fn" || got.Project.RootID != "docs" || got.Project.Name != "docs" {
		t.Fatalf("explicit binding must survive an unavailable catalog: %+v", got)
	}
	if strings.TrimSpace(got.Project.RootID) == "" {
		t.Fatal("empty root in resolution")
	}
}
