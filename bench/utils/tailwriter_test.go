package utils

import (
	"fmt"
	"strings"
	"testing"
)

func TestTailWriterKeepsLastLines(t *testing.T) {
	w := NewTailWriter(3)
	for i := 1; i <= 10; i++ {
		fmt.Fprintf(w, "line %d\n", i)
	}
	if got := w.String(); got != "line 8\nline 9\nline 10" {
		t.Fatalf("tail %q", got)
	}
	// Writes split mid-line join; a last line without a newline counts.
	w = NewTailWriter(2)
	for _, p := range []string{"ab", "c\nde", "f\r\n", "", "g"} {
		_, _ = w.Write([]byte(p))
	}
	if got := w.Tail(); strings.Join(got, "|") != "def|g" {
		t.Fatalf("tail %q", got)
	}
	// The zero value keeps 15 lines.
	var z TailWriter
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&z, "%d\n", i)
	}
	if got := z.Tail(); len(got) != DefaultTailLines || got[0] != "6" || got[14] != "20" {
		t.Fatalf("zero tail %q", got)
	}
	// A very long line without a newline stays bounded.
	w = NewTailWriter(1)
	_, _ = w.Write([]byte(strings.Repeat("x", 3*maxTailLine)))
	if got := w.String(); len(got) != maxTailLine {
		t.Fatalf("long line kept %d bytes", len(got))
	}
}
