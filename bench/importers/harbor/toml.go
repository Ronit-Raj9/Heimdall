package harbor

import (
	"fmt"
	"strconv"
	"strings"
)

// parseTOML reads the subset of TOML that Harbor task.toml files use: tables,
// arrays of tables, dotted and quoted keys, basic and literal strings (also
// multi-line), integers, floats, booleans, arrays and inline tables. Dates
// and other syntax are refused with the line number, so an unfamiliar file is
// skipped with a reason instead of read wrongly. The go.mod has no TOML
// library and must not gain one, hence this small reader.
func parseTOML(src string) (map[string]any, error) {
	p := &tomlParser{src: src, line: 1}
	root := map[string]any{}
	cur := root
	for {
		p.skipBlank()
		if p.eof() {
			return root, nil
		}
		switch {
		case strings.HasPrefix(p.rest(), "[["):
			p.pos += 2
			keys, err := p.key()
			if err != nil {
				return nil, err
			}
			p.skipSpace()
			if !strings.HasPrefix(p.rest(), "]]") {
				return nil, p.errorf("want ]] after array of tables header")
			}
			p.pos += 2
			t, err := appendTable(root, keys)
			if err != nil {
				return nil, p.errorf("%v", err)
			}
			cur = t
		case p.peek() == '[':
			p.pos++
			keys, err := p.key()
			if err != nil {
				return nil, err
			}
			p.skipSpace()
			if p.peek() != ']' {
				return nil, p.errorf("want ] after table header")
			}
			p.pos++
			t, err := table(root, keys)
			if err != nil {
				return nil, p.errorf("%v", err)
			}
			cur = t
		default:
			if err := p.keyValue(cur); err != nil {
				return nil, err
			}
		}
		if err := p.endOfLine(); err != nil {
			return nil, err
		}
	}
}

// tomlParser is a cursor over the source with the current line number.
type tomlParser struct {
	src  string
	pos  int
	line int
}

func (p *tomlParser) eof() bool    { return p.pos >= len(p.src) }
func (p *tomlParser) rest() string { return p.src[p.pos:] }

func (p *tomlParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.src[p.pos]
}

func (p *tomlParser) errorf(format string, args ...any) error {
	return fmt.Errorf("task.toml line %d: %s", p.line, fmt.Sprintf(format, args...))
}

// skipSpace skips spaces and tabs on the current line.
func (p *tomlParser) skipSpace() {
	for !p.eof() && (p.peek() == ' ' || p.peek() == '\t') {
		p.pos++
	}
}

// skipBlank skips whitespace, newlines and comments.
func (p *tomlParser) skipBlank() {
	for !p.eof() {
		switch c := p.peek(); {
		case c == ' ' || c == '\t' || c == '\r':
			p.pos++
		case c == '\n':
			p.pos++
			p.line++
		case c == '#':
			for !p.eof() && p.peek() != '\n' {
				p.pos++
			}
		default:
			return
		}
	}
}

// endOfLine accepts trailing spaces and a comment, then a newline or the end.
func (p *tomlParser) endOfLine() error {
	p.skipSpace()
	if p.peek() == '#' {
		for !p.eof() && p.peek() != '\n' {
			p.pos++
		}
	}
	if p.peek() == '\r' {
		p.pos++
	}
	if p.eof() {
		return nil
	}
	if p.peek() != '\n' {
		return p.errorf("unexpected %q after value", p.peek())
	}
	p.pos++
	p.line++
	return nil
}

// key reads a possibly dotted key of bare or quoted parts.
func (p *tomlParser) key() ([]string, error) {
	var keys []string
	for {
		p.skipSpace()
		var k string
		switch c := p.peek(); {
		case c == '"' || c == '\'':
			s, err := p.str()
			if err != nil {
				return nil, err
			}
			k = s
		default:
			start := p.pos
			for !p.eof() && isBareKey(p.peek()) {
				p.pos++
			}
			if start == p.pos {
				return nil, p.errorf("want a key")
			}
			k = p.src[start:p.pos]
		}
		keys = append(keys, k)
		p.skipSpace()
		if p.peek() != '.' {
			return keys, nil
		}
		p.pos++
	}
}

