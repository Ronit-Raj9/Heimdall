package secrets

import (
	"fmt"
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := []string{
		"key sk-abcdefghijklmnopqrstuvwx used",
		"ANTHROPIC token sk-ant-api03-abcdefghijklmnop",
		"api_key=supersecretvalue",
		`{"password":"hunter22hunter"}`,
		"Authorization: Bearer abcdefghijkl",
		"HEIMDALL_MASTER_KEY=bWFzdGVya2V5bWFzdGVya2V5",
		"HEIMDALL_DATABASE_URL=postgres://u:p@h/db",
		"dial postgres://heimdall:pa55word@db:5432/x failed",
		"gh token ghp_abcdefghijklmnopqrstuvwxyz0123",
		"aws AKIAABCDEFGHIJKLMNOP",
		"jwt eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0.SflKxwRJSMeKKF2QT4",
	}
	leaks := []string{"sk-abcdefghijklmnopqrstuvwx", "abcdefghijklmnop", "supersecretvalue", "hunter22hunter",
		"abcdefghijkl", "bWFzdGVya2V5bWFzdGVya2V5", "u:p@", "pa55word", "ghp_abcdefghijklmnopqrstuvwxyz0123",
		"AKIAABCDEFGHIJKLMNOP", "SflKxwRJSMeKKF2QT4"}
	for i, c := range cases {
		got := Redact(c)
		if strings.Contains(got, leaks[i]) {
			t.Errorf("Redact(%q) = %q still holds %q", c, got, leaks[i])
		}
		if !strings.Contains(got, Mask) {
			t.Errorf("Redact(%q) = %q has no mask", c, got)
		}
	}
	if got := Redact("plain text, 12 tasks done"); got != "plain text, 12 tasks done" {
		t.Errorf("plain text changed: %q", got)
	}
	if got := Redact("value literal-secret-123 here", "literal-secret-123", "short"); strings.Contains(got, "literal-secret-123") {
		t.Errorf("literal kept: %q", got)
	}
}

type memLog struct{ lines []string }

func (m *memLog) Printf(f string, a ...any) { m.lines = append(m.lines, fmt.Sprintf(f, a...)) }
func (m *memLog) Warnf(f string, a ...any)  { m.Printf(f, a...) }
func (m *memLog) Errorf(f string, a ...any) { m.Printf(f, a...) }
func (m *memLog) Write(p []byte) (int, error) {
	m.lines = append(m.lines, string(p))
	return len(p), nil
}

func TestLoggerRedactsSplitWrites(t *testing.T) {
	m := &memLog{}
	l := Logger(m)
	_, _ = l.Write([]byte("first line\nkey sk-abcdefgh"))
	_, _ = l.Write([]byte("ijklmnopqrstuvwx end\ntail api_key=abcdefgh"))
	l.Printf("printf %s", "token=zzzzzzzzzz")
	l.Flush()
	joined := strings.Join(m.lines, "\n")
	for _, leak := range []string{"sk-abcdefghijklmnopqrstuvwx", "abcdefgh\n", "zzzzzzzzzz"} {
		if strings.Contains(joined, leak) {
			t.Fatalf("log leaked %q:\n%s", leak, joined)
		}
	}
	if len(m.lines) != 4 || m.lines[0] != "first line" {
		t.Fatalf("lines = %q", m.lines)
	}
}
