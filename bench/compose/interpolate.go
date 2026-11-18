package compose

import (
	"fmt"
	"strings"
)

// interpolation is the result of expanding the variables in one value.
type interpolation struct {
	value string
	used  []string // every variable the value read, set or not
	unset []string // variables read with no value and no default: compose would take them from the host
	err   error    // a ${VAR:?message} that fired, or a malformed expression
}

// interpolate expands compose variable syntax in s: $VAR, ${VAR},
// ${VAR:-default}, ${VAR-default}, ${VAR:?error}, ${VAR?error},
// ${VAR:+replacement}, ${VAR+replacement}, and $$ for a literal $. Defaults
// and replacements may hold variables themselves. lookup reports a variable's
// value and whether it is set; a run never reads the host environment, so
// the caller decides what is known. It is pure when lookup is.
func interpolate(s string, lookup func(string) (string, bool)) interpolation {
	var res interpolation
	res.value = expand(s, lookup, &res)
	return res
}

func expand(s string, lookup func(string) (string, bool), res *interpolation) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '$' || i+1 >= len(s) {
			b.WriteByte(c)
			continue
		}
		next := s[i+1]
		switch {
		case next == '$':
			b.WriteByte('$')
			i++
		case next == '{':
			end := matchBrace(s, i+1)
			if end < 0 {
				if res.err == nil {
					res.err = fmt.Errorf("unclosed ${ in %q", s)
				}
				b.WriteString(s[i:])
				return b.String()
			}
			b.WriteString(braced(s[i+2:end], lookup, res))
			i = end
		case next == '_' || isLetter(next):
			j := i + 1
			for j < len(s) && (s[j] == '_' || isLetter(s[j]) || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			name := s[i+1 : j]
			b.WriteString(read(name, lookup, res, true))
			i = j - 1
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// braced expands the inside of one ${...}.
func braced(expr string, lookup func(string) (string, bool), res *interpolation) string {
	j := 0
	for j < len(expr) && (expr[j] == '_' || isLetter(expr[j]) || (j > 0 && expr[j] >= '0' && expr[j] <= '9')) {
		j++
	}
	name, rest := expr[:j], expr[j:]
	if name == "" {
		if res.err == nil {
			res.err = fmt.Errorf("${%s} names no variable", expr)
		}
		return ""
	}
	if rest == "" {
		return read(name, lookup, res, true)
	}
	op := rest[:1]
	colon := false
	if op == ":" && len(rest) > 1 {
		colon, op, rest = true, rest[1:2], rest[2:]
	} else {
		rest = rest[1:]
	}
	val, set := lookup(name)
	res.used = append(res.used, name)
	empty := !set || (colon && val == "")
	switch op {
	case "-":
		if empty {
			return expand(rest, lookup, res)
		}
		return val
	case "?":
		if empty {
			msg := expand(rest, lookup, res)
			if msg == "" {
				msg = "required"
			}
			if res.err == nil {
				res.err = fmt.Errorf("variable %s: %s", name, msg)
			}
			return ""
		}
		return val
	case "+":
		if empty {
			return ""
		}
		return expand(rest, lookup, res)
	}
	if res.err == nil {
		res.err = fmt.Errorf("${%s}: unsupported operator %q", expr, op)
	}
	return ""
}

// read is one plain variable reference.
func read(name string, lookup func(string) (string, bool), res *interpolation, record bool) string {
	val, set := lookup(name)
	if record {
		res.used = append(res.used, name)
	}
	if !set {
		res.unset = append(res.unset, name)
		return ""
	}
	return val
}

// matchBrace returns the index of the "}" closing the "{" at open, counting
// nested ${...}, or -1.
func matchBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
