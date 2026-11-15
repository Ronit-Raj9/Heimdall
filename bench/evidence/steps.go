package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// StepTimeline is the name of the step timeline source.
const StepTimeline = "step_timeline"

// Timeline matching windows. Every witness runs on the same host clock as
// the gateway, so the slack only absorbs write and poll delays.
const (
	stepSlack       = time.Second
	stepShimWindow  = 2 * time.Second
	stepFileLinger  = 500 * time.Millisecond
	stepRuntimeLead = 2 * time.Second
	stepSummaryMax  = 200
	stepFileMax     = 64 << 20
)

// shellToolRe matches tool names that run a command, for the conflict where
// only the agent says a command ran while the process observer was watching.
var shellToolRe = regexp.MustCompile(`(?i)(bash|shell|exec|command|terminal)`)

var _ schemas.EvidenceSource = (*stepTimelineSource)(nil)

// stepTimelineSource reads the trusted observers' files (processes.jsonl,
// fsevents.jsonl, observers.json) and joins every witness collected before
// it into one ordered timeline (steps.jsonl). It runs last, after the
// gateway, agent stream, shim log and tool ledger sources, and marks each
// requested tool call with how its execution is known.
type stepTimelineSource struct {
	logger schemas.Logger
}

// GetName implements schemas.Part.
func (s *stepTimelineSource) GetName() string { return StepTimeline }

// GetVersion implements schemas.Part.
func (s *stepTimelineSource) GetVersion() string { return version }

// Collect implements schemas.EvidenceSource. Missing observer files leave
// the fields nil (observers off, or a run recorded before them); a bad line
// is skipped.
func (s *stepTimelineSource) Collect(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) error {
	var errs []error
	if rc != nil && rc.Dir != "" {
		statuses, err := readObserverStatus(rc.Dir)
		if err != nil {
			errs = append(errs, err)
		}
		ev.Observers = statuses
		if procs, err := readJSONLFile[schemas.ProcessRecord](filepath.Join(rc.Dir, schemas.ProcessesFile)); err != nil {
			errs = append(errs, err)
		} else {
			ev.Processes = procs
		}
		if evs, err := readJSONLFile[schemas.FileEvent](filepath.Join(rc.Dir, schemas.FSEventsFile)); err != nil {
			errs = append(errs, err)
		} else {
			ev.FileEvents = evs
		}
	}
	steps, tools := BuildTimeline(ev)
	if len(steps) == 0 {
		return errors.Join(errs...)
	}
	ev.Steps, ev.RequestedTools = steps, tools
	if rc != nil {
		if err := writeJSONL(rc.Dir, schemas.StepsFile, steps); err != nil {
			errs = append(errs, fmt.Errorf("write steps for run %s: %w", runID(rc), err))
		}
	}
	return errors.Join(errs...)
}

// newStepTimelineSource returns the step_timeline source.
func newStepTimelineSource(deps Deps) *stepTimelineSource {
	return &stepTimelineSource{logger: deps.Logger}
}

func readObserverStatus(dir string) ([]schemas.ObserverStatus, error) {
	b, err := os.ReadFile(filepath.Join(dir, schemas.ObserversFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", schemas.ObserversFile, err)
	}
	var out []schemas.ObserverStatus
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("read %s: %w", schemas.ObserversFile, err)
	}
	return out, nil
}

func readJSONLFile[T any](p string) ([]T, error) {
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(io.LimitReader(f, stepFileMax)); err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(p), err)
	}
	out := []T{}
	for _, line := range bytes.Split(buf.Bytes(), []byte("\n")) {
		var r T
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &r) != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// row is a timeline row while it is built: links point at row indices and
// become sequence numbers once the rows are sorted.
type row struct {
	schemas.StepRecord
	order     int // tie break: model turns, then requests, then the rest
	witnessOf int // index + 1 of the counted row this row witnesses
	witnesses []int
	target    string
	reported  bool // tool request: an agent-stream witness exists
	proven    bool // tool request: a trusted witness exists
	root      int  // process rows: index + 1 of the command root
	end       time.Time
	linked    bool
	args      string
	server    string
	cmd       string   // tool request: the command it names (toolproof.go)
	paths     []string // tool request: the file paths it names
}

