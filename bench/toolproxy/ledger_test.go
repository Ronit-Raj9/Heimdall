package toolproxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestLedgerAppendAndReopen(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		wantSeqs []int
		wantBad  int
	}{
		{name: "fresh file", wantSeqs: []int{1, 2}},
		{name: "continues after highest seq", existing: `{"seq":7,"server":"a","tool":"x","protocol":"mcp","args_sha256":"","result_sha256":"","is_error":false,"started":"2026-11-15T00:00:00Z","latency_ms":1}` + "\n", wantSeqs: []int{7, 8, 9}},
		{name: "torn last line skipped", existing: `{"seq":3,"server":"a","tool":"x","protocol":"mcp","args_sha256":"","result_sha256":"","is_error":false,"started":"2026-11-15T00:00:00Z","latency_ms":1}` + "\n" + `{"seq":4,"ser`, wantSeqs: []int{3, 4, 5}, wantBad: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "sub", "tools.jsonl")
			if tt.existing != "" {
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(tt.existing), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			l, err := OpenLedger(p)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				c := schemas.ToolCall{Server: "fs", Tool: "read", Protocol: ProtocolMCP}
				if err := l.Append(&c); err != nil {
					t.Fatal(err)
				}
			}
			if err := l.Healthy(); err != nil {
				t.Fatal(err)
			}
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			calls, bad := ReadLedger(f)
			var seqs []int
			for _, c := range calls {
				seqs = append(seqs, c.Seq)
			}
			if bad != tt.wantBad {
				t.Errorf("bad lines = %d, want %d", bad, tt.wantBad)
			}
			if len(seqs) != len(tt.wantSeqs) {
				t.Fatalf("seqs = %v, want %v", seqs, tt.wantSeqs)
			}
			for i := range seqs {
				if seqs[i] != tt.wantSeqs[i] {
					t.Fatalf("seqs = %v, want %v", seqs, tt.wantSeqs)
				}
			}
		})
	}
}

func TestOpenLedgerEmptyPath(t *testing.T) {
	if _, err := OpenLedger(""); err == nil {
		t.Fatal("want error for empty path")
	}
}

func TestCapture(t *testing.T) {
	big := strings.Repeat("é", MaxRecorded) // 2 bytes per rune, so the cut lands mid-rune
	tests := []struct {
		name    string
		in      string
		wantLen int
	}{
		{name: "small kept whole", in: `{"a":1}`, wantLen: 7},
		{name: "empty", in: "", wantLen: 0},
		{name: "large cut on rune boundary", in: big, wantLen: MaxRecorded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, sum := Capture([]byte(tt.in))
			full := sha256.Sum256([]byte(tt.in))
			if sum != hex.EncodeToString(full[:]) {
				t.Errorf("hash does not cover the whole value")
			}
			if len(text) != tt.wantLen {
				t.Errorf("kept %d bytes, want %d", len(text), tt.wantLen)
			}
			if !strings.HasPrefix(tt.in, text) {
				t.Errorf("kept text is not a prefix")
			}
		})
	}
}

// TestLedgerStatusAndStickyError checks the ledger status file (3-D-08): it
// counts calls in flight, a failed write leaves a Seq gap, and the error
// stays after a later write succeeds, on Healthy and in the status.
func TestLedgerStatusAndStickyError(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLedger(filepath.Join(dir, "tools.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	status := func() schemas.ToolLedgerStatus {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, LedgerStatusFile))
		if err != nil {
			t.Fatal(err)
		}
		var st schemas.ToolLedgerStatus
		if err := json.Unmarshal(raw, &st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	end := l.Begin()
	if st := status(); st.InFlight != 1 || st.Incomplete() == "" {
		t.Fatalf("in flight status %+v", st)
	}
	if err := l.Append(&schemas.ToolCall{Server: "a", Tool: "x"}); err != nil {
		t.Fatal(err)
	}
	end()
	end() // a second end is a no-op
	if st := status(); st.InFlight != 0 || st.Rows != 1 || st.Incomplete() != "" {
		t.Fatalf("settled status %+v", st)
	}
	good := l.f
	bad, err := os.Open(filepath.Join(dir, "tools.jsonl")) // read-only: writes fail
	if err != nil {
		t.Fatal(err)
	}
	l.f = bad
	if err := l.Append(&schemas.ToolCall{Server: "a", Tool: "y"}); err == nil {
		t.Fatal("write to a read-only file worked")
	}
	_ = bad.Close()
	l.f = good
	c := &schemas.ToolCall{Server: "a", Tool: "z"}
	if err := l.Append(c); err != nil {
		t.Fatal(err)
	}
	if c.Seq != 3 {
		t.Fatalf("seq after a failed row = %d, want 3 (a gap)", c.Seq)
	}
	if l.Healthy() == nil {
		t.Fatal("the write error was cleared by a later write")
	}
	if st := status(); st.Failed != 1 || st.Rows != 3 || st.Incomplete() == "" {
		t.Fatalf("status after a failed row %+v", st)
	}
	_ = l.Close()
}
