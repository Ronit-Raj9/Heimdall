package compliance

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxEvidenceRows bounds the run links printed per test in the Markdown; the
// JSON pack always carries every one.
const maxEvidenceRows = 12

// Markdown renders a pack as a document for a reader without the bench. It
// is pure and deterministic for a given pack.
func Markdown(p *schemas.CompliancePack) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Compliance evidence pack\n\nGenerated %s by %s, pack format %s.\n\n", p.GeneratedAt.Format("2006-01-02 15:04 MST"), p.Generator, p.Version)
	b.WriteString("This pack maps the tests an experiment executed to OWASP and NIST ids and points at the sealed runs behind each row. It certifies nothing; see [What this pack does not certify](#what-this-pack-does-not-certify).\n\n")
	writeExperiments(&b, p)
	writeWarnings(&b, p.Warnings)
	writeTests(&b, p)
	writeFunctions(&b, p.Functions)
	writeRecordKeeping(&b, p.RecordKeeping)
	writeFrameworks(&b, p.Frameworks)
	b.WriteString("## What this pack does not certify\n\n")
	for _, s := range p.NotCertified {
		fmt.Fprintf(&b, "- %s\n", s)
	}
	return []byte(b.String())
}

func writeExperiments(b *strings.Builder, p *schemas.CompliancePack) {
	b.WriteString("## Experiments\n\n| Experiment | Suite | Manifest hash | Counted runs | Seals verified | Chain | Verdict | Mode |\n|---|---|---|---|---|---|---|---|\n")
	for _, e := range p.Experiments {
		verdict := "no analysis"
		if e.AnalysisID != "" {
			verdict = fmt.Sprintf("%s, %s (analysis %s)", e.Decision, e.Action, e.AnalysisID)
		}
		fmt.Fprintf(b, "| %s | %s@%s | `%s` | %d | %d | %s | %s | %s |\n", cell(e.ExpID), cell(e.Suite), cell(e.SuiteVersion),
			short(e.ManifestHash), e.CountedRuns, e.RunsVerified, e.ChainStatus, cell(verdict), orDash(string(e.Mode)))
	}
	for _, e := range p.Experiments {
		if e.SealsLogTail != "" {
			fmt.Fprintf(b, "\n%s seals.log tail: `%s`", e.ExpID, e.SealsLogTail)
		}
	}
	b.WriteString("\n\n")
	if r := p.Release; r != nil {
		fmt.Fprintf(b, "Release %s of %s: %s, %s, approved by %s, record hash `%s`, seals digest `%s`.", r.ReleaseID, r.ExpID, r.Decision, r.Action, cell(r.Approver), short(r.Hash), short(r.SealsDigest))
		if r.Override != "" {
			fmt.Fprintf(b, " Released with an override: %s.", cell(r.Override))
		}
		b.WriteString("\n\n")
	}
}

func writeWarnings(b *strings.Builder, w []string) {
	if len(w) == 0 {
		return
	}
	b.WriteString("## Read first\n\n")
	for _, s := range w {
		fmt.Fprintf(b, "- %s\n", s)
	}
	b.WriteString("\n")
}