// turnSpan is one answered logical model request.
type turnSpan struct {
	step       int
	start, end time.Time
}

// BuildTimeline joins every witness in ev into one ordered timeline and
// returns it with the requested tool calls marked with how their execution
// is known (ToolCallRecord.Proof). It is pure: ev is not changed.
//
// Counting rule (agent_steps, docs/steps-and-evidence.md): each answered
// model turn counts once; each requested tool call with an execution
// witness counts once, and the witnesses linked to it (ledger row, shim
// call, command, file change, tool result) do not count again; a command
// tree the process observer saw counts once with all its descendants
// unless it is linked to a tool call; a file change counts once per path and
// turn, unless it is linked to a tool call or happened while a counted
// command ran. Rows from a witness that saw nothing new link to the counted
// row instead.
func BuildTimeline(ev *schemas.Evidence) ([]schemas.StepRecord, []schemas.ToolCallRecord) {
	if ev == nil {
		return nil, nil
	}
	ev = sortedInputs(ev) // order independence (toolproof.go)
	aliases, returned := streamIDAliases(ev), returnedResults(ev)
	var rows []row
	add := func(r row) int {
		rows = append(rows, r)
		return len(rows) - 1
	}
	turns, requestRows := gatewayRows(ev, add)
	gatewayComplete := ev.Requests != nil && ev.Requests.Complete
	turnOf := func(t time.Time) int {
		n := 0
		for _, ts := range turns {
			if !ts.end.After(t) {
				n = ts.step
			}
		}
		return n
	}
	window := func(step int) (time.Time, time.Time) {
		var lo, hi time.Time
		for i, ts := range turns {
			if ts.step == step {
				lo = ts.end.Add(-stepSlack)
				if i+1 < len(turns) {
					hi = turns[i+1].start.Add(stepSlack)
				}
			}
		}
		return lo, hi
	}
	inWindow := func(t, lo, hi time.Time) bool {
		return !t.Before(lo) && (hi.IsZero() || !t.After(hi))
	}
	requestedIDs := map[string]int{}
	for _, i := range requestRows {
		requestedIDs[rows[i].ToolCallID] = i
	}

	// Tool proxy ledger.
	var ledgerRows []int
	for _, c := range ev.ToolCalls {
		if !c.IsToolCall() { // listings, handshakes, notifications, opaque traffic: not tool calls (schemas/toolrows.go)
			continue
		}
		d := c.LatencyMS
		r := row{order: 3, args: c.ArgsSHA256, server: c.Server, end: c.Started.Add(time.Duration(d) * time.Millisecond)}
		r.TS, r.Kind, r.Source, r.Proof = c.Started, schemas.StepToolExecution, schemas.StepSourceToolProxy, schemas.EvidenceVerified
		r.ToolName, r.DurationMS, r.IsError = c.Tool, &d, c.IsError || c.Undelivered
		r.Summary = clip(c.Server+" "+c.Tool, stepSummaryMax)
		ledgerRows = append(ledgerRows, add(r))
	}
	// Shim log.
	var shimRows []int
	for _, c := range ev.ShimLog {
		if c.Action == "" {
			continue
		}
		ts, _ := time.Parse(time.RFC3339Nano, c.At)
		r := row{order: 3}
		r.TS, r.Kind, r.Source, r.Proof = ts, schemas.StepToolExecution, schemas.StepSourceShim, schemas.EvidenceVerified
		r.ToolName, r.ToolCallID, r.IsError = c.Tool, c.ToolCallID, c.Action == "crash"
		r.Summary = clip(utils.RedactSecrets(strings.TrimSpace(c.Tool+" "+c.Argv)), stepSummaryMax)
		shimRows = append(shimRows, add(r))
	}
	// Agent stream: tool results are the agent's own word that a call ran.
	var agentRows []int
	for _, e := range ev.Events {
		if e.Source != schemas.SourceAgentStream || (e.Type != schemas.EventToolResult && e.Type != schemas.EventToolCall) {
			continue
		}
		if id, ok := aliases[e.ToolCallID]; ok {
			e.ToolCallID = id // a stream with its own call ids, paired with the gateway's (toolproof.go)
		}
		if e.Type == schemas.EventToolCall {
			if _, ok := requestedIDs[e.ToolCallID]; ok || !gatewayComplete || e.ToolCallID == "" {
				continue
			}
			r := row{order: 3}
			r.TS, r.Kind, r.Source, r.Proof = e.TS, schemas.StepAgentEvent, schemas.StepSourceAgent, schemas.EvidencePartial
			r.ToolCallID, r.ToolName = e.ToolCallID, e.ToolName
			r.Summary = clip(utils.RedactSecrets(strings.TrimSpace("tool call "+e.ToolName+" "+e.Target)), stepSummaryMax)
			r.Conflict = "the agent stream reports a tool call the complete gateway log does not have"
			add(r)
			continue
		}
		r := row{order: 3}
		r.TS, r.Kind, r.Source, r.Proof = e.TS, schemas.StepToolExecution, schemas.StepSourceAgent, schemas.EvidencePartial
		r.ToolCallID, r.ToolName, r.IsError, r.DurationMS, r.ExitCode = e.ToolCallID, e.ToolName, e.IsError, e.DurationMS, e.ExitStatus
		r.Summary = clip(utils.RedactSecrets(strings.TrimSpace("tool result "+e.ToolName+" "+e.Summary)), stepSummaryMax)
		agentRows = append(agentRows, add(r))
	}
	// Process observer.
	procRows, roots := processRows(ev.Processes, turns, add)
	for i, root := range roots {
		rows[i].root = root + 1
	}
	// Workspace observer.
	var fileRows []int
	for _, f := range ev.FileEvents {
		r := row{order: 4}
		r.TS, r.Kind, r.Source, r.Proof = f.TS, schemas.StepFileChange, schemas.StepSourceFS, schemas.EvidenceVerified
		r.Path, r.Op, r.Session = f.Path, f.Op, f.Session
		r.Summary = clip(f.Op+" "+f.Path, stepSummaryMax)
		if f.From != "" {
			r.Summary = clip(f.Op+" "+f.From+" -> "+f.Path, stepSummaryMax)
		}
		fileRows = append(fileRows, add(r))
	}
	for i := range rows {
		if rows[i].Kind != schemas.StepModelTurn && rows[i].Kind != schemas.StepToolRequest && rows[i].Kind != schemas.StepModelAttempt {
			rows[i].Turn = turnOf(rows[i].TS)
		}
	}

	link := func(witness, to int) {
		rows[witness].witnessOf = to + 1
		rows[witness].linked = true
		rows[to].witnesses = append(rows[to].witnesses, witness)
	}
	// Requested tool calls to their execution witnesses.
	for _, ri := range requestRows {
		req := &rows[ri]
		lo, hi := window(req.Turn)
		for _, si := range shimRows {
			if !rows[si].linked && req.ToolCallID != "" && rows[si].ToolCallID == req.ToolCallID {
				link(si, ri)
				req.proven = true
			}
		}
		for _, ai := range agentRows {
			if !rows[ai].linked && req.ToolCallID != "" && rows[ai].ToolCallID == req.ToolCallID {
				link(ai, ri)
				req.reported = true
			}
		}
		if lj := matchLedgerRow(rows, ledgerRows, req, lo, hi, inWindow); lj >= 0 {
			link(lj, ri)
			req.proven = true
			for _, ai := range req.witnesses {
				if rows[ai].Source == schemas.StepSourceAgent && rows[ai].IsError != rows[lj].IsError {
					req.Conflict = fmt.Sprintf("the agent stream says error=%t, the tool proxy ledger says error=%t", rows[ai].IsError, rows[lj].IsError)
				}
			}
		}
		if req.cmd != "" {
			if pj := matchProcess(rows, procRows, req.cmd, lo, hi, inWindow); pj >= 0 {
				link(pj, ri)
				req.proven = true
				for _, k := range procRows {
					if rows[k].root == pj+1 && k != pj {
						rows[k].witnessOf, rows[k].linked = ri+1, true
					}
				}
			}
		}
		for _, fj := range fileRows {
			if !rows[fj].linked && pathFits(rows[fj].Path, req.paths) && inWindow(rows[fj].TS, lo, hi) {
				link(fj, ri)
				req.proven = true
			}
		}
		switch {
		case req.proven:
			req.Execution = schemas.ExecutedWithProof
		case req.reported:
			req.Execution = schemas.ExecutedAgentReported
			if req.cmd != "" && shellToolRe.MatchString(req.ToolName) && observed(ev.Observers, schemas.ObserverProc) {
				req.Conflict = "only the agent stream says this command ran; the process observer saw no matching command"
			}
		default:
			req.Execution = schemas.ExecutionNoWitness
		}
		req.Counted = req.Execution != schemas.ExecutionNoWitness
		req.ProofClass = proofClass(rows, req, returned) // strongest witness (toolproof.go)
	}
	// Witnesses not linked to a request: a shim call is a witness of the
	// command it ran in; the rest count on their own.
	for _, si := range shimRows {
		if rows[si].linked {
			continue
		}
		for _, pj := range procRows {
			p := rows[pj]
			if p.Op == "runtime" || len(p.args) == 0 || path.Base(p.args) != rows[si].ToolName {
				continue
			}
			if d := rows[si].TS.Sub(p.TS); d > -stepShimWindow && d < stepShimWindow {
				to := p.root - 1
				if p.witnessOf > 0 {
					to = p.witnessOf - 1
				}
				link(si, to)
				break
			}
		}
	}
	for _, i := range append(append(append([]int{}, ledgerRows...), shimRows...), agentRows...) {
		if !rows[i].linked {
			rows[i].Counted = true
		}
	}
	for _, pj := range procRows {
		p := &rows[pj]
		switch {
		case p.Op == "runtime" || p.linked:
		case p.root == pj+1:
			p.Counted = true
		default:
			p.witnessOf = p.root
		}
	}
	// File changes: inside a counted command's lifetime they are its effect;
	// otherwise one counted change per path and turn.
	firstOf := map[string]int{}
	for _, fj := range fileRows {
		f := &rows[fj]
		if f.linked {
			continue
		}
		for _, pj := range procRows {
			p := rows[pj]
			if p.root != pj+1 || p.Op == "runtime" || (!p.Counted && p.witnessOf == 0) {
				continue
			}
			if !f.TS.Before(p.TS) && !f.TS.After(p.end.Add(stepFileLinger)) {
				to := pj
				if p.witnessOf > 0 {
					to = p.witnessOf - 1
				}
				link(fj, to)
				break
			}
		}
		if f.linked {
			continue
		}
		key := fmt.Sprintf("%d\x00%d\x00%s", f.Session, f.Turn, f.Path)
		if first, ok := firstOf[key]; ok {
			link(fj, first)
			continue
		}
		firstOf[key] = fj
		f.Counted = true
	}
	return joinReported(finishRows(rows), ev), markRequested(ev.RequestedTools, rows, requestRows) // Agent events area: reported rows (stepsreported.go)
}

