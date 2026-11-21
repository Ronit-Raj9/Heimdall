package sharelinks

import (
	"fmt"
	"html"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Export formats of a report card.
const (
	FormatHTML     = "html"
	FormatMarkdown = "md"
	FormatJSON     = "json"
)

// ReportDoc is what a report card export is rendered from.
type ReportDoc struct {
	Evaluation *schemas.Evaluation
	Report     *reportcard.Report
	// Generated is when the export was made; zero means now.
	Generated time.Time
	// Source is where the numbers came from, shown in the footer (the
	// server's public URL); empty leaves it out.
	Source string
}

var capabilityNames = map[string]string{
	"terminal": "Terminal", "coding": "Coding", "memory": "Memory", "support": "Customer support",
	"research": "Research", "voice": "Voice", "safety": "Safety", "team": "Teamwork", "gpu": "GPU",
}

var faultNames = map[string]string{
	"clean": "No fault", "model-down": "Model goes down", "broken-tool": "Broken tool",
	"misleading-instruction": "Misleading instruction",
}

func capabilityName(id string) string {
	if n, ok := capabilityNames[id]; ok {
		return n
	}
	return strings.ReplaceAll(strings.ReplaceAll(id, "_", " "), "-", " ")
}

func faultName(id string) string {
	if n, ok := faultNames[id]; ok {
		return n
	}
	return strings.ReplaceAll(strings.ReplaceAll(id, "_", " "), "-", " ")
}

func pct(x float64) string {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return "-"
	}
	return fmt.Sprintf("%d%%", int(math.Round(x*100)))
}

func points(x float64) string {
	p := int(math.Round(x * 100))
	if p > 0 {
		return fmt.Sprintf("+%d pts", p)
	}
	return fmt.Sprintf("%d pts", p)
}

func usd(x float64, runs int) string {
	if runs > 0 && x == 0 {
		return "unknown"
	}
	if x < 10 {
		return fmt.Sprintf("$%.2f", x)
	}
	return fmt.Sprintf("$%.0f", math.Round(x))
}

func secs(s float64) string {
	n := int(math.Round(s))
	switch {
	case n < 60:
		return fmt.Sprintf("%ds", n)
	case n < 3600:
		return fmt.Sprintf("%dm %ds", n/60, n%60)
	}
	return fmt.Sprintf("%dh %dm", n/3600, (n/60)%60)
}

// Visible is the report as people see it: an evaluation in score mode runs a
// hidden repeat of its one arm, so only that arm is kept, with no A/B
// verdict (the web report card does the same).
func Visible(r *reportcard.Report) *reportcard.Report {
	if r == nil || r.Mode != schemas.EvaluationModeScore || len(r.Arms) == 0 {
		return r
	}
	label := r.Arms[0].Label
	keep := func(m map[string]reportcard.Metrics) map[string]reportcard.Metrics {
		out := map[string]reportcard.Metrics{}
		if v, ok := m[label]; ok {
			out[label] = v
		}
		return out
	}
	c := *r
	c.Arms = r.Arms[:1]
	c.Overall = nil
	c.Capabilities = make([]reportcard.Capability, len(r.Capabilities))
	for i, x := range r.Capabilities {
		x.Arms = keep(x.Arms)
		c.Capabilities[i] = x
	}
	c.Benchmarks = make([]reportcard.Benchmark, len(r.Benchmarks))
	for i, x := range r.Benchmarks {
		x.Arms = keep(x.Arms)
		x.Verdict = nil
		c.Benchmarks[i] = x
	}
	c.Reliability = make([]reportcard.Reliability, len(r.Reliability))
	for i, x := range r.Reliability {
		fs := map[string]reportcard.FaultScore{}
		if v, ok := x.Arms[label]; ok {
			fs[label] = v
		}
		x.Arms = fs
		c.Reliability[i] = x
	}
	sf := func(m map[string]float64) map[string]float64 {
		out := map[string]float64{}
		if v, ok := m[label]; ok {
			out[label] = v
		}
		return out
	}
	c.Safety = reportcard.Safety{AttackResisted: sf(r.Safety.AttackResisted), CheatRate: sf(r.Safety.CheatRate)}
	return &c
}

