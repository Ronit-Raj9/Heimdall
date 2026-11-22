package taskchecks

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Path is a parsed JSON path. Two spellings are read:
//
//   - JSON path, from $: .key, ['key'], [n] (negative counts from the end),
//     [*] and .*, and a filter [?(@.key == value)] or [?(@.key != value)]
//     whose value is a quoted string, a number, true, false or null. A
//     filter field may be a dotted path (@.customer.id).
//   - The slash form of the support template's checks.py, for a path that
//     does not start with $: keys and list indexes split by "/", and
//     key=value picks the first list element whose key equals value.
//
// A path is definite when it has no wildcard or filter: it names at most
// one value.
type Path struct {
	steps    []step
	definite bool
	text     string
}

type stepKind int

const (
	stepKey stepKind = iota
	stepIndex
	stepWild
	stepFilter
	stepFirstWhere // slash form key=value: the first match only
)

type step struct {
	kind  stepKind
	key   string
	index int
	field []string // filter: the field path under @
	op    string   // == or !=
	value any
}

// ParsePath parses a JSON path or a slash path.
func ParsePath(s string) (*Path, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("path is empty")
	}
	if !strings.HasPrefix(s, "$") {
		return parseSlash(s)
	}
	p := &Path{definite: true, text: s}
	i := 1
	for i < len(s) {
		switch s[i] {
		case '.':
			i++
			if i < len(s) && s[i] == '.' {
				return nil, fmt.Errorf("path %q: recursive descent (..) is not supported; name each step", s)
			}
			if i < len(s) && s[i] == '*' {
				p.steps = append(p.steps, step{kind: stepWild})
				p.definite = false
				i++
				continue
			}
			j := i
			for j < len(s) && s[j] != '.' && s[j] != '[' {
				j++
			}
			if j == i {
				return nil, fmt.Errorf("path %q: empty key at %d", s, i)
			}
			p.steps = append(p.steps, step{kind: stepKey, key: s[i:j]})
			i = j
		case '[':
			end, st, err := bracket(s, i)
			if err != nil {
				return nil, err
			}
			if st.kind == stepWild || st.kind == stepFilter {
				p.definite = false
			}
			p.steps = append(p.steps, st)
			i = end
		default:
			return nil, fmt.Errorf("path %q: unexpected %q at %d (want . or [)", s, s[i], i)
		}
	}
	return p, nil
}

// bracket parses the [...] step starting at s[i] and returns the index
// after it.
func bracket(s string, i int) (int, step, error) {
	rest := s[i+1:]
	switch {
	case strings.HasPrefix(rest, "*]"):
		return i + 3, step{kind: stepWild}, nil
	case strings.HasPrefix(rest, "'") || strings.HasPrefix(rest, `"`):
		q := rest[0]
		j := strings.IndexByte(rest[1:], q)
		if j < 0 || len(rest) < j+3 || rest[j+2] != ']' {
			return 0, step{}, fmt.Errorf("path %q: unclosed quoted key at %d", s, i)
		}
		return i + j + 4, step{kind: stepKey, key: rest[1 : j+1]}, nil
	case strings.HasPrefix(rest, "?("):
		end := strings.Index(rest, ")]")
		if end < 0 {
			return 0, step{}, fmt.Errorf("path %q: unclosed filter at %d (want [?(@.key == value)])", s, i)
		}
		st, err := filter(s, rest[2:end])
		if err != nil {
			return 0, step{}, err
		}
		return i + 1 + end + 2, st, nil
	}
	j := strings.IndexByte(rest, ']')
	if j < 0 {
		return 0, step{}, fmt.Errorf("path %q: unclosed [ at %d", s, i)
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest[:j]))
	if err != nil {
		return 0, step{}, fmt.Errorf("path %q: [%s] is not an index, *, a quoted key or a filter", s, rest[:j])
	}
	return i + j + 2, step{kind: stepIndex, index: n}, nil
}

