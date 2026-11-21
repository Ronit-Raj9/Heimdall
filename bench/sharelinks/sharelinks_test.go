package sharelinks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/reportcard"
)

func days(n int) *int { return &n }

func TestStoreLifecycle(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	st := Open(root).WithClock(func() time.Time { return now })

	sh, err := st.Create(NewShare{Kind: KindRunTrace, Target: "run_1", ExpiresInDays: days(1)}, "usr_1")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := st.Token("org_a", sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := st.Verify("org_a", tok); err != nil || got.ID != sh.ID {
		t.Fatalf("verify %v %v", got, err)
	}
	if _, err := st.Verify("org_b", tok); !errors.Is(err, ErrBadToken) {
		t.Fatalf("another org: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(root, ".heimdall", "share.key")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file %v %v", fi, err)
	}

	later := st.WithClock(func() time.Time { return now.Add(25 * time.Hour) })
	if _, err := later.Verify("org_a", tok); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := st.Revoke(sh.ID, "usr_2"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Verify("org_a", tok); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked: %v", err)
	}

	never, err := st.Create(NewShare{Kind: KindLeaderboard, Target: "agents", Query: "model=gpt-5&capability=coding", ExpiresInDays: days(0)}, "usr_1")
	if err != nil || never.ExpiresAt != nil || never.Query != "capability=coding&model=gpt-5" {
		t.Fatalf("never %+v %v", never, err)
	}
	// Deleting the key revokes every link.
	tok2, _ := st.Token("org_a", never.ID)
	if err := os.Remove(filepath.Join(root, ".heimdall", "share.key")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Verify("org_a", tok2); !errors.Is(err, ErrBadToken) {
		t.Fatalf("after key removal: %v", err)
	}
}

func TestValidate(t *testing.T) {
	bad := []NewShare{
		{Kind: "nope", Target: "x"},
		{Kind: KindVerdict, Target: "../x"},
		{Kind: KindLeaderboard, Target: "secret"},
		{Kind: KindLeaderboard, Target: "models", Query: "split=heldout"},
		{Kind: KindVerdict, Target: "exp_1", Query: "a=b"},
		{Kind: KindVerdict, Target: "exp_1", ExpiresInDays: days(400)},
		{Kind: KindVerdict, Target: "exp_1", Label: strings.Repeat("x", 200)},
	}
	for _, n := range bad {
		if _, err := n.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: want invalid, got %v", n, err)
		}
	}
}

func TestRedact(t *testing.T) {
	in := map[string]any{
		"agent":          "codex",
		"api_key":        "plain",
		"OPENAI_API_KEY": "x",
		"created_by":     "usr_1",
		"tokens":         12.0,
		"note":           "key sk-abcdefghijklmnopqrstuvwx used",
		"events":         []any{map[string]any{"summary": "ran ls", "tool_name": "bash", "target": "/etc"}},
	}
	out := Redact(in, false).(map[string]any)
	if out["api_key"] != "[redacted]" || out["OPENAI_API_KEY"] != "[redacted]" || out["created_by"] != "[redacted]" {
		t.Fatalf("secret keys %v", out)
	}
	if out["tokens"] != 12.0 || out["agent"] != "codex" {
		t.Fatalf("plain values changed %v", out)
	}
	if strings.Contains(out["note"].(string), "sk-abc") {
		t.Fatalf("key shape kept %v", out["note"])
	}
	ev := out["events"].([]any)[0].(map[string]any)
	if ev["summary"] != "ran ls" {
		t.Fatalf("prompts hidden without asking %v", ev)
	}
	hid := Redact(in, true).(map[string]any)["events"].([]any)[0].(map[string]any)
	if hid["summary"] != Hidden || hid["target"] != Hidden || hid["tool_name"] != "bash" {
		t.Fatalf("hide prompts %v", hid)
	}
}

func testReport() *reportcard.Report {
	return &reportcard.Report{
		Evaluation: "ev_1", Mode: "score",
		Arms: []reportcard.Arm{{Label: "A", Agent: "codex", Version: 2, Model: "gpt-5"}, {Label: "B", Agent: "codex", Version: 2, Model: "gpt-5"}},
		Benchmarks: []reportcard.Benchmark{{Benchmark: "tb2", Capability: "terminal", Arms: map[string]reportcard.Metrics{
			"A": {Score: 0.75, CILow: 0.5, CIHigh: 0.9, Runs: 8, CostUSD: 1.2, WallS: 75},
			"B": {Score: 0.5, Runs: 8},
		}}},
	}
}

func TestRenderAndBadge(t *testing.T) {
	r := testReport()
	v := Visible(r)
	if len(v.Arms) != 1 || len(v.Benchmarks[0].Arms) != 1 {
		t.Fatalf("score mode keeps one arm: %+v", v)
	}
	doc := ReportDoc{Report: r, Generated: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)}
	md := doc.Markdown()
	for _, want := range []string{"| tb2 | 75% | 50% to 90% | 8 | $1.20 | 1m 15s |", "## By benchmark", "codex v2 on gpt-5"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	h := doc.HTML()
	if !strings.Contains(h, "@media print") || strings.Contains(h, "<script") || !strings.Contains(h, "<td>75%</td>") {
		t.Fatalf("html %s", h)
	}
	if strings.ContainsAny(md+h, "\u2014\u2013") {
		t.Fatal("em or en dash in export")
	}
	b := ReportBadge(r, "codex")
	if !strings.HasPrefix(b, "<svg") || !strings.Contains(b, "75%") || !strings.Contains(b, badgeGood) {
		t.Fatalf("badge %s", b)
	}
	if b := ReportBadge(&reportcard.Report{}, ""); !strings.Contains(b, "no runs yet") {
		t.Fatalf("empty badge %s", b)
	}
	if b := VerdictBadge("ship", "improved"); !strings.Contains(b, ">ship<") {
		t.Fatalf("verdict badge %s", b)
	}
	if b := Badge("a<b", "c&d", ""); strings.Contains(b, "a<b") {
		t.Fatal("badge text not escaped")
	}
}