// OverallScore is the runs-weighted mean score of one arm across
// benchmarks (capabilities when no benchmark row has runs). ok is false
// when the arm has no score yet.
func OverallScore(r *reportcard.Report, label string) (score float64, runs int, ok bool) {
	if r == nil {
		return 0, 0, false
	}
	rows := make([]reportcard.Metrics, 0, len(r.Benchmarks))
	for _, b := range r.Benchmarks {
		if m, ok := b.Arms[label]; ok {
			rows = append(rows, m)
		}
	}
	if len(rows) == 0 {
		for _, c := range r.Capabilities {
			if m, ok := c.Arms[label]; ok {
				rows = append(rows, m)
			}
		}
	}
	var sum float64
	for _, m := range rows {
		if m.Runs > 0 && !math.IsNaN(m.Score) {
			sum += m.Score * float64(m.Runs)
			runs += m.Runs
		}
	}
	if runs == 0 {
		return 0, 0, false
	}
	return sum / float64(runs), runs, true
}

func armTitle(a reportcard.Arm) string {
	v := ""
	if a.Version > 0 {
		v = fmt.Sprintf(" v%d", a.Version)
	}
	return fmt.Sprintf("%s%s on %s", a.Agent, v, a.Model)
}

func (d ReportDoc) title() string {
	if d.Evaluation != nil && strings.TrimSpace(d.Evaluation.Spec.Name) != "" {
		return d.Evaluation.Spec.Name
	}
	if d.Report != nil && len(d.Report.Arms) > 0 {
		return "Report card: " + armTitle(d.Report.Arms[0])
	}
	return "Report card"
}

func (d ReportDoc) generated() time.Time {
	if d.Generated.IsZero() {
		return time.Now().UTC()
	}
	return d.Generated.UTC()
}

func (d ReportDoc) facts() [][2]string {
	var out [][2]string
	if d.Evaluation != nil {
		ev := d.Evaluation
		out = append(out, [2]string{"Evaluation", ev.ID})
		if ev.Spec.Mode != "" {
			out = append(out, [2]string{"What it measures", modeName(ev.Spec.Mode)})
		}
		out = append(out, [2]string{"Status", ev.Status})
		if ev.Spec.TasksPerBenchmark > 0 {
			out = append(out, [2]string{"Tasks per benchmark", fmt.Sprint(ev.Spec.TasksPerBenchmark)})
		}
		if ev.Spec.Trials > 0 {
			out = append(out, [2]string{"Repeats per task", fmt.Sprint(ev.Spec.Trials)})
		}
		if len(ev.Spec.Faults) > 0 {
			names := make([]string, len(ev.Spec.Faults))
			for i, f := range ev.Spec.Faults {
				names[i] = faultName(f)
			}
			out = append(out, [2]string{"Faults", strings.Join(names, ", ")})
		}
		if !ev.CreatedAt.IsZero() {
			out = append(out, [2]string{"Started", ev.CreatedAt.UTC().Format("2006-01-02 15:04 UTC")})
		}
	} else if d.Report != nil {
		out = append(out, [2]string{"Evaluation", d.Report.Evaluation})
	}
	return out
}

func modeName(m string) string {
	switch m {
	case schemas.EvaluationModeScore:
		return "Score"
	case schemas.EvaluationModeVersions:
		return "Version vs version"
	case schemas.EvaluationModeModels:
		return "Model vs model"
	case schemas.EvaluationModeOpponent:
		return "Vs a baseline agent"
	}
	return m
}

func winnerText(w string, delta float64, significant bool) string {
	switch w {
	case "", "unclear", "tie":
		return "No clear winner yet"
	case "invalid", "broken":
		return "Test was broken"
	}
	s := fmt.Sprintf("%s wins (%s)", w, points(math.Abs(delta)))
	if significant {
		s += ", clear difference"
	}
	return s
}

// table is a rendered table: a header and rows of cells.
type table struct {
	head []string
	rows [][]string
}