// gatewayRows adds a model turn row per logical model request, an attempt
// row per retry or fallback, and a request row per requested tool call.
func gatewayRows(ev *schemas.Evidence, add func(row) int) ([]turnSpan, []int) {
	if ev.Requests == nil {
		return nil, nil
	}
	rows := messageRowsInOrder(ev.Requests.Requests)
	parents := map[string]string{}
	for _, r := range rows {
		if r.ID != "" {
			parents[r.ID] = r.ParentID
		}
	}
	type logical struct {
		step       int
		rows       []schemas.GatewayRequest
		start, end time.Time
		ok         bool
	}
	var order []string
	byRoot := map[string]*logical{}
	for i, r := range rows {
		root := rootOf(r, parents)
		if root == "" {
			root = fmt.Sprintf("request:%d", i)
		}
		l, ok := byRoot[root]
		if !ok {
			l = &logical{step: len(order) + 1, start: r.Timestamp}
			byRoot[root] = l
			order = append(order, root)
		}
		l.rows = append(l.rows, r)
		end := r.Timestamp.Add(time.Duration(r.LatencyMS * float64(time.Millisecond)))
		if r.Status == statusSuccess {
			l.ok = true
			if end.After(l.end) {
				l.end = end
			}
		}
	}
	var turns []turnSpan
	var requests []int
	seenTool := map[string]bool{}
	for _, root := range order {
		l := byRoot[root]
		last := l.rows[len(l.rows)-1]
		r := row{order: 1}
		r.TS, r.Kind, r.Source, r.Proof = l.start, schemas.StepModelTurn, schemas.StepSourceGateway, schemas.EvidenceStrong
		r.Turn, r.RequestID, r.Counted, r.IsError = l.step, root, l.ok, !l.ok
		if l.ok {
			d := l.end.Sub(l.start).Milliseconds()
			r.DurationMS = &d
			turns = append(turns, turnSpan{step: l.step, start: l.start, end: l.end})
		}
		r.Summary = clip(fmt.Sprintf("model turn %d: %s (%s)", l.step, last.ModelServed, last.Status), stepSummaryMax)
		add(r)
		for ri, gr := range l.rows {
			trail := len(gr.Attempts)
			for ai := 0; ai < trail; ai++ {
				if ri == 0 && ai == 0 {
					continue
				}
				a := gr.Attempts[ai]
				ar := row{order: 2}
				ar.TS, ar.Kind, ar.Source, ar.Proof = gr.Timestamp, schemas.StepModelAttempt, schemas.StepSourceGateway, schemas.EvidenceStrong
				ar.Turn, ar.RequestID, ar.Attempt, ar.IsError = l.step, gr.ID, a.N, a.StatusCode >= 400 || a.FailReason != ""
				kind := "retry"
				if gr.FallbackIndex > 0 {
					kind = "fallback"
				}
				ar.Summary = clip(fmt.Sprintf("%s attempt %d on %s: %d %s", kind, a.N, gr.Provider, a.StatusCode, a.FailReason), stepSummaryMax)
				add(ar)
			}
			if trail == 0 && ri > 0 {
				ar := row{order: 2}
				ar.TS, ar.Kind, ar.Source, ar.Proof = gr.Timestamp, schemas.StepModelAttempt, schemas.StepSourceGateway, schemas.EvidenceStrong
				ar.Turn, ar.RequestID, ar.Attempt, ar.IsError = l.step, gr.ID, 1, gr.Status != statusSuccess
				ar.Summary = clip(fmt.Sprintf("fallback on %s %s (%s)", gr.Provider, gr.ModelServed, gr.Status), stepSummaryMax)
				add(ar)
			}
			for ci, tc := range gr.ToolCalls {
				id := tc.ID
				if id == "" {
					id = fmt.Sprintf("%s:tool:%d", root, ci)
				}
				if seenTool[id] {
					continue
				}
				seenTool[id] = true
				tr := row{order: 2, target: utils.RedactSecrets(tc.Target), args: tc.ArgsHash}
				tr.cmd, tr.paths = requestHints(tc, tr.target)
				tr.TS, tr.Kind, tr.Source, tr.Proof = l.end, schemas.StepToolRequest, schemas.StepSourceGateway, schemas.EvidenceStrong
				if tr.TS.IsZero() {
					tr.TS = gr.Timestamp
				}
				tr.Turn, tr.RequestID, tr.ToolCallID, tr.ToolName = l.step, gr.ID, id, tc.Name
				tr.Summary = clip(strings.TrimSpace(tc.Name+" "+tr.target), stepSummaryMax)
				requests = append(requests, add(tr))
			}
		}
	}
	return turns, requests
}

