package traceinbox

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/traceimport"
)

// Preview renders the redacted transcript preview of an item: why it was
// flagged, the conversation, the tool calls and the outcome, cut to
// MaxPreviewBytes. Every text in it comes from the already redacted session.
func Preview(s *traceimport.Session, reasons []string, source string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Flagged session %s\n\n", s.ID)
	fmt.Fprintf(&b, "Source: %s (%s format). Redacted before it was stored; names and street addresses are not caught.\n\n", source, s.Format)
	if len(reasons) > 0 {
		b.WriteString("## Why it was flagged\n\n")
		for _, r := range reasons {
			b.WriteString("- " + r + "\n")
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Outcome: %s", s.Outcome.Status)
	if len(s.Outcome.Hints) > 0 {
		b.WriteString(" (" + strings.Join(s.Outcome.Hints, "; ") + ")")
	}
	b.WriteString("\n")
	if s.Model != "" {
		fmt.Fprintf(&b, "Model: %s\n", s.Model)
	}
	b.WriteString("\n## Conversation\n")
	for i, t := range s.Turns {
		if t.Role == "system" {
			continue
		}
		fmt.Fprintf(&b, "\n%d. %s:\n%s\n", i+1, t.Role, indent(clipText(t.Text, 1500)))
	}
	if len(s.ToolCalls) > 0 {
		b.WriteString("\n## Tool calls\n")
	}
	for _, c := range s.ToolCalls {
		status := "ok"
		if c.IsError {
			status = "failed"
		}
		fmt.Fprintf(&b, "\n%d. %s (%s)\n    arguments: %s\n", c.Seq, c.Name, status, oneLine(string(c.Arguments), 300))
		if c.Result != "" {
			fmt.Fprintf(&b, "    result: %s\n", oneLine(c.Result, 300))
		}
		if c.Error != "" {
			fmt.Fprintf(&b, "    error: %s\n", oneLine(c.Error, 300))
		}
	}
	if s.FinalAnswer != "" {
		b.WriteString("\n## Final answer\n\n" + indent(clipText(s.FinalAnswer, 1500)) + "\n")
	}
	out := b.String()
	if len(out) > MaxPreviewBytes {
		cut := MaxPreviewBytes - 64
		for cut > 0 && out[cut]&0xC0 == 0x80 {
			cut--
		}
		out = out[:cut] + "\n\n[preview cut; the whole session is in session.json]\n"
	}
	return []byte(out)
}

func indent(s string) string {
	if strings.TrimSpace(s) == "" {
		return "    (empty)"
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + " [...]"
}

// ListSuites returns the scenario suites under the benchmarks folder with
// the template each was made from, sorted by id. A suite of another kind
// (or one without a detectable template) is listed with an empty template.
func ListSuites(benchmarksRoot string) ([]SuiteRef, error) {
	if benchmarksRoot == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(benchmarksRoot)
	if err != nil {
		return nil, err
	}
	var out []SuiteRef
	for _, e := range entries {
		if !e.IsDir() || !scaffold.ValidID(e.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(benchmarksRoot, e.Name(), "suite.yaml"))
		if err != nil {
			continue
		}
		out = append(out, SuiteRef{ID: e.Name(), Template: scaffold.DetectTemplate(data)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
