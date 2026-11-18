package compliance

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestMarkdownSections(t *testing.T) {
	p, err := Build(Input{Experiments: []ExperimentInput{fixture("/runs")}, RunsRoot: "/runs", BenchVersion: "test", Now: time.Unix(0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	md := string(Markdown(p))
	for _, want := range []string{
		"# Compliance evidence pack",
		"## Experiments",
		"## Tests executed",
		"### Evidence per test",
		"## NIST AI RMF, pack level",
		"## Record-keeping (EU AI Act Art. 12 and 26)",
		"## Ids cited and their sources",
		"## What this pack does not certify",
		"LLM01:2025",
		"ASI01*",
		"e_test/slots/s_4/runs/r_4",
		"Unmapped tests (no framework id): gate:brand_new_gate",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
	if strings.ContainsAny(md, "—–") {
		t.Error("markdown must use plain hyphens, not em or en dashes")
	}
	if string(Markdown(p)) != md {
		t.Error("markdown is not deterministic")
	}
}

func TestMarkdownEvidenceCap(t *testing.T) {
	ex := fixture("/runs")
	base := ex.Runs[0]
	for i := 0; i < maxEvidenceRows+3; i++ {
		r := base
		r.Run.RunID = fmt.Sprintf("r_x%02d", i)
		ex.Runs = append(ex.Runs, r)
	}
	p, err := Build(Input{Experiments: []ExperimentInput{ex}, RunsRoot: "/runs"})
	if err != nil {
		t.Fatal(err)
	}
	if md := string(Markdown(p)); !strings.Contains(md, "more runs are listed in the JSON pack") {
		t.Error("evidence over the cap must point at the JSON pack")
	}
}

func TestCellAndShort(t *testing.T) {
	cases := []struct{ in, cell string }{
		{"a|b", `a\|b`},
		{"two\nlines", "two lines"},
	}
	for _, c := range cases {
		if got := cell(c.in); got != c.cell {
			t.Errorf("cell(%q) = %q, want %q", c.in, got, c.cell)
		}
	}
	if got := short("sha256:0123456789abcdef"); got != "0123456789ab" {
		t.Errorf("short = %q", got)
	}
	if got := short(""); got != "-" {
		t.Errorf("short(empty) = %q", got)
	}
	if got := ids([]schemas.ComplianceMapping{{Framework: schemas.FrameworkOWASPAgentic2026, ID: "ASI01"}}, schemas.FrameworkOWASPAgentic2026); got != "ASI01*" {
		t.Errorf("ids = %q", got)
	}
}
