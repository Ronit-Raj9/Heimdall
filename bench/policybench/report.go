package policybench

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/taskhealth"
)

// RunRecord is one counted, scored run of an accepted policy task.
type RunRecord struct {
	RunID  string `json:"run_id"`
	ExpID  string `json:"exp_id"`
	Task   string `json:"task"`
	Agent  string `json:"agent"`
	Passed bool   `json:"passed"`
	Reason string `json:"reason,omitempty"` // the grade's reason (failed checks)
	At     string `json:"at,omitempty"`
}

// Rate is a pass count with its 95% Wilson range.
type Rate struct {
	Runs   int     `json:"runs"`
	Passed int     `json:"passed"`
	Rate   float64 `json:"rate"`
	Lo     float64 `json:"lo"`
	Hi     float64 `json:"hi"`
}

func rateOf(passed, runs int) Rate {
	r := Rate{Runs: runs, Passed: passed}
	if runs > 0 {
		r.Rate = float64(passed) / float64(runs)
		r.Lo, r.Hi, _ = taskhealth.Wilson(passed, runs)
	}
	return r
}

// ClauseResult is one clause's results for one agent.
type ClauseResult struct {
	Number string          `json:"number"`
	Title  string          `json:"title"`
	Total  Rate            `json:"total"`
	ByKind map[string]Rate `json:"by_kind"`
}

// Example is a failed run behind a finding.
type Example struct {
	RunID  string `json:"run_id"`
	ExpID  string `json:"exp_id"`
	Task   string `json:"task"`
	Reason string `json:"reason,omitempty"`
}

// Finding is a plain-words explanation of how an agent breaks a clause.
type Finding struct {
	Clause   string    `json:"clause"`
	Kind     string    `json:"kind"` // insists, edge, plain
	Text     string    `json:"text"`
	Examples []Example `json:"examples"`
}

// AgentReport is the report of one agent (agent version).
type AgentReport struct {
	Agent    string         `json:"agent"`
	Total    Rate           `json:"total"`
	ByKind   map[string]Rate `json:"by_kind"`
	Clauses  []ClauseResult `json:"clauses"`
	Findings []Finding      `json:"findings"`
}

// Report is the compliance report of a policy document.
type Report struct {
	Suite       string        `json:"suite"`
	Doc         string        `json:"doc"`
	Title       string        `json:"title"`
	Source      string        `json:"source"`
	GeneratedAt string        `json:"generated_at"`
	Clauses     int           `json:"clauses"`
	Tested      int           `json:"tested_clauses"`
	Accepted    int           `json:"accepted_tasks"`
	Pending     int           `json:"pending_tasks"`
	Runs        int           `json:"runs"`
	Agents      []AgentReport `json:"agents"`
	Untested    []string      `json:"untested_clauses"`
	NotTestable []NoTest      `json:"not_testable,omitempty"`
	Notes       []string      `json:"notes"`
}

// TaskClause maps the accepted tasks to their clause and kind.
func (d *Doc) TaskClause() map[string]Draft {
	out := map[string]Draft{}
	for _, x := range d.Drafts {
		if x.Status == StatusAccepted {
			out[x.Task] = x
		}
	}
	return out
}

// BuildReport computes the report from the runs of the document's accepted
// tasks. Every number comes from the runs given; with none it says so.
func BuildReport(suiteID string, d *Doc, runs []RunRecord, now time.Time) *Report {
	tasks := d.TaskClause()
	counts := d.Counts()
	r := &Report{Suite: suiteID, Doc: d.ID, Title: d.Title, Source: d.Name, GeneratedAt: now.UTC().Format(time.RFC3339),
		Clauses: len(d.Clauses), Accepted: counts[StatusAccepted], Pending: counts[StatusPending], Agents: []AgentReport{},
		Untested: []string{}, NotTestable: d.NotTestable, Notes: []string{}}
	tested := map[string]bool{}
	for _, x := range tasks {
		tested[x.Clause] = true
	}
	for _, c := range d.Clauses {
		if !tested[c.Number] {
			r.Untested = append(r.Untested, c.Number)
		}
	}
	r.Tested = len(d.Clauses) - len(r.Untested)
	byAgent := map[string][]RunRecord{}
	for _, run := range runs {
		if _, ok := tasks[run.Task]; !ok {
			continue
		}
		byAgent[run.Agent] = append(byAgent[run.Agent], run)
		r.Runs++
	}
	agents := make([]string, 0, len(byAgent))
	for a := range byAgent {
		agents = append(agents, a)
	}
	sort.Strings(agents)
	for _, a := range agents {
		r.Agents = append(r.Agents, agentReport(a, d, tasks, byAgent[a]))
	}
	switch {
	case r.Accepted == 0:
		r.Notes = append(r.Notes, "No drafted task has been accepted yet, so nothing has been measured. Review the drafts first.")
	case r.Runs == 0:
		r.Notes = append(r.Notes, "No evaluation has run the accepted tasks yet. Run an evaluation of an agent on this benchmark; the report fills in from its runs.")
	}
	if r.Pending > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("%d drafted task(s) are still pending review and are not counted.", r.Pending))
	}
	if len(r.Untested) > 0 {
		r.Notes = append(r.Notes, fmt.Sprintf("%d of %d clauses have no accepted task, so the report says nothing about them.", len(r.Untested), len(d.Clauses)))
	}
	r.Notes = append(r.Notes, "Ranges are 95% Wilson intervals over counted runs. A clause with few runs has a wide range: run more trials before you rely on it.")
	return r
}