func writeTests(b *strings.Builder, p *schemas.CompliancePack) {
	b.WriteString("## Tests executed\n\nA condition row counts counted runs under it and, as fired, the runs where any veto gate fired. A gate row counts the runs it was evaluated on and the runs where it fired. Ids marked * are unverified.\n\n")
	b.WriteString("| Test | Kind | Runs | Fired | OWASP LLM 2025 | OWASP Agentic 2026 | NIST AI RMF | Mapped by |\n|---|---|---|---|---|---|---|---|\n")
	for _, t := range p.Tests {
		fmt.Fprintf(b, "| %s (`%s`) | %s | %d | %d | %s | %s | %s | %s |\n", cell(t.Title), t.Name, kindLabel(t), t.Runs, t.Fired,
			ids(t.Mappings, schemas.FrameworkOWASPLLM2025), ids(t.Mappings, schemas.FrameworkOWASPAgentic2026), ids(t.Mappings, schemas.FrameworkNISTAIRMF), t.MappedBy)
	}
	if len(p.Unmapped) > 0 {
		fmt.Fprintf(b, "\nUnmapped tests (no framework id): %s.\n", strings.Join(p.Unmapped, ", "))
	}
	b.WriteString("\n### Evidence per test\n\nRun folders are relative to the runs root. Each seal hash is the sha256 of the run's seal.json.\n")
	for _, t := range p.Tests {
		fmt.Fprintf(b, "\n#### %s (`%s`)\n\n", cell(t.Title), t.ID)
		for _, m := range t.Mappings {
			if m.Note != "" {
				fmt.Fprintf(b, "Why: %s.\n\n", m.Note)
				break
			}
		}
		if len(t.Evidence) == 0 {
			b.WriteString("No run to link: the gate never fired.\n")
			continue
		}
		b.WriteString("| Run | Task | Arm | Trial | Outcome | Fired | Seal | Folder |\n|---|---|---|---|---|---|---|---|\n")
		for i, e := range t.Evidence {
			if i == maxEvidenceRows {
				fmt.Fprintf(b, "\n%d more runs are listed in the JSON pack.\n", len(t.Evidence)-maxEvidenceRows)
				break
			}
			fmt.Fprintf(b, "| %s | %s | %s | %d | %s | %s | `%s` | `%s` |\n", e.RunID, cell(e.Task), e.Arm, e.Trial, orDash(string(e.Outcome)), yesNo(e.Fired), short(e.SealHash), e.Path)
		}
	}
	b.WriteString("\n")
}

func writeFunctions(b *strings.Builder, fs []schemas.ComplianceFunction) {
	b.WriteString("## NIST AI RMF, pack level\n\n| Subcategory | What this pack offers | What it does not |\n|---|---|---|\n")
	for _, f := range fs {
		fmt.Fprintf(b, "| %s%s | %s | %s |\n", f.ID, star(f.Verified), cell(f.Evidence), cell(f.Gap))
	}
	b.WriteString("\n")
}

func writeRecordKeeping(b *strings.Builder, rk schemas.ComplianceRecordKeeping) {
	b.WriteString("## Record-keeping (EU AI Act Art. 12 and 26)\n\n")
	fmt.Fprintf(b, "Where: %s.\n\nHow long: %s\n\nExternal anchor: %s.\n\n", rk.Location, rk.Retention, yesNo(rk.ExternalAnchor))
	b.WriteString("| File | Holds | Under the run seal |\n|---|---|---|\n")
	for _, a := range rk.Retained {
		fmt.Fprintf(b, "| %s | %s | %s |\n", cell(a.File), cell(a.Holds), yesNo(a.Sealed))
	}
	b.WriteString("\n| Article | Text (paraphrase) | How the bench's records relate |\n|---|---|---|\n")
	for _, a := range rk.Articles {
		fmt.Fprintf(b, "| %s%s | %s | %s |\n", a.ID, star(a.Verified), cell(a.Name), cell(a.Note))
	}
	b.WriteString("\n")
	for _, n := range rk.Notes {
		fmt.Fprintf(b, "- %s\n", n)
	}
	b.WriteString("\n")
}

func writeFrameworks(b *strings.Builder, fs []schemas.ComplianceFramework) {
	b.WriteString("## Ids cited and their sources\n\n| Framework | Id | Name | Verified | Source |\n|---|---|---|---|---|\n")
	for _, f := range fs {
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s |\n", f.Framework, f.ID, cell(f.Name), yesNo(f.Verified), f.Source)
	}
	b.WriteString("\n")
}

// ids lists a test's ids in one framework, unverified ones starred.
func ids(ms []schemas.ComplianceMapping, framework string) string {
	var out []string
	for _, m := range ms {
		if m.Framework == framework {
			out = append(out, m.ID+star(m.Verified))
		}
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, ", ")
}

func kindLabel(t schemas.ComplianceTest) string {
	if t.Kind == schemas.ComplianceTestGate {
		return "gate (" + string(t.Effect) + ")"
	}
	if t.Axis != "" {
		return "condition (" + t.Axis + ")"
	}
	return "condition"
}

func star(verified bool) string {
	if verified {
		return ""
	}
	return "*"
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// short keeps the first 12 characters of a hash after any "sha256:" prefix.
func short(h string) string {
	h = strings.TrimPrefix(h, "sha256:")
	if len(h) > 12 {
		return h[:12]
	}
	return orDash(h)
}

// cell makes text safe inside a Markdown table cell.
func cell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.Join(strings.Fields(s), " ")
}
