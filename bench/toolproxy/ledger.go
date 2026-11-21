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
//
// Next to the ledger it keeps a status file (LedgerStatusFile) saying how
// many rows were numbered, how many calls are still in flight and whether
// any write failed. The proxy answers a call before its row is written, so
// the ledger alone cannot tell a quiet run from one copied mid-call or one
// whose writes failed; the exporter copies the status with the ledger and
// evidence collection refuses a ledger the status marks incomplete (3-D-08).
type Ledger struct {
	mu       sync.Mutex
	path     string
	f        *os.File
	seq      int
	err      error // the first write error; it stays for the life of the ledger
	failed   int   // rows that could not be written
	inflight int   // calls begun and not yet ended
}

// LedgerStatusFile is the status file kept next to the ledger.
const LedgerStatusFile = schemas.ToolLedgerStatusFile

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
	l := &Ledger{path: path, f: f, seq: seq}
	l.carryStatus()
	l.mu.Lock()
	l.writeStatus()
	l.mu.Unlock()
	return l, nil
}

// carryStatus folds the status a previous proxy left next to the ledger into
// l before the first rewrite, so a restart cannot erase the only witness
// that rows are missing: its failures stay, its numbering is never reused,
// and calls it still had in flight (whose rows can no longer be written)
// count as failed rows.
func (l *Ledger) carryStatus() {
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(l.path), LedgerStatusFile))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var prev schemas.ToolLedgerStatus
	if err == nil {
		err = json.Unmarshal(raw, &prev)
	}
	if err != nil {
		l.fail(fmt.Errorf("read previous ledger status: %w", err))
		return
	}
	l.seq = max(l.seq, prev.Rows)
	l.failed = prev.Failed + prev.InFlight
	if prev.Error != "" {
		l.fail(errors.New(prev.Error))
	}
	if prev.InFlight > 0 {
		l.fail(fmt.Errorf("the proxy restarted with %d calls in flight", prev.InFlight))
	}
}

// Begin marks a call in flight until the returned end is called, after its
// row was appended (or the call made no row).
func (l *Ledger) Begin() (end func()) {
	l.mu.Lock()
	l.inflight++
	l.writeStatus()
	l.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.inflight--
			l.writeStatus()
			l.mu.Unlock()
		})
	}
}

// writeStatus replaces the status file; l.mu is held. A failure to write it
// is itself a ledger fault: the status file would then claim too little.
func (l *Ledger) writeStatus() {
	st := schemas.ToolLedgerStatus{Rows: l.seq, InFlight: l.inflight, Failed: l.failed}
	if l.err != nil {
		st.Error = l.err.Error()
	}
	b, _ := json.Marshal(st)
	dst := filepath.Join(filepath.Dir(l.path), LedgerStatusFile)
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		l.fail(fmt.Errorf("write ledger status: %w", err))
		return
	}
	if err := os.Rename(tmp, dst); err != nil {
		l.fail(fmt.Errorf("write ledger status: %w", err))
	}
}

// fail records the first write error; l.mu is held.
func (l *Ledger) fail(err error) {
	if l.err == nil {
		l.err = err
	}
}

// Append numbers c, writes it as one line and fsyncs. It sets c.Seq.
func (l *Ledger) Append(c *schemas.ToolCall) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	// The number is used even when the write fails, so a lost row leaves a
	// gap in Seq, and the error stays: one lost row makes the whole ledger
	// an incomplete record.
	l.seq++
	c.Seq = l.seq
	err := l.write(c)
	if err != nil {
		l.failed++
		l.fail(err)
	}
	l.writeStatus()
	return err
}

// write writes and fsyncs one row; l.mu is held.
func (l *Ledger) write(c *schemas.ToolCall) error {
	line, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode ledger row %d: %w", c.Seq, err)
	}
	if _, err := l.f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write ledger %s: %w", l.path, err)
	}
	if err := l.f.Sync(); err != nil {
		return fmt.Errorf("sync ledger %s: %w", l.path, err)
	}
	return nil
}

// Healthy returns the first write error, nil while every write worked.
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
