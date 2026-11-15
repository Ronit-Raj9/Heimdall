package evidence

import (
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Proof classes and the matching rules behind them
// (docs/steps-and-evidence.md, "Proof classes"). BuildTimeline links each
// requested tool call to its witnesses; this file holds the helpers that
// decide what a request names (its command and paths), how a command line
// is compared, how an agent stream that numbers its tool calls on its own is
// paired with the gateway's calls, and which class a request ends in.

// requestHints returns what a requested call names for matching: its shell
// command (the Command hint, else the target) and its file paths (the Paths
// hint, else the target). Both are redacted like the observer records they
// are compared with.
func requestHints(tc schemas.ToolCallRef, target string) (string, []string) {
	cmd := target
	if tc.Command != "" {
		cmd = tc.Command
	}
	var paths []string
	for _, p := range strings.Split(tc.Paths, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	if target != "" {
		paths = append(paths, target)
	}
	return cmd, paths
}

// commandFits reports whether a process command line runs the requested
// command: the command, whitespace normalized, is part of the command line;
// failing that, the same holds with every quote and backslash removed from
// both, because a framework that wraps the command in its own shell call
// (Claude Code's eval '...') re-quotes it.
func commandFits(argv, want string) bool {
	w := strings.Join(strings.Fields(want), " ")
	if w == "" {
		return false
	}
	g := strings.Join(strings.Fields(argv), " ")
	if strings.Contains(g, w) {
		return true
	}
	uq := func(s string) string {
		return strings.Join(strings.Fields(strings.NewReplacer(`'`, "", `"`, "", `\`, "").Replace(s)), " ")
	}
	w = uq(w)
	return w != "" && strings.Contains(uq(g), w)
}

// pathFits reports whether a workspace-relative changed path is one of the
// requested paths: equal relative to /workspace, or a requested absolute
// path that ends in "/" plus the changed path.
func pathFits(changed string, paths []string) bool {
	for _, p := range paths {
		rel := relPath(p)
		if rel == "" || rel == "." {
			continue
		}
		if changed == rel || strings.HasSuffix(p, "/"+changed) {
			return true
		}
	}
	return false
}

// sortedInputs returns a shallow copy of ev whose witness lists are in time
// order (ledger by start then sequence, shim calls by time, processes by
// session, start and pid, file events by time), so the timeline and every
// link in it do not depend on the order the records were stored in. Agent
// stream events keep their order: it is the stream's own.
func sortedInputs(ev *schemas.Evidence) *schemas.Evidence {
	out := *ev
	out.ToolCalls = append([]schemas.ToolCall(nil), ev.ToolCalls...)
	sort.SliceStable(out.ToolCalls, func(a, b int) bool {
		x, y := out.ToolCalls[a], out.ToolCalls[b]
		if !x.Started.Equal(y.Started) {
			return x.Started.Before(y.Started)
		}
		return x.Seq < y.Seq
	})
	if ev.ToolCalls == nil {
		out.ToolCalls = nil
	}
	if ev.ShimLog != nil {
		out.ShimLog = append(out.ShimLog[:0:0], ev.ShimLog...)
		sort.SliceStable(out.ShimLog, func(a, b int) bool {
			ta, _ := time.Parse(time.RFC3339Nano, out.ShimLog[a].At)
			tb, _ := time.Parse(time.RFC3339Nano, out.ShimLog[b].At)
			return ta.Before(tb)
		})
	}
	if ev.Processes != nil {
		out.Processes = append([]schemas.ProcessRecord(nil), ev.Processes...)
		sort.SliceStable(out.Processes, func(a, b int) bool {
			x, y := out.Processes[a], out.Processes[b]
			if x.Session != y.Session {
				return x.Session < y.Session
			}
			if !x.Start.Equal(y.Start) {
				return x.Start.Before(y.Start)
			}
			return x.PID < y.PID
		})
	}
	if ev.FileEvents != nil {
		out.FileEvents = append([]schemas.FileEvent(nil), ev.FileEvents...)
		sort.SliceStable(out.FileEvents, func(a, b int) bool { return out.FileEvents[a].TS.Before(out.FileEvents[b].TS) })
	}
	return &out
}

// streamIDAliases pairs the tool calls of an agent stream that numbers them
// on its own (Codex's exec stream calls them item_1, item_2, ..., while the
// model's calls are call_...) with the gateway's requested calls, and
// returns stream id to gateway id. It returns nil when the gateway log is
// incomplete or any stream id is also a gateway id (one namespace, matched
// by id as before). A stream call pairs with the next unpaired gateway call
// whose command or path it names; when both sides hold the same number of
// calls, the calls left over pair in order. The pairing only joins the
// agent's own word to the request: it never makes a call proven.
func streamIDAliases(ev *schemas.Evidence) map[string]string {
	if ev.Requests == nil || !ev.Requests.Complete {
		return nil
	}
	type gwCall struct {
		id    string
		cmd   string
		paths []string
	}
	var gw []gwCall
	gwIDs := map[string]bool{}
	for _, r := range messageRowsInOrder(ev.Requests.Requests) {
		for _, tc := range r.ToolCalls {
			if tc.ID == "" || gwIDs[tc.ID] {
				continue
			}
			gwIDs[tc.ID] = true
			cmd, paths := requestHints(tc, tc.Target)
			gw = append(gw, gwCall{id: tc.ID, cmd: cmd, paths: paths})
		}
	}
	type stCall struct{ id, target string }
	var st []stCall
	stIDs := map[string]bool{}
	for _, e := range ev.Events {
		if e.Source != schemas.SourceAgentStream || e.Type != schemas.EventToolCall || e.ToolCallID == "" || stIDs[e.ToolCallID] {
			continue
		}
		if gwIDs[e.ToolCallID] {
			return nil
		}
		stIDs[e.ToolCallID] = true
		st = append(st, stCall{id: e.ToolCallID, target: e.Target})
	}
	if len(st) == 0 || len(gw) == 0 {
		return nil
	}
	out := map[string]string{}
	used := make([]bool, len(gw))
	next := 0
	for _, s := range st {
		if strings.TrimSpace(s.target) == "" {
			continue
		}
		for j := next; j < len(gw); j++ {
			g := gw[j]
			if used[j] {
				continue
			}
			if (g.cmd != "" && (commandFits(s.target, g.cmd) || commandFits(g.cmd, s.target))) || pathFits(relPath(s.target), g.paths) {
				out[s.id], used[j], next = g.id, true, j+1
				break
			}
		}
	}
	if len(st) == len(gw) {
		j := 0
		for _, s := range st {
			if _, ok := out[s.id]; ok {
				continue
			}
			for j < len(gw) && used[j] {
				j++
			}
			if j == len(gw) {
				break
			}
			out[s.id], used[j] = gw[j].id, true
		}
	}
	return out
}

// returnedResults returns the ids of tool calls whose result a model request
// carried back to the model (GatewayRequest.ToolResultIDs).
func returnedResults(ev *schemas.Evidence) map[string]bool {
	out := map[string]bool{}
	if ev.Requests == nil {
		return out
	}
	for _, r := range ev.Requests.Requests {
		for _, id := range r.ToolResultIDs {
			out[id] = true
		}
	}
	return out
}

// proofClass is the strongest witness of a request row's execution, in the
// order proxy, process, file effect, shim, result returned, none.
func proofClass(rows []row, req *row, returned map[string]bool) string {
	rank := map[string]int{
		schemas.StepSourceToolProxy: 1, schemas.StepSourceProcess: 2, schemas.StepSourceFS: 3,
		schemas.StepSourceShim: 4, schemas.StepSourceAgent: 5,
	}
	best := 6
	for _, w := range req.witnesses {
		if r, ok := rank[rows[w].Source]; ok && r < best {
			best = r
		}
	}
	if best == 6 && returned[req.ToolCallID] {
		best = 5
	}
	return []string{"", schemas.ProofProxy, schemas.ProofProcess, schemas.ProofFileEffect, schemas.ProofShim, schemas.ProofResultReturned, schemas.ProofNone}[best]
}