func agentReport(agent string, d *Doc, tasks map[string]Draft, runs []RunRecord) AgentReport {
	ar := AgentReport{Agent: agent, ByKind: map[string]Rate{}, Clauses: []ClauseResult{}, Findings: []Finding{}}
	type tally struct{ runs, passed int }
	total := tally{}
	kind := map[string]*tally{}
	clause := map[string]map[string]*tally{}
	fails := map[string]map[string][]RunRecord{}
	for _, run := range runs {
		x := tasks[run.Task]
		if clause[x.Clause] == nil {
			clause[x.Clause] = map[string]*tally{}
			fails[x.Clause] = map[string][]RunRecord{}
		}
		if clause[x.Clause][x.Kind] == nil {
			clause[x.Clause][x.Kind] = &tally{}
		}
		if kind[x.Kind] == nil {
			kind[x.Kind] = &tally{}
		}
		for _, t := range []*tally{&total, kind[x.Kind], clause[x.Clause][x.Kind]} {
			t.runs++
			if run.Passed {
				t.passed++
			}
		}
		if !run.Passed {
			fails[x.Clause][x.Kind] = append(fails[x.Clause][x.Kind], run)
		}
	}
	ar.Total = rateOf(total.passed, total.runs)
	for k, t := range kind {
		ar.ByKind[k] = rateOf(t.passed, t.runs)
	}
	for _, c := range d.Clauses {
		ks, ok := clause[c.Number]
		if !ok {
			continue
		}
		cr := ClauseResult{Number: c.Number, Title: c.Title, ByKind: map[string]Rate{}}
		ct := tally{}
		for k, t := range ks {
			cr.ByKind[k] = rateOf(t.passed, t.runs)
			ct.runs += t.runs
			ct.passed += t.passed
		}
		cr.Total = rateOf(ct.passed, ct.runs)
		ar.Clauses = append(ar.Clauses, cr)
		ar.Findings = append(ar.Findings, findings(c, cr, fails[c.Number])...)
	}
	return ar
}

func examples(runs []RunRecord) []Example {
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].At > runs[j].At })
	var out []Example
	for _, r := range runs {
		if len(out) == 3 {
			break
		}
		out = append(out, Example{RunID: r.RunID, ExpID: r.ExpID, Task: r.Task, Reason: clip(r.Reason, 300)})
	}
	return out
}

func held(r Rate) string {
	return fmt.Sprintf("%d of %d runs (%.0f%%, range %.0f-%.0f%%)", r.Passed, r.Runs, 100*r.Rate, 100*r.Lo, 100*r.Hi)
}

// findings explains a clause's failures in plain words.
func findings(c Clause, cr ClauseResult, fails map[string][]RunRecord) []Finding {
	var out []Finding
	norm, hasNorm := cr.ByKind[KindNormal]
	adv, hasAdv := cr.ByKind[KindAdversarial]
	edge, hasEdge := cr.ByKind[KindEdge]
	if hasAdv && adv.Passed < adv.Runs {
		text := fmt.Sprintf("Breaks clause %s when the customer insists: it held the rule in %s where the customer pushed back", c.Number, held(adv))
		if hasNorm && norm.Runs > 0 {
			text += fmt.Sprintf(", against %s in the plain case", held(norm))
		}
		out = append(out, Finding{Clause: c.Number, Kind: "insists", Text: text + ".", Examples: examples(fails[KindAdversarial])})
	}
	if hasEdge && edge.Passed < edge.Runs {
		out = append(out, Finding{Clause: c.Number, Kind: "edge", Text: fmt.Sprintf("Misses the edge case of clause %s: it followed the rule in %s at the boundary.", c.Number, held(edge)),
			Examples: examples(fails[KindEdge])})
	}
	if hasNorm && norm.Passed < norm.Runs {
		out = append(out, Finding{Clause: c.Number, Kind: "plain", Text: fmt.Sprintf("Does not follow clause %s even in the plain case: %s passed.", c.Number, held(norm)),
			Examples: examples(fails[KindNormal])})
	}
	return out
}

func pct(r Rate) string {
	if r.Runs == 0 {
		return "no runs"
	}
	return fmt.Sprintf("%.0f%% (%.0f-%.0f%%, n=%d)", 100*r.Rate, 100*r.Lo, 100*r.Hi, r.Runs)
}