// filter parses "@.key == value".
func filter(path, expr string) (step, error) {
	op := "=="
	k := strings.Index(expr, "==")
	if n := strings.Index(expr, "!="); n >= 0 && (k < 0 || n < k) {
		op, k = "!=", n
	}
	if k < 0 {
		return step{}, fmt.Errorf("path %q: filter %q: want @.key == value or @.key != value", path, expr)
	}
	lhs, rhs := strings.TrimSpace(expr[:k]), strings.TrimSpace(expr[k+2:])
	if !strings.HasPrefix(lhs, "@.") || len(lhs) < 3 {
		return step{}, fmt.Errorf("path %q: filter %q: the left side must be @.key", path, expr)
	}
	var val any
	switch {
	case len(rhs) >= 2 && (rhs[0] == '\'' && rhs[len(rhs)-1] == '\'' || rhs[0] == '"' && rhs[len(rhs)-1] == '"'):
		val = rhs[1 : len(rhs)-1]
	default:
		if err := json.Unmarshal([]byte(rhs), &val); err != nil {
			return step{}, fmt.Errorf("path %q: filter value %q: want a quoted string, a number, true, false or null", path, rhs)
		}
	}
	return step{kind: stepFilter, field: strings.Split(lhs[2:], "."), op: op, value: val}, nil
}

// parseSlash parses the slash form.
func parseSlash(s string) (*Path, error) {
	p := &Path{definite: true, text: s}
	for _, seg := range strings.Split(s, "/") {
		if seg == "" {
			continue
		}
		switch {
		case strings.Contains(seg, "="):
			k, v, _ := strings.Cut(seg, "=")
			if k == "" {
				return nil, fmt.Errorf("path %q: %q needs a key before =", s, seg)
			}
			p.steps = append(p.steps, step{kind: stepFirstWhere, field: []string{k}, op: "==", value: v})
		case isInt(seg):
			n, _ := strconv.Atoi(seg)
			p.steps = append(p.steps, step{kind: stepIndex, index: n, key: seg})
		default:
			p.steps = append(p.steps, step{kind: stepKey, key: seg})
		}
	}
	return p, nil
}

func isInt(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

// String is the path as written.
func (p *Path) String() string { return p.text }

// Definite reports whether the path names at most one value.
func (p *Path) Definite() bool { return p.definite }

// Eval returns every value the path reaches in doc.
func (p *Path) Eval(doc any) []any {
	cur := []any{doc}
	for _, st := range p.steps {
		var next []any
		for _, v := range cur {
			next = append(next, apply(st, v)...)
		}
		cur = next
		if len(cur) == 0 {
			return nil
		}
	}
	return cur
}

func apply(st step, v any) []any {
	switch st.kind {
	case stepKey:
		if m, ok := v.(map[string]any); ok {
			if x, ok := m[st.key]; ok {
				return []any{x}
			}
		}
	case stepIndex:
		if l, ok := v.([]any); ok {
			i := st.index
			if i < 0 {
				i += len(l)
			}
			if i >= 0 && i < len(l) {
				return []any{l[i]}
			}
			return nil
		}
		if m, ok := v.(map[string]any); ok && st.key != "" { // slash form: a numeric key of an object
			if x, ok := m[st.key]; ok {
				return []any{x}
			}
		}
	case stepWild:
		switch t := v.(type) {
		case []any:
			return append([]any(nil), t...)
		case map[string]any:
			out := make([]any, 0, len(t))
			for _, k := range sortedKeys(t) {
				out = append(out, t[k])
			}
			return out
		}
	case stepFilter, stepFirstWhere:
		l, ok := v.([]any)
		if !ok {
			return nil
		}
		var out []any
		for _, el := range l {
			got, found := fieldOf(el, st.field)
			hit := found && looseEqual(got, st.value, st.kind == stepFirstWhere)
			if st.op == "!=" {
				hit = !hit
			}
			if hit {
				out = append(out, el)
				if st.kind == stepFirstWhere {
					break
				}
			}
		}
		return out
	}
	return nil
}

func fieldOf(v any, field []string) (any, bool) {
	for _, f := range field {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[f]; !ok {
			return nil, false
		}
	}
	return v, true
}

// looseEqual is filter equality: JSON equality, and in the slash form the
// text of the value (as checks.py compares str(value)).
func looseEqual(got, want any, asText bool) bool {
	if asText {
		return textOf(got) == textOf(want)
	}
	return Equal(got, want)
}
