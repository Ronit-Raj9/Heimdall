package gates

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// CanaryEchoed is the name of the gate that looks for contamination canaries
// (docs/held-out-and-canaries.md) in what the agent produced.
const CanaryEchoed = "canary_echoed"

// maxCanaryArtifactBytes caps how much of one artifact file the gate reads.
const maxCanaryArtifactBytes = 8 << 20

var _ schemas.Gate = (*canaryEchoedGate)(nil)

// canaryEchoedGate reports a run whose output holds a benchmark canary GUID
// it had no business producing. It reads what the agent wrote: the answer,
// the final message, the lines the exported diff adds, the exported artifact
// files and the arguments of its tool calls (the tool proxy ledger and the
// tool call events). It fires when that output holds
//
//   - the canary of a different suite: a GUID that sibling suites of the run's
//     suite declare in suite.yaml, or any GUID behind the canary marker
//     phrase that is not the run's own; the agent can only know it from
//     training data or from text outside this task, so it is evidence of a
//     verbatim training-data leak or of cross-suite contamination;
//   - the run's own suite canary while that canary is not in the prompt (no
//     instruction file of the task contains it): the agent reproduced text
//     it was never shown in this run.
//
// The run's own canary in the output is not evidence when the stamped
// instruction carries it: the agent saw it in its prompt and may have copied
// it, quoted the task, or kept the line in a file it wrote. The gate says so
// in its detail and does not fire.
//
// The gate is report only and opt-in. A canary detects verbatim copies of
// stamped text and nothing else: a paraphrased leak, a leak of an unstamped
// suite, or a model that learned the task but drops the comment line all
// pass. A run that does not fire proves nothing about contamination. The
// raw agent stream is not read: it mixes the prompt and tool results with
// the model's own text, so a hit there could not be attributed to the agent.
type canaryEchoedGate struct{}

// GetName implements schemas.Part.
func (g *canaryEchoedGate) GetName() string { return CanaryEchoed }

// GetVersion implements schemas.Part.
func (g *canaryEchoedGate) GetVersion() string { return version }

// Effect implements schemas.Gate.
func (g *canaryEchoedGate) Effect() schemas.GateEffect { return schemas.GateReport }

// Baseline implements schemas.Gate.
func (g *canaryEchoedGate) Baseline() bool { return false }

// Check implements schemas.Gate. Evidence lists "<source>:<guid>" for every
// canary that fired it, sources being answer, final_message,
// diff:<path>, artifact:<path>, tool_call:<seq> and event:<event id>. An
// unreadable diff is an error; an unreadable artifact or sibling suite is a
// note in the detail.
func (g *canaryEchoedGate) Check(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.GateResult, error) {
	if rc == nil || rc.Suite == nil {
		return newResult(g, false, "no suite in the run context", nil), nil
	}
	own, known, notes := runCanaries(rc)
	if len(known) == 0 {
		notes = append(notes, "no suite canary is known, so only canary lines with a GUID were looked for")
	}
	sources, srcNotes, err := canarySources(rc, ev)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", CanaryEchoed, err)
	}
	notes = append(notes, srcNotes...)
	prompt, promptErr := promptText(rc.Task)
	inPrompt := own != "" && promptErr == nil && strings.Contains(strings.ToLower(prompt), own)
	var parts, evidence []string
	var ownSeen []string
	for _, s := range sources {
		for _, h := range utils.FindCanaries(s.text, known) {
			if own != "" && h.GUID == own {
				ownSeen = append(ownSeen, s.name)
				if promptErr != nil || inPrompt {
					continue
				}
				parts = append(parts, fmt.Sprintf("%s holds this suite's own canary %s, which was not in the prompt", s.name, h.GUID))
				evidence = append(evidence, s.name+":"+h.GUID)
				continue
			}
			owner := "an unknown suite"
			if h.Known {
				owner = "suite " + h.Suite
			}
			parts = append(parts, fmt.Sprintf("%s holds the canary %s of %s", s.name, h.GUID, owner))
			evidence = append(evidence, s.name+":"+h.GUID)
		}
	}
	if len(ownSeen) > 0 {
		switch {
		case promptErr != nil:
			notes = append(notes, fmt.Sprintf("this suite's own canary appears in %s, but the prompt could not be read (%v), so the echo is not judged", strings.Join(ownSeen, ", "), promptErr))
		case inPrompt:
			notes = append(notes, fmt.Sprintf("this suite's own canary appears in %s; it is not evidence, because the stamped instruction put it in the agent's prompt", strings.Join(ownSeen, ", ")))
		}
	}
	limit := "a canary only catches verbatim copies of stamped text; no hit proves nothing about paraphrased or unstamped leaks"
	if len(parts) == 0 {
		return newResult(g, false, strings.Join(append(notes, limit), "; "), nil), nil
	}
	detail := "benchmark canary in the agent's output, a sign of a verbatim training-data leak or cross-suite contamination: " +
		strings.Join(parts, "; ")
	if len(notes) > 0 {
		detail += "; " + strings.Join(notes, "; ")
	}
	return newResult(g, true, detail+"; "+limit, evidence), nil
}