func (d ReportDoc) sections() []struct {
	title string
	t     table
	note  string
} {
	r := Visible(d.Report)
	type sec = struct {
		title string
		t     table
		note  string
	}
	var out []sec
	if r == nil {
		return out
	}
	labels := make([]string, len(r.Arms))
	for i, a := range r.Arms {
		labels[i] = a.Label
	}
	multi := len(r.Arms) > 1
	armCol := func(label string) string {
		if !multi {
			return ""
		}
		return "Setup " + label
	}
	metricRow := func(name string, label string, m reportcard.Metrics) []string {
		row := []string{name}
		if multi {
			row = append(row, armCol(label))
		}
		return append(row, pct(m.Score), pct(m.CILow)+" to "+pct(m.CIHigh), fmt.Sprint(m.Runs), usd(m.CostUSD, m.Runs), secs(m.WallS))
	}
	head := func(first string) []string {
		h := []string{first}
		if multi {
			h = append(h, "Setup")
		}
		return append(h, "Score", "Likely range", "Runs", "Cost", "Time per run")
	}

	// Overall per arm.
	ov := table{head: []string{"Setup", "Agent", "Score", "Runs"}}
	for _, a := range r.Arms {
		s, n, ok := OverallScore(r, a.Label)
		score := "-"
		if ok {
			score = pct(s)
		}
		ov.rows = append(ov.rows, []string{a.Label, armTitle(a), score, fmt.Sprint(n)})
	}
	note := ""
	if r.Overall != nil {
		note = "Overall: " + winnerText(r.Overall.Winner, r.Overall.Delta, r.Overall.Significant)
		if r.Overall.Benchmarks > 0 {
			wins := make([]string, 0, len(r.Overall.Wins))
			for _, l := range labels {
				wins = append(wins, fmt.Sprintf("%s better on %d", l, r.Overall.Wins[l]))
			}
			note += fmt.Sprintf(" (%s of %d benchmarks)", strings.Join(wins, ", "), r.Overall.Benchmarks)
		}
	}
	out = append(out, sec{"Summary", ov, note})

	if len(r.Capabilities) > 0 {
		t := table{head: head("Skill")}
		for _, c := range r.Capabilities {
			for _, l := range labels {
				if m, ok := c.Arms[l]; ok {
					t.rows = append(t.rows, metricRow(capabilityName(c.Capability), l, m))
				}
			}
		}
		out = append(out, sec{"By skill", t, ""})
	}
	if len(r.Benchmarks) > 0 {
		t := table{head: head("Benchmark")}
		if multi {
			t.head = append(t.head, "Result")
		}
		for _, b := range r.Benchmarks {
			for i, l := range labels {
				m, ok := b.Arms[l]
				if !ok {
					continue
				}
				row := metricRow(b.Benchmark, l, m)
				if multi {
					res := ""
					if i == 0 && b.Verdict != nil {
						res = winnerText(b.Verdict.Winner, b.Verdict.Delta, b.Verdict.Significant)
					}
					row = append(row, res)
				}
				t.rows = append(t.rows, row)
			}
		}
		out = append(out, sec{"By benchmark", t, ""})
	}
	if len(r.Reliability) > 0 {
		t := table{head: []string{"Fault"}}
		if multi {
			t.head = append(t.head, "Setup")
		}
		t.head = append(t.head, "Score", "Drop", "Runs")
		for _, x := range r.Reliability {
			for _, l := range labels {
				f, ok := x.Arms[l]
				if !ok {
					continue
				}
				row := []string{faultName(x.Fault)}
				if multi {
					row = append(row, armCol(l))
				}
				t.rows = append(t.rows, append(row, pct(f.Score), points(-f.Drop), fmt.Sprint(f.Runs)))
			}
		}
		out = append(out, sec{"When things go wrong", t, "Score under each fault, and how far it dropped from normal conditions."})
	}
	if len(r.Safety.AttackResisted) > 0 || len(r.Safety.CheatRate) > 0 {
		t := table{head: []string{"Setup", "Attacks resisted", "Cheating caught"}}
		for _, l := range labels {
			ar, okA := r.Safety.AttackResisted[l]
			cr, okC := r.Safety.CheatRate[l]
			if !okA && !okC {
				continue
			}
			a, c := "-", "-"
			if okA {
				a = pct(ar)
			}
			if okC {
				c = pct(cr)
			}
			t.rows = append(t.rows, []string{l, a, c})
		}
		out = append(out, sec{"Safety", t, ""})
	}
	return out
}

func mdCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", `\|`), "\n", " ")
}

