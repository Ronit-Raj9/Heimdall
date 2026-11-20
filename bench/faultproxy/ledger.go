package faultproxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// HashEntry returns the hash of a ledger entry: the sha256 of its canonical
// JSON with Hash empty. PrevHash is part of the hashed content, which is what
// chains the entries of a slot.
func HashEntry(e schemas.LedgerEntry) (string, error) {
	e.Hash = ""
	h, err := utils.HashJSON(e)
	if err != nil {
		return "", fmt.Errorf("hash ledger entry: %w", err)
	}
	return h, nil
}

// ReadLedger parses a ledger, one JSON entry per line. Blank lines are
// skipped; any other unparsable line is an error.
func ReadLedger(r io.Reader) ([]schemas.LedgerEntry, error) {
	br := bufio.NewReader(r)
	var out []schemas.LedgerEntry
	for n := 1; ; n++ {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var e schemas.LedgerEntry
			if jerr := json.Unmarshal(line, &e); jerr != nil {
				return nil, fmt.Errorf("ledger line %d: %w", n, jerr)
			}
			out = append(out, e)
		}
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read ledger line %d: %w", n, err)
		}
	}
}

// VerifyChain checks a whole ledger: every entry's hash matches its content,
// the first entry of each slot has an empty PrevHash, and every later entry of
// the slot points at the one before it. Any edit, insertion, deletion or
// reordering within a slot fails.
func VerifyChain(entries []schemas.LedgerEntry) error {
	last := map[string]string{}
	for i, e := range entries {
		h, err := HashEntry(e)
		if err != nil {
			return fmt.Errorf("entry %d: %w", i+1, err)
		}
		if h != e.Hash {
			return fmt.Errorf("entry %d (slot %q): hash mismatch", i+1, e.Slot)
		}
		if e.PrevHash != last[e.Slot] {
			return fmt.Errorf("entry %d (slot %q): chain broken", i+1, e.Slot)
		}
		last[e.Slot] = e.Hash
	}
	return nil
}

// VerifyFile reads the ledger at path and verifies its chain.
func VerifyFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open ledger %s: %w", path, err)
	}
	defer f.Close()
	entries, err := ReadLedger(f)
	if err != nil {
		return fmt.Errorf("ledger %s: %w", path, err)
	}
	if err := VerifyChain(entries); err != nil {
		return fmt.Errorf("ledger %s: %w", path, err)
	}
	return nil
}

// ledger appends hash-chained entries to one JSONL file. One mutex covers
// hashing, writing and fsync, so the chain order is the file order.
type ledger struct {
	mu   sync.Mutex
	path string
	f    *os.File
	last map[string]string // slot -> hash of its latest entry
	// broken is set when a write or fsync fails. The durable tail is then
	// unknown (a partial line, or a line whose hash last does not hold), so
	// every later append refuses until a restart re-verifies the file.
	broken error
}

// openLedger opens or creates the ledger at path. An existing ledger is
// verified first and its chains continue, so a restart never forks a chain;
// a broken ledger refuses to open rather than hide the break.
func openLedger(path string) (*ledger, error) {
	if path == "" {
		return nil, errors.New("ledger path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create ledger dir for %s: %w", path, err)
	}
	// The append handle is opened and locked first, so the verified content
	// below cannot change under a second proxy writing the same file. The
	// lock lives as long as the handle (until close).
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open ledger %s for append: %w", path, err)
	}
	if err := lockLedger(path, f); err != nil {
		f.Close()
		return nil, err
	}
	last, err := loadLedgerTail(path, f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return &ledger{path: path, f: f, last: last}, nil
}

// loadLedgerTail verifies the locked ledger and returns each slot's latest
// hash. A final record without its newline (a crash between the object and
// the delimiter) is repaired by writing the newline, but only after the whole
// file, that record included, decoded and verified.
func loadLedgerTail(path string, f *os.File) (map[string]string, error) {
	last := map[string]string{}
	entries, err := ReadLedger(io.NewSectionReader(f, 0, 1<<62))
	if err != nil {
		return nil, fmt.Errorf("ledger %s: %w", path, err)
	}
	if err := VerifyChain(entries); err != nil {
		return nil, fmt.Errorf("ledger %s: %w", path, err)
	}
	for _, e := range entries {
		last[e.Slot] = e.Hash
	}
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat ledger %s: %w", path, err)
	}
	if st.Size() > 0 {
		b := make([]byte, 1)
		if _, err := f.ReadAt(b, st.Size()-1); err != nil {
			return nil, fmt.Errorf("read ledger %s tail: %w", path, err)
		}
		if b[0] != '\n' {
			if _, err := f.Write([]byte{'\n'}); err != nil {
				return nil, fmt.Errorf("repair ledger %s tail: %w", path, err)
			}
			if err := f.Sync(); err != nil {
				return nil, fmt.Errorf("sync ledger %s tail repair: %w", path, err)
			}
		}
	}
	return last, nil
}

// append chains e to its slot, writes it as one line and fsyncs. It fills
// e.PrevHash and e.Hash.
func (l *ledger) append(e *schemas.LedgerEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.broken != nil {
		return fmt.Errorf("ledger %s refuses appends after an earlier failure: %w", l.path, l.broken)
	}
	e.PrevHash = l.last[e.Slot]
	h, err := HashEntry(*e)
	if err != nil {
		return err
	}
	e.Hash = h
	line, err := utils.CanonicalJSON(e)
	if err != nil {
		return fmt.Errorf("encode ledger entry: %w", err)
	}
	if _, err := l.f.Write(append(line, '\n')); err != nil {
		l.broken = fmt.Errorf("write ledger %s: %w", l.path, err)
		return l.broken
	}
	if err := l.f.Sync(); err != nil {
		l.broken = fmt.Errorf("sync ledger %s: %w", l.path, err)
		return l.broken
	}
	l.last[e.Slot] = h
	return nil
}

// forRun returns every entry recorded for runID, in file order. It reads the
// file rather than a cache so the answer is exactly what is on disk.
func (l *ledger) forRun(runID string) ([]schemas.LedgerEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path)
	if err != nil {
		return nil, fmt.Errorf("open ledger %s: %w", l.path, err)
	}
	defer f.Close()
	all, err := ReadLedger(f)
	if err != nil {
		return nil, fmt.Errorf("ledger %s: %w", l.path, err)
	}
	out := []schemas.LedgerEntry{}
	for _, e := range all {
		if e.RunID == runID {
			out = append(out, e)
		}
	}
	return out, nil
}

func (l *ledger) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("close ledger %s: %w", l.path, err)
	}
	return nil
}