// processRows adds one row per observed process and marks the agent's
// runtime and each command tree's root (row.root). Runtime processes are
// each session's PID 1 and the processes that started before the first
// model turn ended and still ran when the last one ended (without model
// turns: started within 2 s of the session's first process and running at
// its end). A command root is a process whose parent is runtime or unseen.
func processRows(procs []schemas.ProcessRecord, turns []turnSpan, add func(row) int) ([]int, map[int]int) {
	var firstEnd, lastEnd time.Time
	if len(turns) > 0 {
		firstEnd, lastEnd = turns[0].end, turns[len(turns)-1].end
	}
	type key struct{ session, pid int }
	first := map[int]time.Time{}
	for _, p := range procs {
		if t, ok := first[p.Session]; !ok || p.Start.Before(t) {
			first[p.Session] = p.Start
		}
	}
	var idx []int
	byPID := map[key]int{}
	runtime := map[int]bool{}
	for _, p := range procs {
		r := row{order: 3, end: p.End}
		r.TS, r.Kind, r.Source, r.Proof = p.Start, schemas.StepCommand, schemas.StepSourceProcess, schemas.EvidenceVerified
		r.PID, r.PPID, r.Session, r.ExitCode = p.PID, p.PPID, p.Session, p.ExitCode
		if !p.End.IsZero() {
			d := p.End.Sub(p.Start).Milliseconds()
			r.DurationMS = &d
		}
		if len(p.Argv) > 0 {
			r.args = p.Argv[0]
		}
		r.target = strings.Join(p.Argv, " ")
		text := r.target
		if text == "" {
			text = p.Comm
		}
		r.Summary = clip(text, stepSummaryMax)
		var rt bool
		switch {
		case p.PID == 1:
			rt = true
		case len(turns) > 0:
			rt = p.Start.Before(firstEnd) && (p.RunningAtEnd || !p.End.Before(lastEnd))
		default:
			rt = p.Start.Sub(first[p.Session]) < stepRuntimeLead && p.RunningAtEnd
		}
		if rt {
			r.Op = "runtime"
		}
		i := add(r)
		idx = append(idx, i)
		byPID[key{p.Session, p.PID}] = len(idx) - 1
		runtime[len(idx)-1] = rt
	}
	// Roots: walk parents (processes arrive in start order, so a parent's
	// root is known before its children's).
	roots := map[int]int{}
	rootOfIdx := make([]int, len(idx))
	for n, p := range procs {
		if runtime[n] {
			continue
		}
		parent, ok := byPID[key{p.Session, p.PPID}]
		if !ok || runtime[parent] || parent >= n {
			rootOfIdx[n] = n
		} else {
			rootOfIdx[n] = rootOfIdx[parent]
		}
		roots[idx[n]] = idx[rootOfIdx[n]]
	}
	return idx, roots
}

