package connect

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Answer paths in the lane Y10 dialect (schemas.RemoteMapping.AnswerPath):
// dotted keys, [n] and [-n] indexes, [*] for every element, ['key'] and a
// [?(@.k=='v')] filter. This is the subset Detect writes and Verify reads;
// the launcher (adapters/remotehttp.py, remotemap) reads the full dialect.

type segment struct {
	key    string
	index  *int
	all    bool
	fkey   string
	fvalue string
	filter bool
}

func parsePath(src string) ([]segment, error) {
	var out []segment
	s := strings.TrimPrefix(strings.TrimSpace(src), "$")
	s = strings.TrimPrefix(s, ".")
	for len(s) > 0 {
		switch {
		case s[0] == '.':
			s = s[1:]
		case s[0] == '[':
			end := strings.Index(s, "]")
			if strings.HasPrefix(s, "[?(") {
				end = strings.Index(s, ")]")
				if end < 0 {
					return nil, fmt.Errorf("path %q: unclosed filter", src)
				}
				expr := s[3:end]
				s = s[end+2:]
				k, v, ok := strings.Cut(expr, "==")
				if !ok {
					return nil, fmt.Errorf("path %q: a filter must be @.key=='value'", src)
				}
				k = strings.TrimPrefix(strings.TrimSpace(k), "@.")
				v = strings.Trim(strings.TrimSpace(v), `'"`)
				out = append(out, segment{filter: true, fkey: k, fvalue: v})
				continue
			}
			if end < 0 {
				return nil, fmt.Errorf("path %q: unclosed [", src)
			}
			inner := strings.TrimSpace(s[1:end])
			s = s[end+1:]
			switch {
			case inner == "*":
				out = append(out, segment{all: true})
			case strings.HasPrefix(inner, "'") || strings.HasPrefix(inner, `"`):
				out = append(out, segment{key: strings.Trim(inner, `'"`)})
			default:
				n, err := strconv.Atoi(inner)
				if err != nil {
					return nil, fmt.Errorf("path %q: [%s] is not an index", src, inner)
				}
				out = append(out, segment{index: &n})
			}
		default:
			end := strings.IndexAny(s, ".[")
			if end < 0 {
				end = len(s)
			}
			out = append(out, segment{key: s[:end]})
			s = s[end:]
		}
	}
	return out, nil
}

// EvalPath returns every value path picks out of doc.
func EvalPath(doc any, path string) ([]any, error) {
	segs, err := parsePath(path)
	if err != nil {
		return nil, err
	}
	cur := []any{doc}
	for _, sg := range segs {
		var next []any
		for _, v := range cur {
			switch {
			case sg.key != "":
				if m, ok := v.(map[string]any); ok {
					if c, ok := m[sg.key]; ok {
						next = append(next, c)
					}
				}
			case sg.index != nil:
				if a, ok := v.([]any); ok {
					i := *sg.index
					if i < 0 {
						i += len(a)
					}
					if i >= 0 && i < len(a) {
						next = append(next, a[i])
					}
				}
			case sg.all:
				switch t := v.(type) {
				case []any:
					next = append(next, t...)
				case map[string]any:
					for _, k := range sortedKeys(t) {
						next = append(next, t[k])
					}
				}
			case sg.filter:
				if a, ok := v.([]any); ok {
					for _, e := range a {
						if m, ok := e.(map[string]any); ok && fmt.Sprint(m[sg.fkey]) == sg.fvalue {
							next = append(next, e)
						}
					}
				}
			}
		}
		cur = next
	}
	return cur, nil
}

// PathText is the text of the values path picks out of doc, joined with a
// newline (the launcher's rule). ok is false when nothing matched.
func PathText(doc any, path string) (string, bool) {
	vals, err := EvalPath(doc, path)
	if err != nil || len(vals) == 0 {
		return "", false
	}
	var parts []string
	for _, v := range vals {
		parts = append(parts, textOf(v))
	}
	return strings.Join(parts, "\n"), true
}

func textOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var identRe = func(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if !(r == '_' || r == '-' && i > 0 || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' && i > 0) {
			return false
		}
	}
	return true
}

type found struct {
	path  string
	value string
}

// stringLeaves lists every string in doc with its path, breadth first, so
// shorter paths come first. A list index is written as [-1] when it is the
// last element of a list of two or more (a message list grows; the answer
// is the last one), else as [n].
func stringLeaves(doc any) []found {
	type item struct {
		path string
		v    any
	}
	var out []found
	queue := []item{{"", doc}}
	for len(queue) > 0 && len(out) < 5000 {
		it := queue[0]
		queue = queue[1:]
		switch t := it.v.(type) {
		case string:
			out = append(out, found{it.path, t})
		case map[string]any:
			for _, k := range sortedKeys(t) {
				p := k
				if !identRe(k) {
					p = "['" + k + "']"
					if it.path != "" {
						p = it.path + p
					}
				} else if it.path != "" {
					p = it.path + "." + k
				}
				queue = append(queue, item{p, t[k]})
			}
		case []any:
			for i, e := range t {
				idx := "[" + strconv.Itoa(i) + "]"
				if i == len(t)-1 && len(t) > 1 {
					idx = "[-1]"
				}
				queue = append(queue, item{it.path + idx, e})
			}
		}
	}
	return out
}

// LocateNonce finds the shortest path to a string holding nonce in doc.
// Strings that also hold the whole prompt are echoes of the request, not
// answers: echo reports that only echoes were found. fields lists the
// string fields seen, for the pick-a-field fallback.
func LocateNonce(doc any, nonce, prompt string) (path string, echo bool, fields []string) {
	for _, f := range stringLeaves(doc) {
		if len(fields) < 20 && f.path != "" {
			fields = append(fields, f.path)
		}
		if !strings.Contains(f.value, nonce) {
			continue
		}
		if prompt != "" && strings.Contains(f.value, prompt) {
			echo = true
			continue
		}
		if path == "" {
			path = f.path
		}
	}
	if path != "" {
		echo = false
	}
	return path, echo, fields
}

// streamPathFor finds the path whose values, concatenated across the
// events of a stream, hold nonce (tokens split the code). "" is the event
// text itself (a plain-text stream). echo reports that only an echo of the
// prompt held it.
func streamPathFor(events []any, nonce, prompt string) (path string, ok, echo bool) {
	order := []string{}
	acc := map[string]*strings.Builder{}
	for _, ev := range events {
		for _, f := range stringLeaves(ev) {
			b, seen := acc[f.path]
			if !seen {
				b = &strings.Builder{}
				acc[f.path] = b
				order = append(order, f.path)
			}
			b.WriteString(f.value)
		}
	}
	for _, p := range order {
		s := acc[p].String()
		if !strings.Contains(s, nonce) {
			continue
		}
		if prompt != "" && strings.Contains(s, prompt) {
			echo = true
			continue
		}
		return p, true, false
	}
	return "", false, echo
}
