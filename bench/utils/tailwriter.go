package utils

import (
	"bytes"
	"strings"
	"sync"
)

// TailWriter is an io.Writer that keeps only the last Lines lines written
// to it (a ring of lines), so a failed build can name its last output
// without holding the whole log. A partial last line counts as a line. It
// is safe for concurrent use. The zero value keeps 15 lines.
type TailWriter struct {
	Lines int

	mu      sync.Mutex
	lines   []string
	partial []byte
}

// DefaultTailLines is how many lines a zero TailWriter keeps.
const DefaultTailLines = 15

// maxTailLine bounds one kept line, so a build that prints no newline
// cannot grow the buffer without limit.
const maxTailLine = 4096

// NewTailWriter returns a TailWriter that keeps the last n lines.
func NewTailWriter(n int) *TailWriter { return &TailWriter{Lines: n} }

func (t *TailWriter) max() int {
	if t.Lines <= 0 {
		return DefaultTailLines
	}
	return t.Lines
}

// Write implements io.Writer.
func (t *TailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rest := p
	for {
		i := bytes.IndexByte(rest, '\n')
		if i < 0 {
			t.partial = append(t.partial, rest...)
			if len(t.partial) > maxTailLine {
				t.partial = t.partial[len(t.partial)-maxTailLine:]
			}
			return len(p), nil
		}
		line := append(t.partial, rest[:i]...)
		t.partial = nil
		t.push(string(line))
		rest = rest[i+1:]
	}
}

func (t *TailWriter) push(line string) {
	line = strings.TrimRight(line, "\r")
	if len(line) > maxTailLine {
		line = line[len(line)-maxTailLine:]
	}
	t.lines = append(t.lines, line)
	if over := len(t.lines) - t.max(); over > 0 {
		t.lines = append(t.lines[:0:0], t.lines[over:]...)
	}
}

// Tail returns the kept lines, the oldest first, without blank lines at
// either end.
func (t *TailWriter) Tail() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := append([]string(nil), t.lines...)
	if len(t.partial) > 0 {
		out = append(out, strings.TrimRight(string(t.partial), "\r"))
		if over := len(out) - t.max(); over > 0 {
			out = out[over:]
		}
	}
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

// String returns the kept lines joined by newlines.
func (t *TailWriter) String() string { return strings.Join(t.Tail(), "\n") }
