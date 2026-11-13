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
	last := map[string]string{}
	existing, err := os.Open(path)
	switch {
	case err == nil:
		entries, rerr := ReadLedger(existing)
		cerr := existing.Close()
		if rerr != nil {
			return nil, fmt.Errorf("ledger %s: %w", path, rerr)
		}
		if cerr != nil {
			return nil, fmt.Errorf("close ledger %s: %w", path, cerr)
		}
		if verr := VerifyChain(entries); verr != nil {
			return nil, fmt.Errorf("ledger %s: %w", path, verr)
		}
		for _, e := range entries {
			last[e.Slot] = e.Hash
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("open ledger %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open ledger %s for append: %w", path, err)
	}
	return &ledger{path: path, f: f, last: last}, nil
}

// append chains e to its slot, writes it as one line and fsyncs. It fills
// e.PrevHash and e.Hash.
func (l *ledger) append(e *schemas.LedgerEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
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
		return fmt.Errorf("write ledger %s: %w", l.path, err)
	}
	if err := l.f.Sync(); err != nil {
		return fmt.Errorf("sync ledger %s: %w", l.path, err)
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