// matchLedgerRow returns the first unlinked ledger row in the window whose
// tool name fits the request (args hash equal wins), or -1.
func matchLedgerRow(rows []row, ledger []int, req *row, lo, hi time.Time, inWindow func(t, lo, hi time.Time) bool) int {
	best := -1
	for _, j := range ledger {
		c := rows[j]
		if c.linked || !inWindow(c.TS, lo, hi) || !toolNameFits(req.ToolName, c.server, c.ToolName) {
			continue
		}
		if req.args != "" && c.args == req.args {
			return j
		}
		if best < 0 {
			best = j
		}
	}
	return best
}

// toolNameFits reports whether a model-side tool name names the proxied
// server's tool: frameworks prefix MCP tools ("mcp__docs__search",
// "docs.search", "docs_search").
func toolNameFits(requested, server, tool string) bool {
	if requested == tool {
		return true
	}
	for _, sep := range []string{"__", ".", "/", "_", ":"} {
		if strings.HasSuffix(requested, sep+tool) && (server == "" || strings.Contains(requested, server)) {
			return true
		}
	}
	return false
}

// matchProcess returns the first unlinked command root in the window whose
// command line holds the requested command, or -1.
func matchProcess(rows []row, procs []int, target string, lo, hi time.Time, inWindow func(t, lo, hi time.Time) bool) int {
	want := strings.Join(strings.Fields(target), " ")
	if want == "" {
		return -1
	}
	for _, j := range procs {
		p := rows[j]
		if p.linked || p.Op == "runtime" || p.root != j+1 || !inWindow(p.TS, lo, hi) {
			continue
		}
		if commandFits(p.target, want) {
			return j
		}
	}
	return -1
}

