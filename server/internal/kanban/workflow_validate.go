package kanban

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type json_Number = json.Number

// EvalSafeExpression evaluates a restricted declarative expression against
// persisted node-output JSON. Grammar:  <path> <op> <literal>  where path is
// dot-separated identifiers (e.g. result.score), op in ==,!=,>=,<=,>,< and
// literal is number, "string", true/false. Anything else (function calls,
// ;, shell/network tokens, brackets) is rejected. No eval, no script, no IO.
func EvalSafeExpression(expr string, outputs map[string]any) (bool, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false, errors.New("empty expression")
	}
	for _, bad := range []string{"(", ")", ";", "`", "$", "exec", "eval", "fetch", "system", "http", "import", "require", "[", "]", "{", "}"} {
		if strings.Contains(strings.ToLower(expr), bad) {
			return false, fmt.Errorf("forbidden token %q in expression", bad)
		}
	}
	// split on first operator
	ops := []string{"==", "!=", ">=", "<=", ">", "<"}
	var lhs, op, rhs string
	for _, o := range ops {
		if i := strings.Index(expr, o); i >= 0 {
			lhs, op, rhs = expr[:i], o, expr[i+len(o):]
			break
		}
	}
	if op == "" {
		return false, errors.New("expression must be '<path> <op> <literal>'")
	}
	lhs, rhs = strings.TrimSpace(lhs), strings.TrimSpace(rhs)
	if !isSafePath(lhs) {
		return false, fmt.Errorf("invalid field path %q", lhs)
	}
	val, ok := lookupPath(outputs, lhs)
	if !ok {
		return false, fmt.Errorf("field %q not found in node outputs", lhs)
	}
	lit, err := parseLiteral(rhs)
	if err != nil {
		return false, err
	}
	return compareValues(val, op, lit), nil
}

func isSafePath(p string) bool {
	if p == "" {
		return false
	}
	for _, seg := range strings.Split(p, ".") {
		if seg == "" {
			return false
		}
		for _, r := range seg {
			if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
				return false
			}
		}
	}
	return true
}

func lookupPath(m map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = m
	for i, p := range parts {
		mp, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = mp[p]
		if !ok && i == len(parts)-1 {
			return nil, false
		}
	}
	return cur, true
}

func parseLiteral(s string) (any, error) {
	if s == "true" {
		return true, nil
	}
	if s == "false" {
		return false, nil
	}
	if len(s) >= 2 && strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
		return strings.Trim(s, "\""), nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, nil
	}
	return nil, fmt.Errorf("invalid literal %q", s)
}

func compareValues(val any, op string, lit any) bool {
	switch l := lit.(type) {
	case float64:
		v, ok := toFloat(val)
		if !ok {
			return false
		}
		switch op {
		case "==":
			return math.Abs(v-l) < 1e-9
		case "!=":
			return math.Abs(v-l) >= 1e-9
		case ">":
			return v > l
		case "<":
			return v < l
		case ">=":
			return v >= l
		case "<=":
			return v <= l
		}
	case string:
		s, _ := val.(string)
		switch op {
		case "==":
			return s == l
		case "!=":
			return s != l
		}
	case bool:
		b, _ := val.(bool)
		switch op {
		case "==":
			return b == l
		case "!=":
			return b != l
		}
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case json_Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func validateLoopNode(n WorkflowNode) error {
	raw, ok := n.Params["max_iterations"]
	if !ok {
		return fmt.Errorf("loop node %q: max_iterations required (1..20, default 5)", n.ID)
	}
	m, ok := toInt(raw)
	if !ok || m < LoopMinMax || m > LoopMaxMax {
		return fmt.Errorf("loop node %q: max_iterations must be %d..%d", n.ID, LoopMinMax, LoopMaxMax)
	}
	if g, ok := n.Params["graph"]; ok {
		b, _ := json.Marshal(g)
		var inner WorkflowDefinition
		if err := json.Unmarshal(b, &inner); err == nil {
			for _, in := range inner.Nodes {
				if in.Kind == NodeKindLoop {
					return fmt.Errorf("loop node %q: nested loops not allowed", n.ID)
				}
			}
		}
	}
	return nil
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	}
	return 0, false
}

// ValidateWorkflowDefinition checks: non-empty nodes, unique ids, valid kinds,
// edges reference existing nodes, DAG (no cycles), condition routes declared,
// loop bounds 1..20 explicit, no nested loops.
func ValidateWorkflowDefinition(d WorkflowDefinition) error {
	if len(d.Nodes) == 0 {
		return errors.New("workflow graph must have at least one node")
	}
	ids := map[string]bool{}
	for _, n := range d.Nodes {
		if n.ID == "" {
			return errors.New("node id required")
		}
		if ids[n.ID] {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		ids[n.ID] = true
		switch n.Kind {
		case NodeKindAgent, NodeKindCondition, NodeKindHumanGate, NodeKindLoop, NodeKindNotify:
		default:
			return fmt.Errorf("node %q: unknown kind %q", n.ID, n.Kind)
		}
		if n.Kind == NodeKindLoop {
			if err := validateLoopNode(n); err != nil {
				return err
			}
		}
		if n.Kind == NodeKindCondition {
			if expr, _ := n.Params["expression"].(string); expr == "" {
				return fmt.Errorf("condition node %q: expression required", n.ID)
			}
			if routes, ok := n.Params["routes"].([]any); !ok || len(routes) == 0 {
				return fmt.Errorf("condition node %q: routes required", n.ID)
			}
		}
	}
	adj := map[string][]string{}
	for _, e := range d.Edges {
		if !ids[e.From] || !ids[e.To] {
			return fmt.Errorf("edge %q->%q references unknown node", e.From, e.To)
		}
		adj[e.From] = append(adj[e.From], e.To)
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		color[id] = gray
		for _, nb := range adj[id] {
			switch color[nb] {
			case gray:
				return fmt.Errorf("cycle detected at %q (arbitrary back-edge not allowed)", nb)
			case white:
				if err := visit(nb); err != nil {
					return err
				}
			}
		}
		color[id] = black
		return nil
	}
	for id := range ids {
		if color[id] == white {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	for _, e := range d.Edges {
		from, _ := d.node(e.From)
		if from.Kind != NodeKindCondition {
			continue
		}
		if e.Route == "" {
			return fmt.Errorf("edge from condition %q must declare route", e.From)
		}
		routes, _ := from.Params["routes"].([]any)
		found := false
		for _, r := range routes {
			if s, ok := r.(string); ok && s == e.Route {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("edge from condition %q: route %q not declared", e.From, e.Route)
		}
	}
	return nil
}
