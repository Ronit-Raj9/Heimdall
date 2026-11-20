package jobs

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

// Log levels stored in job_logs.level.
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// Logger writes a job's log. Lines go to job_logs with increasing seq; per
// job the first LogHead and the newest LogTail lines are kept. As an
// io.Writer every complete line written is an info line, so code that prints
// to a writer (the CLI's operations) logs into the job unchanged.
type Logger interface {
	Printf(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
	Write(p []byte) (int, error)
}

// jobLogger is the Logger of one running job.
type jobLogger struct {
	r   *Runner
	job string

	mu      sync.Mutex
	seq     int64
	partial bytes.Buffer
}

func (r *Runner) newLogger(j *Job) *jobLogger {
	seq, err := r.lastLogSeq(context.Background(), j.ID)
	if err != nil {
		r.opts.Logger.Warn("read job log seq", "job", j.ID, "error", err.Error())
	}
	return &jobLogger{r: r, job: j.ID, seq: seq}
}

func (l *jobLogger) Printf(format string, args ...any) {
	l.line(LevelInfo, fmt.Sprintf(format, args...))
}
func (l *jobLogger) Warnf(format string, args ...any) {
	l.line(LevelWarn, fmt.Sprintf(format, args...))
}
func (l *jobLogger) Errorf(format string, args ...any) {
	l.line(LevelError, fmt.Sprintf(format, args...))
}

// Write splits p into lines; a trailing partial line waits for its newline
// (or the next Flush).
func (l *jobLogger) Write(p []byte) (int, error) {
	l.mu.Lock()
	l.partial.Write(p)
	var lines []string
	for {
		b := l.partial.Bytes()
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, string(b[:i]))
		l.partial.Next(i + 1)
	}
	l.mu.Unlock()
	for _, s := range lines {
		l.line(LevelInfo, s)
	}
	return len(p), nil
}

// Flush writes a pending partial line.
func (l *jobLogger) Flush() {
	l.mu.Lock()
	rest := l.partial.String()
	l.partial.Reset()
	l.mu.Unlock()
	if rest != "" {
		l.line(LevelInfo, rest)
	}
}

func (l *jobLogger) line(level, s string) {
	s = clipLine(strings.TrimRight(s, "\r\n"), l.r.opts.MaxLineBytes)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	if err := l.r.appendLog(context.Background(), l.job, l.seq, l.r.now(), level, s,
		int64(l.r.opts.LogHead), int64(l.r.opts.LogTail)); err != nil {
		l.r.opts.Logger.Warn("job log write failed", "job", l.job, "error", err.Error())
	}
}

// lineCut marks a line clipLine shortened.
const lineCut = " [line cut]"

// clipLine makes s valid UTF-8 (a TEXT column in a UTF-8 PostgreSQL
// database refuses anything else) and keeps at most max bytes of it, cut at
// a rune boundary, before the marker.
func clipLine(s string, max int) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	if len(s) <= max {
		return s
	}
	keep := max
	for keep > 0 && !utf8.RuneStart(s[keep]) {
		keep--
	}
	return s[:keep] + lineCut
}