func isBareKey(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// keyValue reads `key = value` into t.
func (p *tomlParser) keyValue(t map[string]any) error {
	keys, err := p.key()
	if err != nil {
		return err
	}
	p.skipSpace()
	if p.peek() != '=' {
		return p.errorf("want = after key %s", strings.Join(keys, "."))
	}
	p.pos++
	p.skipSpace()
	v, err := p.value()
	if err != nil {
		return err
	}
	parent, err := table(t, keys[:len(keys)-1])
	if err != nil {
		return p.errorf("%v", err)
	}
	last := keys[len(keys)-1]
	if _, dup := parent[last]; dup {
		return p.errorf("key %s defined twice", strings.Join(keys, "."))
	}
	parent[last] = v
	return nil
}

// value reads one value.
func (p *tomlParser) value() (any, error) {
	switch c := p.peek(); {
	case c == '"' || c == '\'':
		return p.str()
	case c == '[':
		return p.array()
	case c == '{':
		return p.inlineTable()
	case strings.HasPrefix(p.rest(), "true"):
		p.pos += 4
		return true, nil
	case strings.HasPrefix(p.rest(), "false"):
		p.pos += 5
		return false, nil
	default:
		return p.number()
	}
}

// number reads an integer (int64) or a float (float64).
func (p *tomlParser) number() (any, error) {
	start := p.pos
	for !p.eof() && strings.IndexByte("+-0123456789._eExabcdfABCDFinf", p.peek()) >= 0 {
		p.pos++
	}
	raw := strings.ReplaceAll(p.src[start:p.pos], "_", "")
	if raw == "" {
		return nil, p.errorf("want a value")
	}
	if i, err := strconv.ParseInt(raw, 0, 64); err == nil {
		return i, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, p.errorf("unsupported value %q", raw)
	}
	return f, nil
}

// array reads [v, v, ...], across lines, with comments and a trailing comma.
func (p *tomlParser) array() ([]any, error) {
	p.pos++
	out := []any{}
	for {
		p.skipBlank()
		if p.peek() == ']' {
			p.pos++
			return out, nil
		}
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.skipBlank()
		switch p.peek() {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return out, nil
		default:
			return nil, p.errorf("want , or ] in array")
		}
	}
}

// inlineTable reads {k = v, ...} on one line.
func (p *tomlParser) inlineTable() (map[string]any, error) {
	p.pos++
	out := map[string]any{}
	p.skipSpace()
	if p.peek() == '}' {
		p.pos++
		return out, nil
	}
	for {
		if err := p.keyValue(out); err != nil {
			return nil, err
		}
		p.skipSpace()
		switch p.peek() {
		case ',':
			p.pos++
		case '}':
			p.pos++
			return out, nil
		default:
			return nil, p.errorf("want , or } in inline table")
		}
	}
}

// str reads a basic, literal or multi-line string.
func (p *tomlParser) str() (string, error) {
	q := p.peek()
	multi := strings.HasPrefix(p.rest(), strings.Repeat(string(q), 3))
	delim := string(q)
	if multi {
		delim = strings.Repeat(string(q), 3)
	}
	p.pos += len(delim)
	if multi && strings.HasPrefix(p.rest(), "\n") {
		p.pos++
		p.line++
	} else if multi && strings.HasPrefix(p.rest(), "\r\n") {
		p.pos += 2
		p.line++
	}
	var b strings.Builder
	for {
		if p.eof() {
			return "", p.errorf("unterminated string")
		}
		if strings.HasPrefix(p.rest(), delim) {
			p.pos += len(delim)
			return b.String(), nil
		}
		c := p.peek()
		if c == '\n' {
			if !multi {
				return "", p.errorf("newline in string")
			}
			p.line++
		}
		if c != '\\' || q == '\'' {
			b.WriteByte(c)
			p.pos++
			continue
		}
		if err := p.escape(&b, multi); err != nil {
			return "", err
		}
	}
}

// escape reads one backslash escape of a basic string.
func (p *tomlParser) escape(b *strings.Builder, multi bool) error {
	p.pos++
	if p.eof() {
		return p.errorf("unterminated escape")
	}
	c := p.peek()
	p.pos++
	switch c {
	case 'n':
		b.WriteByte('\n')
	case 't':
		b.WriteByte('\t')
	case 'r':
		b.WriteByte('\r')
	case 'b':
		b.WriteByte('\b')
	case 'f':
		b.WriteByte('\f')
	case '"', '\\':
		b.WriteByte(c)
	case 'u', 'U':
		n := 4
		if c == 'U' {
			n = 8
		}
		if p.pos+n > len(p.src) {
			return p.errorf("short unicode escape")
		}
		r, err := strconv.ParseUint(p.src[p.pos:p.pos+n], 16, 32)
		if err != nil {
			return p.errorf("bad unicode escape")
		}
		b.WriteRune(rune(r))
		p.pos += n
	case '\n', ' ', '\t', '\r':
		if !multi {
			return p.errorf("bad escape")
		}
		// A line-ending backslash trims the newline and following whitespace.
		p.pos--
		for !p.eof() && strings.IndexByte(" \t\r\n", p.peek()) >= 0 {
			if p.peek() == '\n' {
				p.line++
			}
			p.pos++
		}
	default:
		return p.errorf("bad escape \\%c", c)
	}
	return nil
}

// table returns the table at keys under root, creating missing ones.
func table(root map[string]any, keys []string) (map[string]any, error) {
	t := root
	for _, k := range keys {
		switch v := t[k].(type) {
		case nil:
			n := map[string]any{}
			t[k] = n
			t = n
		case map[string]any:
			t = v
		case []any:
			last, ok := lastTable(v)
			if !ok {
				return nil, fmt.Errorf("key %s is not a table", k)
			}
			t = last
		default:
			return nil, fmt.Errorf("key %s is not a table", k)
		}
	}
	return t, nil
}

// appendTable adds a new table to the array of tables at keys.
func appendTable(root map[string]any, keys []string) (map[string]any, error) {
	parent, err := table(root, keys[:len(keys)-1])
	if err != nil {
		return nil, err
	}
	last := keys[len(keys)-1]
	n := map[string]any{}
	switch v := parent[last].(type) {
	case nil:
		parent[last] = []any{n}
	case []any:
		parent[last] = append(v, n)
	default:
		return nil, fmt.Errorf("key %s is not an array of tables", last)
	}
	return n, nil
}

func lastTable(v []any) (map[string]any, bool) {
	if len(v) == 0 {
		return nil, false
	}
	t, ok := v[len(v)-1].(map[string]any)
	return t, ok
}
