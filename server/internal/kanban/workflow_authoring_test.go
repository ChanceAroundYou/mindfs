package kanban

import (
	"strings"
	"testing"
)

func TestParseWorkflowAuthoringProposal(t *testing.T) {
	raw := "```json\n" + `{
		"summary": "Add a notify node",
		"rationale": "The goal asks for a completion message",
		"base_revision": "r1",
		"operations": [
			{"id": "op-1", "op": "add_node", "node": {"id": "b", "kind": "notify", "params": {"message": "done"}}}
		]
	}` + "\n```"
	p, issues := ParseWorkflowAuthoringProposal(raw, "r1")
	if len(issues) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
	if p == nil || p.Summary != "Add a notify node" || p.BaseRevision != "r1" || len(p.Operations) != 1 {
		t.Fatalf("proposal = %+v", p)
	}
	if p.Operations[0].Node == nil || p.Operations[0].Node.ID != "b" {
		t.Fatalf("operation = %+v", p.Operations[0])
	}
}

func TestParseWorkflowAuthoringProposalRejectsInvalidOutput(t *testing.T) {
	op := `{"id":"op-1","op":"add_node","node":{"id":"b","kind":"notify"}}`
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"not json", "I think we should add a node.", "JSON"},
		{"missing summary", `{"base_revision":"r1","operations":[` + op + `]}`, "summary"},
		{"missing base", `{"summary":"x","operations":[` + op + `]}`, "base_revision"},
		{"base mismatch", `{"summary":"x","base_revision":"r0","operations":[` + op + `]}`, "base_revision"},
		{"empty operations", `{"summary":"x","base_revision":"r1","operations":[]}`, "operations"},
		{"duplicate ids", `{"summary":"x","base_revision":"r1","operations":[` + op + `,` + op + `]}`, "duplicate"},
		{"unknown op", `{"summary":"x","base_revision":"r1","operations":[{"id":"op-1","op":"replace_graph"}]}`, "unknown operation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, issues := ParseWorkflowAuthoringProposal(tc.raw, "r1")
			if p != nil {
				t.Fatalf("proposal = %+v, want nil on invalid output", p)
			}
			if len(issues) == 0 {
				t.Fatal("expected issues")
			}
			if !strings.Contains(strings.Join(issueMessages(issues), " "), tc.want) {
				t.Fatalf("issues = %+v, want mention of %q", issues, tc.want)
			}
		})
	}
}

func TestParseWorkflowAuthoringProposalRejectsOversizedOperations(t *testing.T) {
	ops := make([]string, WorkflowPatchMaxOperations+1)
	for i := range ops {
		ops[i] = `{"id":"op-` + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + `","op":"add_node","node":{"id":"n` + string(rune('a'+i%26)) + `","kind":"notify"}}`
	}
	raw := `{"summary":"x","base_revision":"r1","operations":[` + strings.Join(ops, ",") + `]}`
	_, issues := ParseWorkflowAuthoringProposal(raw, "r1")
	if len(issues) == 0 || !strings.Contains(strings.Join(issueMessages(issues), " "), "200") {
		t.Fatalf("issues = %+v, want operation limit", issues)
	}
}

func issueMessages(issues []WorkflowPatchIssue) []string {
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issue.Message)
	}
	return out
}
