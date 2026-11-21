// Package secrets masks secrets in text the bench shows users: job logs,
// job errors and job results (web parity, lane L02).
package secrets

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/utils"
)

// Mask replaces every secret Redact finds.
const Mask = "[redacted]"

// tokenPattern catches bare API keys that carry no key name: OpenAI and
// Anthropic style sk- keys, Stripe style sk_ keys, GitHub tokens, Slack
// tokens, AWS access key ids, Google API keys, Groq keys and JWTs.
var tokenPattern = regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{16,}|sk_[A-Za-z0-9]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|xox[abpr]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{30,}|gsk_[A-Za-z0-9]{20,}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,})\b`)

// urlCredPattern catches user:password@ in URLs (database and S3 URLs).
var urlCredPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s:/@]+:[^\s@/]+@`)

// envSecretPattern catches NAME=value for environment variables whose name
// says they hold a secret (HEIMDALL_MASTER_KEY=..., AWS_SECRET_ACCESS_KEY=...).
var envSecretPattern = regexp.MustCompile(`\b([A-Z][A-Z0-9_]*(?:KEY|SECRET|TOKEN|PASSWORD|PASSWD|DATABASE_URL|DSN|CREDENTIALS))=([^\s,;]+)`)

// Redact returns s with every secret shape the bench knows masked, plus the
// given literal secrets (8 characters or more, so short words survive).
func Redact(s string, literal ...string) string {
	for _, k := range literal {
		if len(k) >= 8 {
			s = strings.ReplaceAll(s, k, Mask)
		}
	}
	s = urlCredPattern.ReplaceAllString(s, "${1}"+Mask+"@")
	s = envSecretPattern.ReplaceAllString(s, "${1}="+Mask)
	s = utils.RedactSecrets(s)
	return tokenPattern.ReplaceAllString(s, Mask)
}

// Logger wraps a job logger so every line it writes is redacted first.
// Write buffers a partial line until its newline so a secret split across
// two writes is still caught; call Flush before the job ends.
func Logger(l jobs.Logger, literal ...string) *RedactingLogger {
	return &RedactingLogger{next: l, literal: literal}
}

// RedactingLogger is the jobs.Logger Logger returns.
type RedactingLogger struct {
	next    jobs.Logger
	literal []string

	mu      sync.Mutex
	partial bytes.Buffer
}

var _ jobs.Logger = (*RedactingLogger)(nil)

func (r *RedactingLogger) Printf(format string, args ...any) {
	r.next.Printf("%s", Redact(fmt.Sprintf(format, args...), r.literal...))
}

func (r *RedactingLogger) Warnf(format string, args ...any) {
	r.next.Warnf("%s", Redact(fmt.Sprintf(format, args...), r.literal...))
}

func (r *RedactingLogger) Errorf(format string, args ...any) {
	r.next.Errorf("%s", Redact(fmt.Sprintf(format, args...), r.literal...))
}

// Write forwards each complete line of p, redacted, as one info line.
func (r *RedactingLogger) Write(p []byte) (int, error) {
	r.mu.Lock()
	r.partial.Write(p)
	var lines []string
	for {
		b := r.partial.Bytes()
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, string(b[:i]))
		r.partial.Next(i + 1)
	}
	r.mu.Unlock()
	for _, s := range lines {
		r.next.Printf("%s", Redact(strings.TrimRight(s, "\r"), r.literal...))
	}
	return len(p), nil
}

// Flush writes a pending partial line, redacted.
func (r *RedactingLogger) Flush() {
	r.mu.Lock()
	rest := r.partial.String()
	r.partial.Reset()
	r.mu.Unlock()
	if rest != "" {
		r.next.Printf("%s", Redact(rest, r.literal...))
	}
}
