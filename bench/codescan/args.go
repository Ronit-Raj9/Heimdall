package codescan

import "strings"

// Call argument helpers. They read the raw text after a call's opening
// parenthesis, on one line: an argument list that goes on past the line end
// is judged on what the line shows.

// splitArgs returns the top-level arguments of the call whose text starts
// after its "(", up to the matching ")" or the line end, and whether the
// closing parenthesis was seen.
func splitArgs(lang, s string) ([]string, bool) {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			switch {
			case c == '\\' && quote != '`':
				i++
			case c == quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			quote = c
		case '`':
			if lang != LangPython {
				quote = c
			}
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				if a := strings.TrimSpace(s[start:i]); a != "" || len(out) > 0 {
					out = append(out, a)
				}
				return out, true
			}
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		case '#':
			if lang == LangPython {
				s = s[:i]
			}
		}
	}
	if a := strings.TrimSpace(s[start:]); a != "" {
		out = append(out, a)
	}
	return out, false
}

// firstArgOf returns the call's first argument, or "".
func firstArgOf(s string) string {
	args, _ := splitArgs("", s)
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// firstArgsOf returns the call's first n arguments joined by commas.
func firstArgsOf(s string, n int) string {
	args, _ := splitArgs("", s)
	if len(args) > n {
		args = args[:n]
	}
	return strings.Join(args, ", ")
}

// constCall reports whether every argument of the call is a constant string
// literal, or the call has none. Keyword arguments (name=...) are not
// counted: they do not hold the code.
func constCall(lang, s string) bool {
	args, _ := splitArgs(lang, s)
	for _, a := range args {
		if a == "" || (lang == LangPython && isKeywordArg(a)) {
			continue
		}
		if !isConstLiteral(lang, a) {
			return false
		}
	}
	return true
}

// constFirstArg reports whether the call's first argument is a constant
// string literal. A call whose argument starts on the next line cannot be
// judged and counts as not constant.
func constFirstArg(lang, s string) bool {
	args, _ := splitArgs(lang, s)
	if len(args) == 0 || args[0] == "" {
		return false
	}
	return isConstLiteral(lang, args[0])
}

// isKeywordArg reports whether a Python argument is name=value.
func isKeywordArg(a string) bool {
	i := strings.IndexByte(a, '=')
	if i <= 0 || (i+1 < len(a) && a[i+1] == '=') {
		return false
	}
	for _, c := range []byte(strings.TrimSpace(a[:i])) {
		if !isWordByte(c) {
			return false
		}
	}
	return true
}

// isConstLiteral reports whether a is one string literal with no
// interpolation: no f-string, no ${...} in a template literal, nothing
// concatenated or formatted onto it. A Python string that opens with triple
// quotes and does not close on the line is taken as constant.
func isConstLiteral(lang, a string) bool {
	i := 0
	if lang == LangPython {
		for i < len(a) && strings.IndexByte("rRbBuU", a[i]) >= 0 {
			i++
		}
		if i < len(a) && (a[i] == 'f' || a[i] == 'F') {
			return false
		}
	}
	if i >= len(a) {
		return false
	}
	q := a[i]
	if q != '"' && q != '\'' && !(q == '`' && lang != LangPython) {
		return false
	}
	if lang == LangPython && strings.HasPrefix(a[i:], strings.Repeat(string(q), 3)) {
		end := strings.Index(a[i+3:], strings.Repeat(string(q), 3))
		if end < 0 {
			return true
		}
		return strings.TrimSpace(a[i+3+end+3:]) == ""
	}
	for j := i + 1; j < len(a); j++ {
		switch {
		case a[j] == '\\' && q != '`':
			j++
		case q == '`' && a[j] == '$' && j+1 < len(a) && a[j+1] == '{' && lang != LangGo:
			return false
		case a[j] == q:
			return strings.TrimSpace(a[j+1:]) == ""
		}
	}
	return false
}