// relPath turns a requested file path into a workspace-relative one.
func relPath(target string) string {
	t := strings.TrimSpace(target)
	if strings.ContainsAny(t, " \n\t|;&") {
		return ""
	}
	t = strings.TrimPrefix(t, "/workspace/")
	t = strings.TrimPrefix(t, "./")
	return path.Clean(t)
}

func observed(statuses []schemas.ObserverStatus, observer string) bool {
	for _, s := range statuses {
		if s.Observer == observer && s.State == schemas.ObserverOn {
			return true
		}
	}
	return false
}

// finishRows sorts the rows by time, numbers them and turns index links
// into sequence numbers.
func finishRows(rows []row) []schemas.StepRecord {
	if len(rows) == 0 {
		return nil
	}
	order := make([]int, len(rows))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ra, rb := rows[order[a]], rows[order[b]]
		if !ra.TS.Equal(rb.TS) {
			return ra.TS.Before(rb.TS)
		}
		return ra.order < rb.order
	})
	seq := make([]int, len(rows))
	for n, i := range order {
		seq[i] = n + 1
	}
	out := make([]schemas.StepRecord, len(rows))
	for n, i := range order {
		r := rows[i].StepRecord
		r.Seq = n + 1
		if rows[i].witnessOf > 0 {
			r.WitnessOf = seq[rows[i].witnessOf-1]
		}
		for _, w := range rows[i].witnesses {
			r.WitnessedBy = append(r.WitnessedBy, seq[w])
		}
		sort.Ints(r.WitnessedBy)
		r.TS = r.TS.UTC()
		// Every string can carry agent-chosen text; none may reach a sealed
		// file looking like a credential.
		r.Summary, r.Path, r.ToolName = utils.RedactSecrets(r.Summary), utils.RedactSecrets(r.Path), utils.RedactSecrets(r.ToolName)
		out[n] = r
	}
	return out
}