// Markdown renders the report.
func (r *Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Compliance report: %s\n\n", r.Title)
	fmt.Fprintf(&b, "Benchmark `%s`, policy document %s (`%s`). Generated %s.\n\n", r.Suite, r.Source, r.Doc, r.GeneratedAt)
	fmt.Fprintf(&b, "- Clauses: %d, with accepted tasks: %d\n- Accepted tasks: %d, pending review: %d\n- Counted runs: %d\n\n", r.Clauses, r.Tested, r.Accepted, r.Pending, r.Runs)
	for _, n := range r.Notes {
		fmt.Fprintf(&b, "> %s\n", n)
	}
	b.WriteString("\n")
	for _, a := range r.Agents {
		fmt.Fprintf(&b, "## Agent %s\n\n", a.Agent)
		fmt.Fprintf(&b, "Overall: %s. Plain case: %s. Edge case: %s. Customer insists: %s.\n\n", pct(a.Total), pct(a.ByKind[KindNormal]), pct(a.ByKind[KindEdge]), pct(a.ByKind[KindAdversarial]))
		b.WriteString("| Clause | Title | Pass rate | Plain | Edge | Customer insists |\n|---|---|---|---|---|---|\n")
		for _, c := range a.Clauses {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", c.Number, mdCell(c.Title), pct(c.Total), pct(c.ByKind[KindNormal]), pct(c.ByKind[KindEdge]), pct(c.ByKind[KindAdversarial]))
		}
		b.WriteString("\n")
		if len(a.Findings) == 0 {
			b.WriteString("No clause broken in the counted runs.\n\n")
			continue
		}
		b.WriteString("### Where it breaks the policy\n\n")
		for _, f := range a.Findings {
			fmt.Fprintf(&b, "- %s\n", f.Text)
			for _, e := range f.Examples {
				fmt.Fprintf(&b, "  - run `%s` (evaluation `%s`, task `%s`)", e.RunID, e.ExpID, e.Task)
				if e.Reason != "" {
					fmt.Fprintf(&b, ": %s", oneLine(e.Reason))
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}
	if len(r.Untested) > 0 {
		fmt.Fprintf(&b, "## Clauses without accepted tasks\n\n%s\n\n", strings.Join(r.Untested, ", "))
	}
	if len(r.NotTestable) > 0 {
		b.WriteString("## Clauses with nothing to test\n\n")
		for _, n := range r.NotTestable {
			fmt.Fprintf(&b, "- %s: %s\n", n.Clause, oneLine(n.Reason))
		}
		b.WriteString("\n")
	}
	b.WriteString("This report maps measured runs to policy clauses. It is evidence for review, not a certificate of compliance.\n")
	return b.String()
}

func mdCell(s string) string { return strings.ReplaceAll(oneLine(s), "|", "\\|") }

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// PDF renders the report as a PDF.
func (r *Report) PDF() []byte {
	var bl []PDFBlock
	add := func(style, text string) { bl = append(bl, PDFBlock{Style: style, Text: text}) }
	add("h1", "Compliance report: "+r.Title)
	add("small", fmt.Sprintf("Benchmark %s, policy document %s (%s). Generated %s.", r.Suite, r.Source, r.Doc, r.GeneratedAt))
	add("body", fmt.Sprintf("Clauses: %d, with accepted tasks: %d. Accepted tasks: %d, pending review: %d. Counted runs: %d.", r.Clauses, r.Tested, r.Accepted, r.Pending, r.Runs))
	for _, n := range r.Notes {
		add("small", n)
	}
	for _, a := range r.Agents {
		add("h2", "Agent "+a.Agent)
		add("body", fmt.Sprintf("Overall: %s. Plain case: %s. Edge case: %s. Customer insists: %s.", pct(a.Total), pct(a.ByKind[KindNormal]), pct(a.ByKind[KindEdge]), pct(a.ByKind[KindAdversarial])))
		add("h3", "Per clause")
		for _, c := range a.Clauses {
			add("body", fmt.Sprintf("Clause %s, %s: %s. Plain %s; edge %s; customer insists %s.", c.Number, oneLine(c.Title), pct(c.Total),
				pct(c.ByKind[KindNormal]), pct(c.ByKind[KindEdge]), pct(c.ByKind[KindAdversarial])))
		}
		add("h3", "Where it breaks the policy")
		if len(a.Findings) == 0 {
			add("body", "No clause broken in the counted runs.")
		}
		for _, f := range a.Findings {
			add("body", "- "+f.Text)
			for _, e := range f.Examples {
				line := fmt.Sprintf("    run %s (evaluation %s, task %s)", e.RunID, e.ExpID, e.Task)
				if e.Reason != "" {
					line += ": " + oneLine(e.Reason)
				}
				add("small", line)
			}
		}
	}
	if len(r.Untested) > 0 {
		add("h2", "Clauses without accepted tasks")
		add("body", strings.Join(r.Untested, ", "))
	}
	if len(r.NotTestable) > 0 {
		add("h2", "Clauses with nothing to test")
		for _, n := range r.NotTestable {
			add("body", n.Clause+": "+oneLine(n.Reason))
		}
	}
	add("small", "This report maps measured runs to policy clauses. It is evidence for review, not a certificate of compliance.")
	return WritePDF("Compliance report: "+r.Title, bl)
}
