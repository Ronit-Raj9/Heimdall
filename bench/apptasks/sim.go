package apptasks

// An in-process copy of the mock service's state rules (scaffold/mockapi/
// server.py, Mock.apply and Mock.call_tool), so a drafted task's reference
// plan can be run against the seed before anything is written: every step
// must succeed, the end state gives the state checks, and the controls are
// graded on the same simulated ledger and state. Readiness then proves the
// same thing against the real service image.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/bench/scaffold"
)

// State is the mock's database: collections of JSON objects.
type State map[string][]any

// Clone deep-copies a state through JSON.
func (s State) Clone() State {
	b, _ := json.Marshal(s)
	var out State
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = State{}
	}
	return out
}

// Sim runs tool calls on a copy of a seed with the mock's rules.
type Sim struct {
	ops   map[string]scaffold.MockOperation
	State State
}

// NewSim starts a simulation of mock over seed (copied). Every collection
// the mock keeps exists, and _actions records action operations, as in
// server.py.
func NewSim(m *scaffold.MockSpec, seed State) *Sim {
	s := &Sim{ops: map[string]scaffold.MockOperation{}, State: seed.Clone()}
	for _, op := range m.Operations {
		s.ops[op.Name] = op
		if op.Collection != "" {
			if _, ok := s.State[op.Collection]; !ok {
				s.State[op.Collection] = []any{}
			}
		}
	}
	if _, ok := s.State["_actions"]; !ok {
		s.State["_actions"] = []any{}
	}
	return s
}

// Call runs one MCP tool call the way call_tool does and returns the HTTP
// status and the response.
func (s *Sim) Call(tool string, args map[string]any) (int, any, error) {
	op, ok := s.ops[tool]
	if !ok {
		return 0, nil, fmt.Errorf("the API has no operation %s", tool)
	}
	if args == nil {
		args = map[string]any{}
	}
	params := map[string]string{}
	for _, p := range op.PathParams {
		params[p] = text(args[p])
	}
	query := map[string]string{}
	for _, p := range op.QueryParams {
		if v, ok := args[p]; ok {
			query[p] = text(v)
		}
	}
	body, hasBody := args["body"]
	if !hasBody && op.Body {
		known := map[string]bool{}
		for _, p := range op.PathParams {
			known[p] = true
		}
		for _, p := range op.QueryParams {
			known[p] = true
		}
		flat := map[string]any{}
		for k, v := range args {
			if !known[k] {
				flat[k] = v
			}
		}
		body = flat
	}
	// call_tool quotes then unquotes path values; a value with a "/" still
	// names one item.
	for k, v := range params {
		if u, err := url.PathUnescape(url.PathEscape(v)); err == nil {
			params[k] = u
		}
	}
	st, resp := s.apply(op, params, query, body)
	return st, resp, nil
}

func text(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprint(int64(x))
		}
		return fmt.Sprint(x)
	case bool:
		if x {
			return "True"
		}
		return "False"
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (s *Sim) find(coll, id string) (int, map[string]any) {
	for i, it := range s.State[coll] {
		if m, ok := it.(map[string]any); ok && text(m["id"]) == id {
			return i, m
		}
	}
	return -1, nil
}

func (s *Sim) apply(op scaffold.MockOperation, params, query map[string]string, body any) (int, any) {
	coll := op.Collection
	pfield, pval, hasParent := "", "", false
	if op.Parent != nil {
		pfield = op.Parent.Field
		pval, hasParent = params[op.Parent.Param]
	}
	switch op.Kind {
	case scaffold.MockList:
		var items []any
		for _, it := range s.State[coll] {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			if pfield != "" && hasParent && text(m[pfield]) != pval {
				continue
			}
			keep := true
			for k, v := range query {
				if got, ok := m[k]; ok && text(got) != v {
					keep = false
				}
			}
			if keep {
				items = append(items, m)
			}
		}
		if items == nil {
			items = []any{}
		}
		return 200, items
	case scaffold.MockCreate:
		b, ok := body.(map[string]any)
		if !ok {
			return 400, map[string]any{"error": "the body must be a JSON object"}
		}
		item := map[string]any{}
		for k, v := range b {
			item[k] = v
		}
		if pfield != "" && hasParent {
			item[pfield] = pval
		}
		if _, ok := item["id"]; !ok {
			prefix := strings.ToUpper(coll)
			if len(prefix) > 3 {
				prefix = prefix[:3]
			}
			if prefix == "" {
				prefix = "ID"
			}
			item["id"] = fmt.Sprintf("%s-%d", prefix, len(s.State[coll])+1001)
		}
		if _, it := s.find(coll, text(item["id"])); it != nil {
			return 409, map[string]any{"error": fmt.Sprintf("%s %s already exists", coll, text(item["id"]))}
		}
		s.State[coll] = append(s.State[coll], item)
		return 201, item
	case scaffold.MockGet, scaffold.MockUpdate, scaffold.MockDelete:
		id := params[op.IDParam]
		i, it := s.find(coll, id)
		if it == nil || (pfield != "" && hasParent && text(it[pfield]) != pval) {
			return 404, map[string]any{"error": fmt.Sprintf("%s %s not found", coll, id)}
		}
		switch op.Kind {
		case scaffold.MockGet:
			return 200, it
		case scaffold.MockDelete:
			s.State[coll] = append(s.State[coll][:i:i], s.State[coll][i+1:]...)
			return 200, map[string]any{"deleted": it["id"]}
		}
		b, ok := body.(map[string]any)
		if !ok {
			return 400, map[string]any{"error": "the body must be a JSON object"}
		}
		for k, v := range b {
			if k != "id" {
				it[k] = v
			}
		}
		return 200, it
	}
	p := map[string]any{}
	for k, v := range params {
		p[k] = v
	}
	q := map[string]any{}
	for k, v := range query {
		q[k] = v
	}
	s.State["_actions"] = append(s.State["_actions"], map[string]any{"operation": op.Name, "params": p, "query": q, "body": body})
	return 200, map[string]any{"ok": true, "operation": op.Name}
}