// markRequested copies each request row's execution state onto the
// requested tool call records.
func markRequested(recs []schemas.ToolCallRecord, rows []row, requests []int) []schemas.ToolCallRecord {
	if len(recs) == 0 {
		return recs
	}
	byID := map[string]row{}
	for _, i := range requests {
		byID[rows[i].ToolCallID] = rows[i]
	}
	out := make([]schemas.ToolCallRecord, len(recs))
	copy(out, recs)
	for i := range out {
		r, ok := byID[out[i].ToolCallID]
		if !ok {
			continue
		}
		out[i].Proof = r.Execution
		out[i].ProofClass = r.ProofClass
		switch r.Execution {
		case schemas.ExecutedWithProof:
			out[i].Executed = true
			out[i].Evidence = schemas.EvidenceVerified
			out[i].ExecutionSource = executionOrigin(rows, r)
		case schemas.ExecutedAgentReported:
			out[i].Executed = true
			out[i].Evidence = schemas.EvidencePartial
			out[i].ExecutionSource = schemas.EvidenceSourceAgentStream
		default:
			out[i].UnknownReason = "no witness of execution"
		}
	}
	return out
}

// executionOrigin names the strongest witness of a request's execution.
func executionOrigin(rows []row, r row) schemas.EvidenceOrigin {
	for _, w := range r.witnesses {
		switch rows[w].Source {
		case schemas.StepSourceToolProxy:
			return schemas.EvidenceSourceToolRunner
		case schemas.StepSourceShim:
			return schemas.EvidenceSourceToolRunner
		case schemas.StepSourceProcess:
			return schemas.EvidenceSourceProcessResult
		case schemas.StepSourceFS:
			return schemas.EvidenceSourceWorkspaceDiff
		}
	}
	return schemas.EvidenceSourceUnknown
}
