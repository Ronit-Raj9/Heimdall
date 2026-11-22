// Package remotemap maps a benchmark task onto a deployed agent's own HTTP
// interface and reads its answer back (lane Y10, gap G1 P0-3): request
// templates, answer paths, streamed answers, auth (OAuth 2 client
// credentials, Microsoft Entra ID, AWS SigV4, Google ID tokens, mutual TLS)
// and the platform presets of the connect form. The run-time launcher
// (adapters/remotehttp.py) implements the same rules in Python; the tests
// run both against the same endpoints.
package remotemap

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// A path picks values out of a JSON document. The syntax is a small
// JSONPath subset:
//
//	$.output[0].content     a leading "$" or "$." is optional
//	messages[-1].content    negative indexes count from the end
//	choices[*].text         [*] every element (or every value of an object)
//	['odd key']             a quoted key
//	output[?(@.type=='message')]   keep elements whose field equals a value
//	activities[?(@.from.id!='me')] or does not equal it
//	items[?(@.text)]        or has the field at all
//
// A filter on an object keeps the object itself when it matches, so a
// path can test a single streamed event: [?(@.type=='text-delta')].delta.

type segKind int

const (
	segKey segKind = iota
	segIndex
	segWild
	segFilter
)

type segment struct {
	kind  segKind
	key   string
	index int
	// filter: field path, operator ("==", "!=" or "" for exists), value
	field []string
	op    string
	value any
}

// Path is a parsed path.
type Path struct {
	src  string
	segs []segment
}

// String returns the path as written.
func (p *Path) String() string { return p.src }

// ParsePath parses a path. An empty path is an error.
func ParsePath(src string) (*Path, error) {
	s := strings.TrimSpace(src)
	if s == "" {
		return nil, fmt.Errorf("empty path")
	}
	if len(s) > 512 {
		return nil, fmt.Errorf("path %q is longer than 512 characters", truncate(s, 40))
	}
	s = strings.TrimPrefix(s, "$")
	p := &Path{src: src}
	i := 0
	for i < len(s) {
		switch c := s[i]; {
		case c == '.':
			i++
			j := i
			for j < len(s) && s[j] != '.' && s[j] != '[' {
				j++
			}
			if j == i {
				return nil, fmt.Errorf("path %q: empty key at %d", src, i)
			}
			if s[i:j] == "*" {
				p.segs = append(p.segs, segment{kind: segWild})
			} else {
				p.segs = append(p.segs, segment{kind: segKey, key: s[i:j]})
			}
			i = j
		case c == '[':
			end, seg, err := parseBracket(s, i)
			if err != nil {
				return nil, fmt.Errorf("path %q: %v", src, err)
			}
			p.segs = append(p.segs, seg)
			i = end
		default:
			if i != 0 {
				return nil, fmt.Errorf("path %q: unexpected %q at %d", src, c, i)
			}
			j := i
			for j < len(s) && s[j] != '.' && s[j] != '[' {
				j++
			}
			if s[i:j] == "*" {
				p.segs = append(p.segs, segment{kind: segWild})
			} else {
				p.segs = append(p.segs, segment{kind: segKey, key: s[i:j]})
			}
			i = j
		}
	}
	return p, nil
}

// parseBracket parses the bracket at s[i] and returns the index after it.
func parseBracket(s string, i int) (int, segment, error) {
	rest := s[i+1:]
	switch {
	case strings.HasPrefix(rest, "*]"):
		return i + 3, segment{kind: segWild}, nil
	case strings.HasPrefix(rest, "'") || strings.HasPrefix(rest, `"`):
		q := rest[0]
		j := strings.IndexByte(rest[1:], q)
		if j < 0 || len(rest) < j+3 || rest[j+2] != ']' {
			return 0, segment{}, fmt.Errorf("unterminated quoted key at %d", i)
		}
		return i + j + 4, segment{kind: segKey, key: rest[1 : j+1]}, nil
	case strings.HasPrefix(rest, "?("):
		j := strings.Index(rest, ")]")
		if j < 0 {
			return 0, segment{}, fmt.Errorf("unterminated filter at %d", i)
		}
		seg, err := parseFilter(rest[2:j])
		if err != nil {
			return 0, segment{}, err
		}
		return i + j + 3, seg, nil
	default:
		j := strings.IndexByte(rest, ']')
		if j < 0 {
			return 0, segment{}, fmt.Errorf("unterminated [ at %d", i)
		}
		n, err := strconv.Atoi(strings.TrimSpace(rest[:j]))
		if err != nil {
			return 0, segment{}, fmt.Errorf("index %q is not a number", rest[:j])
		}
		return i + j + 2, segment{kind: segIndex, index: n}, nil
	}
}