// newCanaryEchoedGate returns the canary_echoed gate.
func newCanaryEchoedGate() *canaryEchoedGate {
	return &canaryEchoedGate{}
}

// canarySource is one piece of agent output the gate scans.
type canarySource struct {
	name string
	text string
}

// runCanaries returns the run's own suite canary and every known canary
// (GUID -> suite id): the suite's own, from the loaded suite or its
// suite.yaml, plus those of the sibling suite folders beside it. Problems
// reading siblings become notes, never errors: the canary marker still
// catches stamped lines of suites the gate could not read.
func runCanaries(rc *schemas.RunContext) (own string, known map[string]string, notes []string) {
	known = map[string]string{}
	own = strings.ToLower(rc.Suite.Canary)
	if rc.Suite.Root != "" {
		if own == "" {
			if b, err := os.ReadFile(filepath.Join(rc.Suite.Root, "suite.yaml")); err == nil {
				own = utils.SuiteYAMLCanary(b)
			}
		}
		siblings, err := utils.SuiteCanaries(filepath.Dir(rc.Suite.Root))
		if err != nil {
			notes = append(notes, "canaries of other suites could not be read: "+err.Error())
		}
		for guid, id := range siblings {
			known[guid] = id
		}
	}
	if !utils.ValidCanary(own) {
		own = ""
	}
	if own != "" {
		known[own] = rc.Suite.ID
	}
	return own, known, notes
}

// canarySources collects the agent's output from the evidence: answer,
// final message, added diff lines per file, artifact files, tool call
// arguments and tool call event targets.
func canarySources(rc *schemas.RunContext, ev *schemas.Evidence) ([]canarySource, []string, error) {
	if ev == nil {
		return nil, nil, nil
	}
	var out []canarySource
	var notes []string
	if ev.Answer != nil && ev.Answer.Text != "" {
		out = append(out, canarySource{"answer", ev.Answer.Text})
	}
	if ev.Claim != nil && ev.Claim.Text != "" {
		out = append(out, canarySource{"final_message", ev.Claim.Text})
	}
	if ev.Diff != nil {
		v, err := loadDiffView(ev.Diff)
		if err != nil {
			return nil, nil, err
		}
		paths := make([]string, 0, len(v.files))
		for p := range v.files {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			if added := addedText(v.files[p]); added != "" {
				out = append(out, canarySource{"diff:" + p, added})
			}
		}
	}
	for _, a := range ev.Artifacts {
		if a.Path == "" || a.Error != "" {
			continue
		}
		text, err := readArtifactText(rc, a.Path)
		if err != nil {
			notes = append(notes, fmt.Sprintf("artifact %s not read: %v", a.Path, err))
			continue
		}
		out = append(out, canarySource{"artifact:" + a.Path, text})
	}
	for _, c := range ev.ToolCalls {
		if c.Arguments != "" {
			out = append(out, canarySource{fmt.Sprintf("tool_call:%d", c.Seq), c.Arguments})
		}
	}
	for _, e := range ev.Events {
		if e.Type != schemas.EventToolCall {
			continue
		}
		if text := toolCallText(e); strings.TrimSpace(text) != "" {
			out = append(out, canarySource{"event:" + e.EventID, text})
		}
	}
	return out, notes, nil
}

// addedText joins the lines a file's diff adds, without their "+" prefix.
// Context and removed lines are the repository's, not the agent's.
func addedText(f *fileDiff) string {
	var b strings.Builder
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			if strings.HasPrefix(l, "+") {
				b.WriteString(l[1:])
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// readArtifactText reads an exported artifact (relative to the run folder)
// up to maxCanaryArtifactBytes; a longer file is read in part.
func readArtifactText(rc *schemas.RunContext, rel string) (string, error) {
	if rc.Dir == "" {
		return "", fmt.Errorf("no run folder")
	}
	p, err := utils.ResolveInside(rc.Dir, rel)
	if err != nil {
		return "", err
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxCanaryArtifactBytes))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// promptText is the instruction text the agent was given: the task's
// instruction and every session's instruction, joined. The engine passes
// these files to the agent verbatim, so a stamped canary line is in the
// prompt exactly when one of them holds it.
func promptText(t *schemas.Task) (string, error) {
	if t == nil || t.Dir == "" {
		return "", fmt.Errorf("no task folder")
	}
	rels := []string{t.Instruction}
	for _, s := range t.Sessions {
		rels = append(rels, s.Instruction)
	}
	var b strings.Builder
	for _, rel := range rels {
		if rel == "" {
			continue
		}
		p, err := utils.ResolveInside(t.Dir, rel)
		if err != nil {
			return "", fmt.Errorf("instruction %s: %w", rel, err)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return "", fmt.Errorf("instruction %s: %w", rel, err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String(), nil
}