// Markdown renders the report card as GitHub-flavoured Markdown.
func (d ReportDoc) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", mdCell(d.title()))
	if d.Report != nil {
		for _, a := range Visible(d.Report).Arms {
			fmt.Fprintf(&b, "- Setup %s: %s\n", a.Label, mdCell(armTitle(a)))
		}
		b.WriteString("\n")
	}
	for _, f := range d.facts() {
		fmt.Fprintf(&b, "**%s:** %s  \n", f[0], mdCell(f[1]))
	}
	b.WriteString("\n")
	for _, s := range d.sections() {
		fmt.Fprintf(&b, "## %s\n\n", s.title)
		if s.note != "" {
			fmt.Fprintf(&b, "%s\n\n", mdCell(s.note))
		}
		if len(s.t.rows) == 0 {
			b.WriteString("No finished runs yet.\n\n")
			continue
		}
		b.WriteString("| " + strings.Join(s.t.head, " | ") + " |\n")
		seps := make([]string, len(s.t.head))
		for i := range seps {
			seps[i] = "---"
		}
		b.WriteString("|" + strings.Join(seps, "|") + "|\n")
		for _, row := range s.t.rows {
			cells := make([]string, len(row))
			for i, c := range row {
				cells[i] = mdCell(c)
			}
			b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(d.footer())
	b.WriteString("\n")
	return b.String()
}

func (d ReportDoc) footer() string {
	s := "Score is the share of runs that passed the hidden checks and every safety check. The likely range is a 95% interval: few runs give a wide range. Generated by Heimdall on " +
		d.generated().Format("2006-01-02 15:04 UTC") + "."
	if d.Source != "" {
		s += " Source: " + d.Source
	}
	return s
}

// printCSS is the export's stylesheet: plain on screen, and laid out for
// paper when printed (Save as PDF), with tables that never split a row.
const printCSS = `
:root{color-scheme:light;--fg:#0f172a;--muted:#64748b;--line:#e2e8f0;--bg:#fff;--soft:#f8fafc;--accent:#4f46e5}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Inter,Roboto,Helvetica,Arial,sans-serif}
main{max-width:960px;margin:0 auto;padding:40px 24px 56px}
header{border-bottom:1px solid var(--line);padding-bottom:20px;margin-bottom:24px}
.eyebrow{color:var(--accent);font-size:12px;font-weight:600;letter-spacing:.04em;text-transform:uppercase;margin:0 0 6px}
h1{font-size:26px;line-height:1.25;margin:0 0 10px;letter-spacing:-.01em}
h2{font-size:16px;margin:28px 0 10px}
.arms{margin:0;padding:0;list-style:none;color:var(--muted)}
.facts{display:grid;grid-template-columns:repeat(auto-fill,minmax(180px,1fr));gap:10px 20px;margin:16px 0 0}
.facts div{min-width:0}.facts dt{color:var(--muted);font-size:12px}.facts dd{margin:0;font-weight:500;overflow-wrap:anywhere}
.note{color:var(--muted);margin:0 0 10px}
table{width:100%;border-collapse:collapse;font-variant-numeric:tabular-nums}
th,td{text-align:left;padding:7px 10px;border-bottom:1px solid var(--line);vertical-align:top}
th{font-size:12px;font-weight:600;color:var(--muted);background:var(--soft)}
tr{break-inside:avoid}
footer{margin-top:32px;padding-top:16px;border-top:1px solid var(--line);color:var(--muted);font-size:12px}
@media print{
 @page{size:A4;margin:16mm}
 body{font-size:11px}
 main{max-width:none;padding:0}
 h2{break-after:avoid}
 section{break-inside:auto}
 thead{display:table-header-group}
 a{color:inherit;text-decoration:none}
}
`

// HTML renders the report card as one standalone page: no script, no
// external asset, a print stylesheet so the browser's Print, Save as PDF
// gives a clean document.
func (d ReportDoc) HTML() string {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">")
	fmt.Fprintf(&b, "<title>%s</title><style>%s</style></head><body><main>", e(d.title()), printCSS)
	b.WriteString("<header><p class=\"eyebrow\">Heimdall report card</p>")
	fmt.Fprintf(&b, "<h1>%s</h1>", e(d.title()))
	if d.Report != nil {
		b.WriteString("<ul class=\"arms\">")
		for _, a := range Visible(d.Report).Arms {
			fmt.Fprintf(&b, "<li>Setup %s: %s</li>", e(a.Label), e(armTitle(a)))
		}
		b.WriteString("</ul>")
	}
	b.WriteString("<dl class=\"facts\">")
	for _, f := range d.facts() {
		fmt.Fprintf(&b, "<div><dt>%s</dt><dd>%s</dd></div>", e(f[0]), e(f[1]))
	}
	b.WriteString("</dl></header>")
	for _, s := range d.sections() {
		fmt.Fprintf(&b, "<section><h2>%s</h2>", e(s.title))
		if s.note != "" {
			fmt.Fprintf(&b, "<p class=\"note\">%s</p>", e(s.note))
		}
		if len(s.t.rows) == 0 {
			b.WriteString("<p class=\"note\">No finished runs yet.</p></section>")
			continue
		}
		b.WriteString("<table><thead><tr>")
		for _, h := range s.t.head {
			fmt.Fprintf(&b, "<th>%s</th>", e(h))
		}
		b.WriteString("</tr></thead><tbody>")
		for _, row := range s.t.rows {
			b.WriteString("<tr>")
			for _, c := range row {
				fmt.Fprintf(&b, "<td>%s</td>", e(c))
			}
			b.WriteString("</tr>")
		}
		b.WriteString("</tbody></table></section>")
	}
	fmt.Fprintf(&b, "<footer>%s</footer></main></body></html>\n", e(d.footer()))
	return b.String()
}

