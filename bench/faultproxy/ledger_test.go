package faultproxy

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// writeLedger appends n entries alternating between two slots and returns
// the file path.
func writeLedger(t *testing.T, n int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sub", "ledger.jsonl")
	l, err := openLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range n {
		slot := []string{"slot-a", "slot-b"}[i%2]
		e := &schemas.LedgerEntry{
			Slot: slot, RunID: "r-" + slot, Call: i/2 + 1, Kind: KindMessage, Path: "/v1/messages",
			Action: ActionPass, Upstream: schemas.LedgerUpstreamReal, Status: 200, ReqSHA256: "abc", UpstreamMS: 1.25,
			Tokens: &schemas.Tokens{Fresh: int64(i), Out: 32}, At: time.Unix(1_700_000_000, int64(i)*1001).UTC(),
		}
		if err := l.append(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLedgerChainVerifies(t *testing.T) {
	path := writeLedger(t, 6)
	if err := VerifyFile(path); err != nil {
		t.Fatalf("VerifyFile: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	entries, err := ReadLedger(f)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].PrevHash != "" || entries[1].PrevHash != "" {
		t.Errorf("first entry of each slot must have an empty prev_hash")
	}
	if entries[2].PrevHash != entries[0].Hash || entries[3].PrevHash != entries[1].Hash {
		t.Errorf("entries are not chained per slot")
	}
}

func TestLedgerDetectsTampering(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(lines []string) []string
	}{
		{"edited field", func(l []string) []string {
			l[2] = strings.Replace(l[2], `"status":200`, `"status":503`, 1)
			return l
		}},
		{"edited tokens", func(l []string) []string {
			l[3] = strings.Replace(l[3], `"out":32`, `"out":1`, 1)
			return l
		}},
		{"edited upstream", func(l []string) []string {
			l[4] = strings.Replace(l[4], `"upstream":"real"`, `"upstream":"scripted"`, 1)
			return l
		}},
		{"deleted line", func(l []string) []string { return append(l[:2], l[3:]...) }},
		{"swapped lines of one slot", func(l []string) []string {
			l[0], l[2] = l[2], l[0]
			return l
		}},
		{"rehashed edit without chain", func(l []string) []string {
			// recomputing one entry's own hash is not enough: its successor
			// still points at the old hash
			entries, err := ReadLedger(strings.NewReader(l[2]))
			if err != nil {
				panic(err)
			}
			entries[0].Status = 503
			h, err := HashEntry(entries[0])
			if err != nil {
				panic(err)
			}
			entries[0].Hash = h
			line, err := canonicalLine(entries[0])
			if err != nil {
				panic(err)
			}
			l[2] = line
			return l
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeLedger(t, 6)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
			out := strings.Join(tc.mutate(lines), "\n") + "\n"
			if out == string(data) {
				t.Fatal("mutation changed nothing")
			}
			if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := VerifyFile(path); err == nil {
				t.Fatal("tampered ledger verified")
			}
			if _, err := openLedger(path); err == nil {
				t.Fatal("tampered ledger opened for append")
			}
		})
	}
}

func TestLedgerReopenContinuesChain(t *testing.T) {
	path := writeLedger(t, 3)
	l, err := openLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.append(&schemas.LedgerEntry{Slot: "slot-a", RunID: "r-slot-a", Kind: KindModels, Action: ActionPass}); err != nil {
		t.Fatal(err)
	}
	if err := l.close(); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFile(path); err != nil {
		t.Fatalf("chain broken across reopen: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("ledger mode %v, want 0600", info.Mode().Perm())
	}
}

// olderLedgerLine is a line a proxy without the upstream field wrote, copied
// from testdata/recorded.
const olderLedgerLine = `{"slot":"fp-slot-1","run_id":"r_nnfsjc6is2","call":1,"kind":"message","path":"/v1/messages","stream":false,"action":"pass","status":200,"req_sha256":"d6f2d34bab6649bf5d681c021defde0129b7a5252bdf51ef971fbf81e129fb53","response_id":"msg_56qkt85cuadskjjrp8koshjo","tokens":{"fresh":619,"cache_read":0,"cache_write":0,"out":32},"upstream_ms":0.042,"at":"2026-11-13T23:29:10.498606021Z","prev_hash":"18ecc694499424fa51969bd4eb95afc9b75dd094bd7841bb83791910dbfd1ed9","hash":"23d45beadabb0b169a5d436a67d75f7aa9c8b2be5208274a7853fe5264040ad8"}`

// TestLedgerOlderLinesStillVerify checks that adding the upstream field kept
// older ledgers valid: an older line hashes to its recorded hash and
// re-encodes byte for byte, and a ledger of unlabelled lines reopens and
// chains labelled ones after them.
func TestLedgerOlderLinesStillVerify(t *testing.T) {
	entries, err := ReadLedger(strings.NewReader(olderLedgerLine))
	if err != nil {
		t.Fatal(err)
	}
	e := entries[0]
	if e.Upstream != "" {
		t.Fatalf("older line upstream = %q, want empty", e.Upstream)
	}
	h, err := HashEntry(e)
	if err != nil {
		t.Fatal(err)
	}
	if h != e.Hash {
		t.Errorf("older line hash %s, recorded %s", h, e.Hash)
	}
	line, err := canonicalLine(e)
	if err != nil {
		t.Fatal(err)
	}
	if line != olderLedgerLine {
		t.Errorf("older line re-encodes differently:\n%s", line)
	}

	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	l, err := openLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.append(&schemas.LedgerEntry{Slot: "slot-a", RunID: "r1", Kind: KindMessage, Action: ActionPass}); err != nil {
		t.Fatal(err)
	}
	if err := l.close(); err != nil {
		t.Fatal(err)
	}
	if l, err = openLedger(path); err != nil {
		t.Fatalf("reopen unlabelled ledger: %v", err)
	}
	if err := l.append(&schemas.LedgerEntry{Slot: "slot-a", RunID: "r1", Kind: KindMessage, Action: ActionPass,
		Upstream: schemas.LedgerUpstreamScripted}); err != nil {
		t.Fatal(err)
	}
	if err := l.close(); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFile(path); err != nil {
		t.Fatalf("mixed ledger: %v", err)
	}
}

func TestLedgerForRunAndConcurrency(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	l, err := openLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := l.close(); err != nil {
			t.Error(err)
		}
	})
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run := []string{"r1", "r2"}[i%2]
			if err := l.append(&schemas.LedgerEntry{Slot: "s-" + run, RunID: run, Kind: KindMessage, Action: ActionPass}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := l.forRun("r1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 {
		t.Fatalf("forRun(r1) = %d entries, want 20", len(got))
	}
	none, err := l.forRun("missing")
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("forRun(missing) = %v, %v; want empty non-nil", none, err)
	}
	if err := VerifyFile(path); err != nil {
		t.Fatalf("concurrent appends broke the chain: %v", err)
	}
}

func TestReadLedgerRejectsGarbage(t *testing.T) {
	if _, err := ReadLedger(bytes.NewReader([]byte("{\"slot\":\"a\"}\nnot json\n"))); err == nil {
		t.Fatal("garbage line accepted")
	}
	got, err := ReadLedger(bytes.NewReader([]byte("\n{\"slot\":\"a\"}\n\n")))
	if err != nil || len(got) != 1 {
		t.Fatalf("blank lines: %v, %v", got, err)
	}
}

func canonicalLine(e schemas.LedgerEntry) (string, error) {
	line, err := utils.CanonicalJSON(e)
	return string(line), err
}

// TestLedgerRepairsUnterminatedTail covers a crash between the final object
// and its newline: reopen repairs the delimiter so the next append stays JSONL.
func TestLedgerRepairsUnterminatedTail(t *testing.T) {
	path := writeLedger(t, 2)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.TrimRight(raw, "\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := openLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.append(&schemas.LedgerEntry{Slot: "slot-a", RunID: "r", Kind: KindModels, Action: ActionPass}); err != nil {
		t.Fatal(err)
	}
	if err := l.close(); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFile(path); err != nil {
		t.Fatalf("append after unterminated tail: %v", err)
	}
}

// TestLedgerRefusesSecondWriter covers two proxies on one ledger path.
func TestLedgerRefusesSecondWriter(t *testing.T) {
	path := writeLedger(t, 1)
	l, err := openLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openLedger(path); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("second opener: %v, want refusal naming the path", err)
	}
	if err := l.close(); err != nil {
		t.Fatal(err)
	}
	l2, err := openLedger(path)
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	l2.close()
}

// TestLedgerStopsAfterWriteFailure: once a write fails the tail is unknown,
// so later appends refuse instead of chaining onto a possibly lost entry.
func TestLedgerStopsAfterWriteFailure(t *testing.T) {
	path := writeLedger(t, 1)
	l, err := openLedger(path)
	if err != nil {
		t.Fatal(err)
	}
	l.f.Close() // force the next write to fail
	if err := l.append(&schemas.LedgerEntry{Slot: "slot-a", Kind: KindModels, Action: ActionPass}); err == nil {
		t.Fatal("write to closed file succeeded")
	}
	err = l.append(&schemas.LedgerEntry{Slot: "slot-a", Kind: KindModels, Action: ActionPass})
	if err == nil || !strings.Contains(err.Error(), "refuses appends") {
		t.Fatalf("append after failure: %v", err)
	}
}