// parseFilter parses "@.a.b=='v'", "@.a!=3" or "@.a".
func parseFilter(expr string) (segment, error) {
	expr = strings.TrimSpace(expr)
	seg := segment{kind: segFilter}
	left, right := expr, ""
	for _, op := range []string{"==", "!="} {
		if k := strings.Index(expr, op); k >= 0 {
			left, right, seg.op = strings.TrimSpace(expr[:k]), strings.TrimSpace(expr[k+2:]), op
			break
		}
	}
	if !strings.HasPrefix(left, "@.") || len(left) < 3 {
		return seg, fmt.Errorf("filter %q: want @.field, @.field=='value' or @.field!='value'", expr)
	}
	seg.field = strings.Split(left[2:], ".")
	for _, f := range seg.field {
		if f == "" {
			return seg, fmt.Errorf("filter %q: empty field", expr)
		}
	}
	if seg.op == "" {
		return seg, nil
	}
	switch {
	case len(right) >= 2 && (right[0] == '\'' || right[0] == '"') && right[len(right)-1] == right[0]:
		seg.value = right[1 : len(right)-1]
	case right == "true":
		seg.value = true
	case right == "false":
		seg.value = false
	case right == "null":
		seg.value = nil
	default:
		f, err := strconv.ParseFloat(right, 64)
		if err != nil {
			return seg, fmt.Errorf("filter %q: value %q is not a quoted string, number, true, false or null", expr, right)
		}
		seg.value = f
	}
	return seg, nil
}

// Eval returns every value the path matches in doc (decoded JSON), in
// document order. A path that matches nothing returns none.
func (p *Path) Eval(doc any) []any {
	cur := []any{doc}
	for _, sg := range p.segs {
		var next []any
		for _, v := range cur {
			switch sg.kind {
			case segKey:
				if m, ok := v.(map[string]any); ok {
					if x, ok := m[sg.key]; ok {
						next = append(next, x)
					}
				}
			case segIndex:
				if a, ok := v.([]any); ok {
					i := sg.index
					if i < 0 {
						i += len(a)
					}
					if i >= 0 && i < len(a) {
						next = append(next, a[i])
					}
				}
			case segWild:
				switch t := v.(type) {
				case []any:
					next = append(next, t...)
				case map[string]any:
					for _, k := range sortedKeys(t) {
						next = append(next, t[k])
					}
				}
			case segFilter:
				switch t := v.(type) {
				case []any:
					for _, e := range t {
						if sg.match(e) {
							next = append(next, e)
						}
					}
				case map[string]any:
					if sg.match(t) {
						next = append(next, t)
					}
				}
			}
		}
		cur = next
	}
	return cur
}

func (sg segment) match(v any) bool {
	for _, f := range sg.field {
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		if v, ok = m[f]; !ok {
			return false
		}
	}
	switch sg.op {
	case "":
		return v != nil
	case "==":
		return equalJSON(v, sg.value)
	default:
		return !equalJSON(v, sg.value)
	}
}

func equalJSON(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		f, err := x.Float64()
		bf, ok := b.(float64)
		return err == nil && ok && f == bf
	case float64:
		bf, ok := b.(float64)
		return ok && x == bf
	}
	return a == b
}

// Text joins matches into one answer: strings as they are, other values as
// JSON, nulls skipped, separated by sep.
func Text(matches []any, sep string) string {
	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		switch v := m.(type) {
		case nil:
		case string:
			parts = append(parts, v)
		default:
			b, err := json.Marshal(v)
			if err == nil {
				parts = append(parts, string(b))
			}
		}
	}
	return strings.Join(parts, sep)
}

// Decode decodes JSON for Eval, keeping numbers as float64.
func Decode(raw []byte) (any, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
