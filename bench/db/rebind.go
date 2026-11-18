package db

import (
	"strconv"
	"strings"
)

// rebindDollar turns each `?` placeholder into $1..$n. Question marks inside
// string literals ('..', E'..' with backslash escapes), quoted identifiers
// ("..", `..`), dollar-quoted bodies ($tag$..$tag$) and comments (-- and
// /* */) are left alone. Postgres JSON operators spelled with `?` are not
// supported in portable SQL; use jsonb_exists() and friends instead.
func rebindDollar(q string) string {
	if !strings.ContainsRune(q, '?') {
		return q
	}
	var b strings.Builder
	b.Grow(len(q) + 8)
	n := 0
	for i := 0; i < len(q); {
		c := q[i]
		switch {
		case c == '\'':
			escapes := i > 0 && (q[i-1] == 'E' || q[i-1] == 'e') && (i == 1 || !isIdentByte(q[i-2]))
			end := quotedEnd(q, i, '\'', escapes)
			b.WriteString(q[i:end])
			i = end
		case c == '"' || c == '`':
			end := quotedEnd(q, i, c, false)
			b.WriteString(q[i:end])
			i = end
		case c == '-' && i+1 < len(q) && q[i+1] == '-':
			end := strings.IndexByte(q[i:], '\n')
			if end < 0 {
				end = len(q)
			} else {
				end += i + 1
			}
			b.WriteString(q[i:end])
			i = end
		case c == '/' && i+1 < len(q) && q[i+1] == '*':
			end := strings.Index(q[i+2:], "*/")
			if end < 0 {
				end = len(q)
			} else {
				end += i + 4
			}
			b.WriteString(q[i:end])
			i = end
		case c == '$' && (i == 0 || !isIdentByte(q[i-1])):
			if tag, ok := dollarTag(q[i:]); ok {
				end := strings.Index(q[i+len(tag):], tag)
				if end < 0 {
					end = len(q)
				} else {
					end += i + 2*len(tag)
				}
				b.WriteString(q[i:end])
				i = end
				continue
			}
			b.WriteByte(c)
			i++
		case c == '?':
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// quotedEnd returns the index just past the literal that opens at q[start].
// A doubled quote stays inside; with escapes a backslash skips one byte.
func quotedEnd(q string, start int, quote byte, escapes bool) int {
	for j := start + 1; j < len(q); j++ {
		switch {
		case escapes && q[j] == '\\':
			j++
		case q[j] == quote:
			if j+1 < len(q) && q[j+1] == quote {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(q)
}

// dollarTag reports the opening tag of a dollar-quoted string ($$ or $name$)
// at the start of s. $1 (a positional parameter) is not a tag.
func dollarTag(s string) (string, bool) {
	for j := 1; j < len(s); j++ {
		c := s[j]
		if c == '$' {
			return s[:j+1], true
		}
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > 1 && c >= '0' && c <= '9') {
			return "", false
		}
	}
	return "", false
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
