package toolproxy

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
)

// MaxRecorded caps the arguments and the result kept on one ledger row. The
// sha256 on the row always covers the whole value.
const MaxRecorded = 64 << 10

// maxLedgerLine bounds one line when an existing ledger is read back.
const maxLedgerLine = 4 << 20

// Ledger appends one schemas.ToolCall JSON line per call to tools.jsonl. One
// mutex covers numbering, writing and fsync, so Seq order is file order and a
// row is on disk before the next one is numbered.
type Ledger struct {
	mu   sync.Mutex
	path string
	f    *os.File
	seq  int
	err  error // the last write error, reported by Healthy
}

// OpenLedger opens or creates the ledger at path. An existing ledger is read
// first and numbering continues after its highest Seq, so a restarted proxy
// never reuses a number. Unparsable lines in it are skipped: a proxy killed
// mid-write leaves a torn last line and the rest is still good.
func OpenLedger(path string) (*Ledger, error) {
	if path == "" {
		return nil, errors.New("ledger path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create ledger dir for %s: %w", path, err)
	}
	seq := 0
	if existing, err := os.Open(path); err == nil {
		calls, _ := ReadLedger(existing)
		cerr := existing.Close()
		if cerr != nil {
			return nil, fmt.Errorf("close ledger %s: %w", path, cerr)
		}
		for _, c := range calls {
			seq = max(seq, c.Seq)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("open ledger %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open ledger %s for append: %w", path, err)
	}
	if err := endTornLine(path, f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Ledger{path: path, f: f, seq: seq}, nil
}

// Append numbers c, writes it as one line and fsyncs. It sets c.Seq.
func (l *Ledger) Append(c *schemas.ToolCall) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	c.Seq = l.seq + 1
	line, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode ledger row %d: %w", c.Seq, err)
	}
	if _, err := l.f.Write(append(line, '\n')); err != nil {
		l.err = err
		return fmt.Errorf("write ledger %s: %w", l.path, err)
	}
	if err := l.f.Sync(); err != nil {
		l.err = err
		return fmt.Errorf("sync ledger %s: %w", l.path, err)
	}
	l.seq = c.Seq
	l.err = nil
	return nil
}

// Healthy returns the last write error, nil when the last write worked.
func (l *Ledger) Healthy() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

// Close closes the ledger file.
func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("close ledger %s: %w", l.path, err)
	}
	return nil
}

// ReadLedger parses a tools.jsonl, one schemas.ToolCall per line, and returns
// the rows in file order and the number of non-blank lines that did not
// parse. It never fails on content, only on a read error.
func ReadLedger(r io.Reader) ([]schemas.ToolCall, int) {
	calls := []schemas.ToolCall{}
	bad := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLedgerLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var c schemas.ToolCall
		if err := json.Unmarshal(line, &c); err != nil || c.Seq < 1 {
			bad++
			continue
		}
		calls = append(calls, c)
	}
	if sc.Err() != nil {
		bad++
	}
	return calls, bad
}

// endTornLine ends a last line that has no newline, so the next row starts a
// line of its own and the torn one stays a single unparsable line.
func endTornLine(path string, f *os.File) error {
	rf, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open ledger %s: %w", path, err)
	}
	defer rf.Close()
	st, err := rf.Stat()
	if err != nil || st.Size() == 0 {
		return nil
	}
	last := make([]byte, 1)
	if _, err := rf.ReadAt(last, st.Size()-1); err != nil || last[0] == '\n' {
		return nil
	}
	if _, err := f.Write([]byte{'\n'}); err != nil {
		return fmt.Errorf("end torn line of ledger %s: %w", path, err)
	}
	return nil
}

// Capture returns the text kept on a ledger row for value and the sha256 of
// the whole value. Text over MaxRecorded bytes is cut on a rune boundary.
func Capture(value []byte) (string, string) {
	sum := sha256.Sum256(value)
	return clip(value, MaxRecorded), hex.EncodeToString(sum[:])
}

// clip returns at most n bytes of b as a string, cut on a rune boundary.
func clip(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(b[cut]) {
		cut--
	}
	return string(b[:cut])
}
