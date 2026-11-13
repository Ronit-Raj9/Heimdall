package bifrost

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

var fixtureRows = []string{
	"json.json", "stream-tools.json", "tools-json.json", "tool-result.json", "count-tokens.json",
	"same-body-pass.json", "retry-ok.json", "retry-fail.json", "list-models.json",
}

func loadRows(t *testing.T, files ...string) []*parsedRow {
	t.Helper()
	var out []*parsedRow
	for _, f := range files {
		p, err := parseDetail(readFixture(t, f))
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		out = append(out, p)
	}
	return out
}

func loadLedger(t *testing.T) []schemas.LedgerEntry {
	t.Helper()
	var l []schemas.LedgerEntry
	if err := json.Unmarshal(readFixture(t, "ledger.json"), &l); err != nil {
		t.Fatalf("ledger: %v", err)
	}
	return l
}

// covered renders which ledger calls each row covers, as "row:action/call".
func covered(j joinResult, rows []*parsedRow) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		var parts []string
		for _, i := range j.Rows[r.Req.ID] {
			parts = append(parts, j.Entries[i].Action+"/"+j.Entries[i].ReqSHA256[:6])
		}
		out[r.Req.ID[:8]] = strings.Join(parts, " ")
	}
	return out
}

func TestJoinLedgerFixtures(t *testing.T) {
	rows := loadRows(t, fixtureRows...)
	ledger := loadLedger(t)
	j := joinLedger(rows, ledger, "slot-b")
	if !j.complete() {
		t.Fatalf("join incomplete: %s", j.reason())
	}
	if len(j.Entries) != 10 {
		t.Errorf("relevant entries = %d, want 10 (models calls excluded)", len(j.Entries))
	}
	got := covered(j, rows)
	want := map[string]string{
		"f89e9e14": "pass/ec700f",               // json
		"ae09522b": "pass/0a8960",               // stream
		"cce852a1": "pass/14d885",               // count_tokens by body hash
		"c7795609": "pass/27aed3",               // same body as retry-ok, first send
		"33e15d33": "inject/27aed3 pass/27aed3", // retried once
		"4b3b1159": "inject/ba19ba inject/ba19ba",
		"e72c6248": "", // list_models never joins
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("row %s covers %q, want %q", id, got[id], w)
		}
	}
	// Row order must not matter.
	rev := append([]*parsedRow{}, rows...)
	slices.Reverse(rev)
	if j2 := joinLedger(rev, ledger, "slot-b"); !j2.complete() || covered(j2, rows)["33e15d33"] != want["33e15d33"] {
		t.Errorf("reversed rows changed the join: %v %s", covered(j2, rows), j2.reason())
	}
}

func TestJoinLedgerIncomplete(t *testing.T) {
	ledger := loadLedger(t)
	tests := []struct {
		name       string
		rows       []string
		ledger     func([]schemas.LedgerEntry) []schemas.LedgerEntry
		unmatched  int
		wantReason string
	}{
		{
			name: "row not written yet", rows: without(fixtureRows, "retry-ok.json"),
			unmatched: 2, wantReason: "2 of 10 ledger entries have no row",
		},
		{
			name: "no rows at all", unmatched: 10, wantReason: "10 of 10 ledger entries have no row",
		},
		{
			name: "attempt missing from the ledger", rows: fixtureRows,
			ledger: func(l []schemas.LedgerEntry) []schemas.LedgerEntry {
				return dropWhere(l, func(e schemas.LedgerEntry) bool { return e.Action == "inject" && e.ReqSHA256[:6] == "27aed3" })
			},
			wantReason: "has 2 attempts but 1 ledger entries",
		},
		{
			name: "row with no ledger entry", rows: fixtureRows,
			ledger: func(l []schemas.LedgerEntry) []schemas.LedgerEntry {
				return dropWhere(l, func(e schemas.LedgerEntry) bool { return e.Kind == "count_tokens" })
			},
			wantReason: "(count_tokens) has no ledger entry",
		},
		{
			name: "duplicate response id", rows: fixtureRows,
			ledger: func(l []schemas.LedgerEntry) []schemas.LedgerEntry {
				dup := l[6]
				dup.Call = 99
				return append(append([]schemas.LedgerEntry{}, l...), dup)
			},
			unmatched: 2, wantReason: "matches 2 ledger entries",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := ledger
			if tt.ledger != nil {
				l = tt.ledger(ledger)
			}
			j := joinLedger(loadRows(t, tt.rows...), l, "slot-b")
			if j.complete() {
				t.Fatal("join reported complete")
			}
			if len(j.Unmatched) != tt.unmatched {
				t.Errorf("unmatched = %d, want %d (%s)", len(j.Unmatched), tt.unmatched, j.reason())
			}
			if !strings.Contains(j.reason(), tt.wantReason) {
				t.Errorf("reason %q lacks %q", j.reason(), tt.wantReason)
			}
		})
	}
}

// TestJoinLedgerBenchProxyNonce runs the join on the bench fault proxy's
// message format: one secret nonce per run, so the call number picks the
// entry. The rows are the captured retry-fail row with its message rewritten.
func TestJoinLedgerBenchProxyNonce(t *testing.T) {
	raw := strings.ReplaceAll(string(readFixture(t, "retry-fail.json")),
		"faultproxy injected 503 on call 2 (nonce l87QjSk5s6tiSBGU)", "Overloaded: the model is temporarily unavailable. ref nonce_run7 call 2")
	p, err := parseDetail([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if p.Nonce != "nonce_run7" || p.NonceCall != 2 {
		t.Fatalf("nonce %q call %d", p.Nonce, p.NonceCall)
	}
	entry := func(call int) schemas.LedgerEntry {
		return schemas.LedgerEntry{Slot: "fp-slot-1", Kind: "message", Call: call, Action: "inject", Status: 503, Nonce: "nonce_run7", ReqSHA256: p.ReqSHA256}
	}
	ledger := []schemas.LedgerEntry{
		entry(1), entry(2),
		{Slot: "fp-slot-1", Kind: "models", Action: "pass", Status: 200},
		{Kind: "message", Action: "reject", Status: 401},
		{Slot: "fp-slot-2", Kind: "message", Call: 1, Action: "pass", Status: 200, ResponseID: "msg_other"},
	}
	j := joinLedger([]*parsedRow{p}, ledger, "fp-slot-1")
	if !j.complete() || len(j.Entries) != 2 || len(j.Rows[p.Req.ID]) != 2 {
		t.Fatalf("join = %+v, %s", j.Rows, j.reason())
	}
	// With call 3 also injected for the same body, only calls 2 and 3 belong
	// to a row whose message names call 3; call 1 stays unmatched.
	p.NonceCall = 3
	j = joinLedger([]*parsedRow{p}, append(ledger, entry(3)), "fp-slot-1")
	if len(j.Unmatched) != 1 || j.Entries[j.Unmatched[0]].Call != 1 {
		t.Errorf("unmatched = %v, %s", j.Unmatched, j.reason())
	}
}

func without(xs []string, drop string) []string {
	var out []string
	for _, x := range xs {
		if x != drop {
			out = append(out, x)
		}
	}
	return out
}

func dropWhere(l []schemas.LedgerEntry, f func(schemas.LedgerEntry) bool) []schemas.LedgerEntry {
	var out []schemas.LedgerEntry
	for _, e := range l {
		if !f(e) {
			out = append(out, e)
		}
	}
	return out
}
