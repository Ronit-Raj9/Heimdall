package gates

import (
	"regexp"
	"strings"
	"unicode"
)

// pyLine is one physical line of Python read on its own, without the rest of
// the file. The reading is lexical only: string literals, comments and
// brackets are recognized, nothing is parsed. A line inside a multi-line
// string is read as if it were code; callers stay conservative about that.
type pyLine struct {
	norm   string // whitespace outside string literals collapsed, trimmed; comment kept
	code   string // norm without the comment
	indent int    // leading columns; a tab advances to the next multiple of 8
	blank  bool   // only whitespace
	depth  int    // net bracket depth change over the code
	minDep int    // lowest bracket depth reached while reading the code
	open   bool   // a string literal is still open at the end of the line
}

// wrapperCalls are the context managers an existing test line may be moved
// into without weakening it. None of them swallows an exception: pytest.warns
// and pytest.deprecated_call only add a requirement that a warning is raised,
// and warnings.catch_warnings only scopes the warning filters. pytest.raises,
// contextlib.suppress and every other block are absent on purpose, because
// they can turn a failing assertion into a pass.
var wrapperCalls = []string{"pytest.warns(", "pytest.deprecated_call(", "warnings.catch_warnings("}

// wrapperTail is what may follow the closing parenthesis of a wrapper call.
var wrapperTail = regexp.MustCompile(`^(as [A-Za-z_][A-Za-z0-9_]*\s*)?:$`)

// readPyLine reads one line of Python source. It is pure and never fails.
func readPyLine(s string) pyLine {
	pl := pyLine{indent: indentOf(s), blank: strings.TrimSpace(s) == ""}
	var norm, code strings.Builder
	depth := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '#':
			norm.WriteString(s[i:])
			i = len(s)
		case c == '"' || c == '\'':
			end, closed := stringEnd(s, i)
			norm.WriteString(s[i:end])
			code.WriteString(s[i:end])
			pl.open = !closed
			i = end
		case unicode.IsSpace(rune(c)):
			for i < len(s) && unicode.IsSpace(rune(s[i])) {
				i++
			}
			norm.WriteByte(' ')
			code.WriteByte(' ')
		default:
			switch c {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				depth--
				pl.minDep = min(pl.minDep, depth)
			}
			norm.WriteByte(c)
			code.WriteByte(c)
			i++
		}
	}
	pl.norm = strings.TrimSpace(norm.String())
	pl.code = strings.TrimSpace(code.String())
	pl.depth = depth
	return pl
}

// stringEnd returns the index just past the string literal that opens at
// s[i], and false when the literal is still open at the end of the line.
func stringEnd(s string, i int) (int, bool) {
	q := quoteAt(s, i)
	return closeQuote(s, i+len(q), q)
}

// quoteAt returns the quote that opens a string literal at s[i]: one quote
// character, or three for a triple-quoted string.
func quoteAt(s string, i int) string {
	q := s[i : i+1]
	if strings.HasPrefix(s[i:], strings.Repeat(q, 3)) {
		return strings.Repeat(q, 3)
	}
	return q
}

// closeQuote returns the index just past the first q at or after from, and
// false when there is none. A backslash always skips the next byte, which is
// also how a raw string ends.
func closeQuote(s string, from int, q string) (int, bool) {
	for j := from; j < len(s); {
		switch {
		case s[j] == '\\':
			j += 2
		case strings.HasPrefix(s[j:], q):
			return j + len(q), true
		default:
			j++
		}
	}
	return len(s), false
}

// pyState is what a Python reader carries from one line to the next: open
// brackets, an open triple-quoted string, a trailing backslash. A line that
// starts with any of them is a continuation of the statement before it.
type pyState struct {
	depth     int
	quote     string // the delimiter of an open triple-quoted string
	backslash bool
}

// fresh reports whether the next line starts a new statement.
func (st *pyState) fresh() bool {
	return st.depth == 0 && st.quote == "" && !st.backslash
}

