package evidence

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// zeroTime is a timestamp for events whose time does not matter.
var zeroTime time.Time

// testRC returns a run context with ids and a fresh run folder.
func testRC(t *testing.T) *schemas.RunContext {
	t.Helper()
	return &schemas.RunContext{
		Run:    &schemas.Run{RunID: "r_1", SlotID: "s_1", PairID: "p_1", ExpID: "e_1"},
		Slot:   &schemas.Slot{SlotID: "s_1", PairID: "p_1", ExpID: "e_1"},
		Dir:    t.TempDir(),
		Logger: schemas.NoopLogger{},
	}
}

func TestBuiltinOrder(t *testing.T) {
	got := Builtin(Deps{})
	want := []string{ProxyLedger, GatewaySource, AgentStream, ShimLog, Cost, ToolLedger}
	if len(got) != len(want) {
		t.Fatalf("Builtin returned %d sources, want %d", len(got), len(want))
	}
	for i, s := range got {
		t.Run(want[i], func(t *testing.T) {
			if s.GetName() != want[i] {
				t.Errorf("source %d is %q, want %q", i, s.GetName(), want[i])
			}
			wantVersion := "1.0.0"
			if s.GetName() == AgentStream {
				wantVersion = "1.1.0"
			}
			if s.GetVersion() != wantVersion {
				t.Errorf("version %q, want %s", s.GetVersion(), wantVersion)
			}
		})
	}
}

func TestEventSeq(t *testing.T) {
	tests := []struct {
		name   string
		rc     *schemas.RunContext
		wantID string
		slot   string
	}{
		{name: "run ids", rc: &schemas.RunContext{Run: &schemas.Run{RunID: "r_9", SlotID: "s_9", PairID: "p_9"}}, wantID: "r_9:gateway:1", slot: "s_9"},
		{name: "slot fallback", rc: &schemas.RunContext{Run: &schemas.Run{RunID: "r_9"}, Slot: &schemas.Slot{SlotID: "s_8", PairID: "p_8"}}, wantID: "r_9:gateway:1", slot: "s_8"},
		{name: "nil context", rc: nil, wantID: ":gateway:1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newEventSeq(tt.rc, schemas.SourceGateway, schemas.EvidenceGateway)
			e := s.event(schemas.EventModelRequest, zeroTime)
			if e.EventID != tt.wantID || e.Seq != 1 || e.SlotID != tt.slot {
				t.Errorf("event = %q seq %d slot %q, want %q seq 1 slot %q", e.EventID, e.Seq, e.SlotID, tt.wantID, tt.slot)
			}
			if next := s.event(schemas.EventModelResponse, zeroTime); next.Seq != 2 {
				t.Errorf("second seq = %d, want 2", next.Seq)
			}
		})
	}
}

func TestWriteHelpers(t *testing.T) {
	t.Run("no run folder writes nothing", func(t *testing.T) {
		if err := writeJSONL("", "x.jsonl", []int{1}); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON("", "x.json", 1); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("jsonl one value per line", func(t *testing.T) {
		dir := t.TempDir()
		if err := writeJSONL(dir, "x.jsonl", []map[string]int{{"a": 1}, {"b": 2}}); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dir, "x.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != "{\"a\":1}\n{\"b\":2}\n" {
			t.Errorf("file = %q", b)
		}
		if _, err := os.Stat(filepath.Join(dir, "x.jsonl.tmp")); !os.IsNotExist(err) {
			t.Errorf("temporary file left behind: %v", err)
		}
	})
}

func TestClip(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"abcdef", 3, "abc"},
		{"héllo", 2, "h"}, // never splits the two-byte é
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := clip(tt.in, tt.n); got != tt.want {
				t.Errorf("clip(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
			}
		})
	}
}

// TestBuiltinCollectAll runs every source in Builtin order against fakes and
// checks what the later ones read from the earlier ones.
func TestBuiltinCollectAll(t *testing.T) {
	rc := testRC(t)
	rc.Key = &schemas.RunKey{ID: "vk_1"}
	stdout := filepath.Join(rc.Dir, "agent.stream.jsonl")
	if err := os.WriteFile(stdout, []byte("All tests pass.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc.Exec = &schemas.ExecResult{StdoutPath: stdout, Ended: t0}
	g := &fakeGateway{log: requestLogFixture()}
	deps := Deps{Gateway: g, Fault: &fakeFault{entries: ledgerFixture(t)}, Prices: testPrices(t)}
	ev := &schemas.Evidence{}
	for _, src := range Builtin(deps) {
		if err := src.Collect(context.Background(), rc, ev); err != nil {
			t.Fatalf("%s: %v", src.GetName(), err)
		}
	}
	if len(g.expect.LedgerEntries) != 3 {
		t.Errorf("gateway saw %d ledger entries, want the 3 the proxy source collected", len(g.expect.LedgerEntries))
	}
	// 1 ledger fault + 16 gateway + 1 claim.
	if len(ev.Events) != 18 || ev.Claim == nil || !ev.Claim.Made || len(ev.Cost) != 6 {
		t.Fatalf("events %d, claim %+v, cost rows %d", len(ev.Events), ev.Claim, len(ev.Cost))
	}
	b, err := os.ReadFile(filepath.Join(rc.Dir, eventsFile))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []schemas.Event
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		var e schemas.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		onDisk = append(onDisk, e)
	}
	if len(onDisk) != len(ev.Events) {
		t.Fatalf("%s has %d events, want %d", eventsFile, len(onDisk), len(ev.Events))
	}
	priced := 0
	for _, e := range onDisk {
		if e.Type == schemas.EventModelResponse && e.USD != nil {
			priced++
		}
	}
	if priced != 4 {
		t.Errorf("%d model_response events carry usd on disk, want 4", priced)
	}
	for _, name := range []string{ledgerFile, requestsFile, rawRequestsFile, costFile} {
		if _, err := os.Stat(filepath.Join(rc.Dir, name)); err != nil {
			t.Errorf("%s not written: %v", name, err)
		}
	}
}
