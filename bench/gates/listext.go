package gates

import (
	"regexp"
	"strings"
)

// keptList is the reason a removed line is kept because an added line or
// statement holds the same list literal with new items appended.
const keptList = "list extended"

// bracketSpace matches whitespace around brackets and commas, which a list
// that gains an item or is wrapped onto several lines changes freely.
var bracketSpace = regexp.MustCompile(`\s*([\[\](),])\s*`)

// compactCode drops the whitespace around brackets and commas and a trailing
// comma, so `["a", "b"]`, `["a","b",]` and a wrapped copy compare equal.
func compactCode(code string) string {
	c := bracketSpace.ReplaceAllString(strings.TrimSpace(code), "$1")
	c = strings.ReplaceAll(c, ",]", "]")
	return strings.TrimSuffix(c, ",")
}

// listExtended reports whether newCode is oldCode with items appended to a
// list: a one-line list `x = [a, b]` that became `x = [a, b, c]`, or a list
// item line `a,` that became `a, b,`. Every existing item stays, in order, so
// the test checks at least what it checked before (the django username lists
// that gain a trailing-newline case). An assert is never judged this way:
// appending to an expected value changes it.
func listExtended(oldCode, newCode string) bool {
	o, n := compactCode(oldCode), compactCode(newCode)
	if o == "" || strings.HasPrefix(o, "assert") || strings.HasPrefix(n, "assert") {
		return false
	}
	if strings.HasSuffix(o, "]") {
		// A whole list: the same text up to its closing bracket, then new
		// items, then the bracket.
		head := strings.TrimSuffix(o, "]")
		if strings.HasSuffix(head, "[") || !strings.HasPrefix(n, head+",") || !strings.HasSuffix(n, "]") {
			return false
		}
		return balancedItems(n[len(head)+1 : len(n)-1])
	}
	// A list item line: the same items, then more. Only a line inside an
	// open bracket is one (keepExtendedLine checks that); `import os, sys`
	// is not.
	if !strings.HasPrefix(n, o+",") {
		return false
	}
	return balancedItems(n[len(o)+1:])
}

// balancedItems reports whether s is one or more list items that never close
// a bracket they did not open, so the appended text cannot end the list early
// and slice or index it. A bracket inside a string is counted too, which only
// ever rejects.
func balancedItems(s string) bool {
	if strings.TrimSpace(s) == "" {
		return false
	}
	depth := 0
	for _, c := range s {
		switch c {
		case '[', '(', '{':
			depth++
		case ']', ')', '}':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// keepExtendedStmts keeps every line of a removed statement that an unused
// added statement in the same block repeats with list items appended, such as
// a one-line list rewrapped onto several lines with a new case. It runs before
// the line rules and returns the removed lines it kept.
func (e *editScan) keepExtendedStmts(used map[lineRef]bool) map[lineRef]bool {
	kept := map[lineRef]bool{}
	for h := range e.hunks {
		olds, news := e.views[0][h].stmts, e.views[1][h].stmts
		for _, old := range olds {
			if old.mark != '-' || !strings.HasSuffix(compactCode(old.code), "]") {
				continue
			}
			for _, ns := range news {
				a := lineRef{h, ns.start}
				if ns.mark != '+' || used[a] || !listExtended(old.code, ns.code) || !e.sameBlock(lineRef{h, old.start}, a) {
					continue
				}
				for i := ns.start; i <= ns.end; i++ {
					if e.hunks[h].Lines[i][0] == '+' {
						used[lineRef{h, i}] = true
					}
				}
				for i := old.start; i <= old.end; i++ {
					if e.hunks[h].Lines[i][0] == '-' {
						kept[lineRef{h, i}] = true
					}
				}
				break
			}
		}
	}
	return kept
}

// keepExtendedLine keeps removed line r when an unused added line in the same
// statement repeats it with list items appended: a whole one-line list, or an
// item line inside an open bracket (both lines continue their statement).
func (e *editScan) keepExtendedLine(r lineRef, used map[lineRef]bool) bool {
	rc := e.view(r, '-').cont[r.idx]
	whole := strings.HasSuffix(compactCode(e.line(r).code), "]")
	if !rc && !whole {
		return false
	}
	for _, a := range e.added {
		if used[a] || e.view(a, '+').cont[a.idx] != rc {
			continue
		}
		if listExtended(e.line(r).code, e.line(a).code) && e.sameBlock(r, a) {
			used[a] = true
			return true
		}
	}
	return false
}

// newTestDecorator reports whether added line a is a decorator of a function
// or class the edit adds whole, under a name no removed line defined. A skip
// or xfail mark there only governs the agent's own new test (astropy's
// `@pytest.mark.skipif('not HAS_BS4')`, copied from the tests beside it) and
// switches no existing test off. A mark put on an existing test, or on one
// removed and added back, is still a disabling line.
func (e *editScan) newTestDecorator(a lineRef) bool {
	v := e.view(a, '+')
	si := v.of[a.idx]
	if v.stmts[si].mark != '+' || !strings.HasPrefix(v.stmts[si].code, "@") {
		return false
	}
	for p := si + 1; p < len(v.stmts); p++ {
		s := v.stmts[p]
		if s.mark != '+' {
			return false
		}
		if strings.HasPrefix(s.code, "@") {
			continue
		}
		name, ok := defName(s.code)
		if !ok {
			return false
		}
		for _, r := range e.removed {
			if n, ok := defName(e.line(r).code); ok && n == name {
				return false
			}
		}
		return true
	}
	return false
}

// defName returns the name a def, async def or class statement defines.
func defName(code string) (string, bool) {
	for _, kw := range []string{"async def ", "def ", "class "} {
		if rest, ok := strings.CutPrefix(code, kw); ok {
			end := strings.IndexAny(rest, "(:")
			if end <= 0 {
				return "", false
			}
			return strings.TrimSpace(rest[:end]), true
		}
	}
	return "", false
}