// advance reads one more line. Brackets never go below zero, so a hunk that
// starts inside an expression only misreads its own first lines.
func (st *pyState) advance(s string) {
	i := 0
	if st.quote != "" {
		end, closed := closeQuote(s, 0, st.quote)
		if !closed {
			return
		}
		i, st.quote = end, ""
	}
	st.backslash = false
	for i < len(s) {
		c := s[i]
		switch {
		case c == '#':
			return
		case c == '"' || c == '\'':
			q := quoteAt(s, i)
			end, closed := closeQuote(s, i+len(q), q)
			if !closed && len(q) == 3 {
				st.quote = q
			}
			i = end
			continue
		case c == '(' || c == '[' || c == '{':
			st.depth++
		case (c == ')' || c == ']' || c == '}') && st.depth > 0:
			st.depth--
		}
		i++
	}
	st.backslash = strings.HasSuffix(strings.TrimRightFunc(s, unicode.IsSpace), `\`)
}

// indentOf counts the leading columns of s.
func indentOf(s string) int {
	n := 0
	for _, c := range s {
		switch c {
		case ' ':
			n++
		case '\t':
			n += 8 - n%8
		default:
			return n
		}
	}
	return n
}

// isHeader reports whether the line opens a block: its code ends with a colon.
func (pl pyLine) isHeader() bool {
	return strings.HasSuffix(pl.code, ":")
}

// isCompleteAssert reports whether the line is a whole assert statement on
// one line: brackets balanced, no open string, no continuation.
func (pl pyLine) isCompleteAssert() bool {
	if !strings.HasPrefix(pl.code, "assert ") && !strings.HasPrefix(pl.code, "assert(") {
		return false
	}
	return pl.depth == 0 && pl.minDep == 0 && !pl.open &&
		!strings.HasSuffix(pl.code, `\`) && !strings.HasSuffix(pl.code, ",")
}

// isWrapper reports whether code is a with statement over exactly one of the
// wrapperCalls, such as `with pytest.warns(DeprecationWarning, match="x"):`.
// A second context manager on the same line is not a wrapper.
func isWrapper(code string) bool {
	rest, ok := strings.CutPrefix(code, "with ")
	if !ok {
		return false
	}
	rest = strings.TrimSpace(rest)
	for _, call := range wrapperCalls {
		if !strings.HasPrefix(rest, call) {
			continue
		}
		end := closeParen(rest, len(call)-1)
		if end < 0 {
			return false
		}
		return wrapperTail.MatchString(strings.TrimSpace(rest[end+1:]))
	}
	return false
}

// closeParen returns the index of the bracket that closes the one at
// s[open], skipping string literals, or -1.
func closeParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); {
		c := s[i]
		switch c {
		case '"', '\'':
			end, closed := stringEnd(s, i)
			if !closed {
				return -1
			}
			i = end
			continue
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

// assertKept reports whether the added line still runs the removed assert
// unchanged: the same complete assert, optionally behind a one-line wrapper
// (`with pytest.warns(...): assert ...`) and optionally followed by an
// assertion message. Anything else around it, such as `or True`, a changed
// operand or an `if` prefix, does not keep it.
func assertKept(removed, added pyLine) bool {
	if !removed.isCompleteAssert() {
		return false
	}
	r, l := removed.code, added.code
	for from := 0; ; {
		i := strings.Index(l[from:], r)
		if i < 0 {
			return false
		}
		i += from
		prefix := strings.TrimSpace(l[:i])
		suffix := strings.TrimSpace(l[i+len(r):])
		if (prefix == "" || isWrapper(prefix)) && (suffix == "" || strings.HasPrefix(suffix, ",")) {
			return true
		}
		from = i + 1
	}
}

// pyImport is one import statement: its form and module, and the entries it
// imports exactly as written ("name" or "name as alias").
type pyImport struct {
	module  string // "from <module>" or "import"
	entries []string
}

// parseImport reads a single-line import statement. A statement that
// continues on the next line is not read.
func parseImport(pl pyLine) (pyImport, bool) {
	code := pl.code
	if pl.depth != 0 || pl.open || strings.HasSuffix(code, `\`) || strings.Contains(code, ";") {
		return pyImport{}, false
	}
	var imp pyImport
	var list string
	switch {
	case strings.HasPrefix(code, "from "):
		mod, names, ok := strings.Cut(strings.TrimPrefix(code, "from "), " import ")
		if !ok {
			return pyImport{}, false
		}
		imp.module, list = "from "+strings.TrimSpace(mod), names
	case strings.HasPrefix(code, "import "):
		imp.module, list = "import", strings.TrimPrefix(code, "import ")
	default:
		return pyImport{}, false
	}
	list = strings.TrimSpace(list)
	if strings.HasPrefix(list, "(") && strings.HasSuffix(list, ")") {
		list = list[1 : len(list)-1]
	}
	for _, e := range strings.Split(list, ",") {
		if e = strings.TrimSpace(e); e != "" {
			imp.entries = append(imp.entries, e)
		}
	}
	return imp, len(imp.entries) > 0
}

// covers reports whether imp imports every entry of old from the same module
// and binds every name only once, so no old name is shadowed by an alias in
// the same statement.
func (imp pyImport) covers(old pyImport) bool {
	if imp.module != old.module {
		return false
	}
	have := map[string]bool{}
	bound := map[string]bool{}
	for _, e := range imp.entries {
		have[e] = true
		b := boundName(e)
		if bound[b] {
			return false
		}
		bound[b] = true
	}
	for _, e := range old.entries {
		if !have[e] {
			return false
		}
	}
	return true
}

// boundName is the name an import entry binds: the alias, or the first dotted
// part of the imported name.
func boundName(entry string) string {
	if _, alias, ok := strings.Cut(entry, " as "); ok {
		return strings.TrimSpace(alias)
	}
	name, _, _ := strings.Cut(entry, ".")
	return strings.TrimSpace(name)
}