// Badge colours.
const (
	badgeGood    = "#16a34a"
	badgeOK      = "#ca8a04"
	badgeLow     = "#dc2626"
	badgeNeutral = "#64748b"
)

// BadgeColor picks the colour of a score badge.
func BadgeColor(score float64) string {
	switch {
	case score >= 0.7:
		return badgeGood
	case score >= 0.4:
		return badgeOK
	}
	return badgeLow
}

// textWidth estimates the width of s in 11px Verdana, the shields.io
// metric, so the badge needs no font measurement.
func textWidth(s string) int {
	w := 0.0
	for _, r := range s {
		switch {
		case strings.ContainsRune("ijlI.,:;|!' ", r):
			w += 3.6
		case strings.ContainsRune("mwMW%@", r):
			w += 10.5
		case r >= 'A' && r <= 'Z':
			w += 7.6
		default:
			w += 6.6
		}
	}
	return int(math.Ceil(w))
}

// Badge renders a flat two-part SVG badge: label on grey, value on colour.
func Badge(label, value, color string) string {
	if color == "" {
		color = badgeNeutral
	}
	lw := textWidth(label) + 12
	vw := textWidth(value) + 12
	total := lw + vw
	e := html.EscapeString
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">`, total, e(label), e(value))
	fmt.Fprintf(&b, `<title>%s: %s</title>`, e(label), e(value))
	b.WriteString(`<linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>`)
	fmt.Fprintf(&b, `<clipPath id="r"><rect width="%d" height="20" rx="3" fill="#fff"/></clipPath>`, total)
	fmt.Fprintf(&b, `<g clip-path="url(#r)"><rect width="%d" height="20" fill="#555"/><rect x="%d" width="%d" height="20" fill="%s"/><rect width="%d" height="20" fill="url(#s)"/></g>`, lw, lw, vw, e(color), total)
	b.WriteString(`<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">`)
	fmt.Fprintf(&b, `<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text><text x="%d" y="14">%s</text>`, lw/2, e(label), lw/2, e(label))
	fmt.Fprintf(&b, `<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text><text x="%d" y="14">%s</text>`, lw+vw/2, e(value), lw+vw/2, e(value))
	b.WriteString(`</g></svg>`)
	return b.String()
}

// ReportBadge is the badge of a report card: the first setup's overall
// score, or "no runs yet".
func ReportBadge(r *reportcard.Report, label string) string {
	if label == "" {
		label = "heimdall"
	}
	r = Visible(r)
	if r == nil || len(r.Arms) == 0 {
		return Badge(label, "no runs yet", badgeNeutral)
	}
	arms := append([]reportcard.Arm(nil), r.Arms...)
	sort.SliceStable(arms, func(i, j int) bool { return arms[i].Label < arms[j].Label })
	s, _, ok := OverallScore(r, arms[0].Label)
	if !ok {
		return Badge(label, "no runs yet", badgeNeutral)
	}
	return Badge(label, pct(s), BadgeColor(s))
}

// VerdictBadge is the badge of a comparison's verdict: its action.
func VerdictBadge(action, decision string) string {
	value, color := strings.ToLower(action), badgeNeutral
	switch strings.ToUpper(action) {
	case "SHIP":
		value, color = "ship", badgeGood
	case "HOLD":
		value, color = "hold", badgeLow
	case "NO DECISION", "NO_DECISION":
		value, color = "no decision", badgeOK
	case "FIX HARNESS", "FIX_HARNESS":
		value, color = "fix harness", badgeLow
	}
	if value == "" {
		value = strings.ToLower(decision)
	}
	if value == "" {
		value = "pending"
	}
	return Badge("verdict", value, color)
}
