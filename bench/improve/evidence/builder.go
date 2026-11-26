// Package evidence builds the dev-only evidence an improver reads (design
// A3.1): a digest of each practice run, failure clusters and the raw
// transcripts, with every protected text kept out.
package evidence

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/improve"
)

func init() { improve.DefaultEvidenceBuilder = Builder{} }

// Builder is the default improve.EvidenceBuilder.
type Builder struct{}

var _ improve.EvidenceBuilder = Builder{}

// ErrProtectedRun is returned when the runner passes a run of an audit or
// sealed task. It is a guard, not a filter: the round fails.
var ErrProtectedRun = errors.New("the evidence for the improver included a run of a held-out task, so this round stopped")

// DefaultLimits are the caps of A3.1.
var DefaultLimits = improve.EvidenceLimits{
	DigestTokensPerRun: 6000,
	DigestTokensTotal:  60000,
	RawBytesPerRun:     2 << 20,
	RawBytesTotal:      50 << 20,
	Clusters:           6,
	Exemplars:          2,
}

const (
	argsClip   = 200
	errorClip  = 2000
	finalLines = 300
	maxErrors  = 5
)

// evidenceFile is evidence.json.
type evidenceFile struct {
	Split      string                 `json:"split"`
	Round      int                    `json:"round"`
	Clusters   []cluster              `json:"clusters"`
	Items      []improve.EvidenceItem `json:"items"`
	PruneHints []improve.Edit         `json:"prune_hints,omitempty"`
	Limits     improve.Limits         `json:"limits"`
}

type cluster struct {
	ID        string   `json:"id"`
	Key       string   `json:"key"`
	Lost      float64  `json:"lost"`
	Tasks     []string `json:"tasks"`
	Runs      []string `json:"runs"`
	Exemplars []string `json:"exemplars"`
	Contrast  string   `json:"contrast,omitempty"`
}

// event is the part of an events.jsonl line the builder reads.
type event struct {
	Type       string `json:"type"`
	Seq        int    `json:"seq"`
	TS         string `json:"ts"`
	ToolName   string `json:"tool_name"`
	Target     string `json:"target"`
	Summary    string `json:"summary"`
	IsError    bool   `json:"is_error"`
	ToolCallID string `json:"tool_call_id"`
	Source     string `json:"source"`
}

// step is one tool call with its result.
type step struct {
	I       int
	Tool    string
	Args    string // cleaned, full
	Result  string // cleaned result summary
	Exit    int
	S       float64
	Error   bool
	callTS  string
	resTS   string
	hasExit bool
}

// run is one loaded, cleaned practice run.
type run struct {
	ref     improve.RunRef
	label   string // dev:<task>#<trial>/<arm letter>
	folder  string // runs/<task>/<trial>/<arm letter>
	item    improve.EvidenceItem
	steps   []step
	instr   string
	diff    string
	raw     []byte // transcript, cleaned
	errSig  string
	last    string // last tool name
	failing int    // index into steps, -1 when none
}

// Build reads the sealed folders of the dev runs in req and returns the
// evidence document. A run of a protected task fails the whole build.
func (Builder) Build(ctx context.Context, req improve.EvidenceRequest) (*improve.EvidenceDoc, error) {
	lim := withDefaults(req.Limits)
	prot := map[string]bool{}
	for _, id := range req.Prot.TaskIDs {
		prot[id] = true
	}
	for i, ref := range req.Runs {
		if prot[ref.Task] {
			return nil, ErrProtectedRun
		}
		if ref.Dir == "" {
			return nil, fmt.Errorf("practice run %d of the evidence has no folder", i+1)
		}
	}
	sc := newScrubber(req.Prot)
	seen := map[string]int{}
	var runs []*run
	for _, ref := range req.Runs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, leak, err := load(ref, sc, req.Playbook)
		if err != nil {
			return nil, err
		}
		if leak {
			log.Printf("evidence: practice run %s of task %s held protected text and was left out", ref.RunID, ref.Task)
			continue
		}
		letter := armLetter(ref.Arm)
		base := fmt.Sprintf("dev:%s#%d/%s", ref.Task, ref.Trial, letter)
		seen[base]++
		if n := seen[base]; n > 1 {
			letter = fmt.Sprintf("%s%d", letter, n)
			base = fmt.Sprintf("dev:%s#%d/%s", ref.Task, ref.Trial, letter)
		}
		r.label = base
		r.folder = fmt.Sprintf("runs/%s/%d/%s", safeName(ref.Task), ref.Trial, letter)
		r.item.RunRef = r.label
		r.item.Digest = r.folder + "/digest.md"
		r.item.Transcript = r.folder + "/transcript.jsonl"
		runs = append(runs, r)
	}
	markNoisy(runs)
	markFlips(runs)
	clusters := buildClusters(runs, lim)

	doc := &improve.EvidenceDoc{Files: map[string][]byte{}}
	rawTotal := 0
	for _, r := range runs {
		doc.Files[r.folder+"/digest.md"] = []byte(digest(r, lim.DigestTokensPerRun*4))
		if r.diff != "" {
			doc.Files[r.folder+"/diff.patch"] = []byte(r.diff)
		}
		raw := clipLines(r.raw, lim.RawBytesPerRun)
		if rawTotal+len(raw) > lim.RawBytesTotal {
			r.item.Transcript = ""
			continue
		}
		rawTotal += len(raw)
		doc.Files[r.folder+"/transcript.jsonl"] = raw
	}

	ev := evidenceFile{Split: "dev", Round: req.Round, Clusters: clusters, Limits: improve.DefaultLimits}
	if ev.Clusters == nil {
		ev.Clusters = []cluster{}
	}
	ev.Items = make([]improve.EvidenceItem, 0, len(runs))
	for _, r := range runs {
		ev.Items = append(ev.Items, r.item)
	}
	ev.PruneHints = pruneHints(req, runs, sc)
	body, err := capEvidence(&ev, lim.DigestTokensTotal*4)
	if err != nil {
		return nil, err
	}
	doc.EvidenceJSON = body
	doc.Items = ev.Items

	hist := make([]improve.HistoryEntry, len(req.History))
	for i, h := range req.History {
		h.Sentence = sc.clean(h.Sentence)
		h.Edits = cleanAll(sc, h.Edits)
		hist[i] = h
	}
	if doc.Files["history.json"], err = json.MarshalIndent(hist, "", "  "); err != nil {
		return nil, err
	}
	if doc.Files["limits.json"], err = json.MarshalIndent(improve.DefaultLimits, "", "  "); err != nil {
		return nil, err
	}
	return doc, nil
}

// pruneHints are the playbook's prune edits for this round, cleaned.
func pruneHints(req improve.EvidenceRequest, runs []*run, sc *scrubber) []improve.Edit {
	if pruneHook == nil {
		return nil
	}
	opened := map[string]int{}
	for _, r := range runs {
		for _, p := range r.item.Opened {
			opened[p] = req.Round
		}
	}
	hints := pruneHook(req.Playbook, opened, req.Round)
	for i := range hints {
		hints[i].Reason = sc.clean(hints[i].Reason)
		hints[i].Text = sc.clean(hints[i].Text)
	}
	return hints
}

// pruneHook is improve.PruneHints; a variable so the evidence never
// depends on a playbook function the improve package may not have.
var pruneHook = improve.PruneHints

// capEvidence marshals ev, dropping items to their first five fields from
// the last one back until it fits in max bytes.
func capEvidence(ev *evidenceFile, max int) ([]byte, error) {
	for i := len(ev.Items); ; i-- {
		b, err := json.MarshalIndent(ev, "", "  ")
		if err != nil || len(b) <= max || i == 0 {
			return b, err
		}
		it := ev.Items[i-1]
		ev.Items[i-1] = improve.EvidenceItem{Task: it.Task, Trial: it.Trial, Outcome: it.Outcome, Success: it.Success, Gates: it.Gates}
	}
}

func withDefaults(l improve.EvidenceLimits) improve.EvidenceLimits {
	d := DefaultLimits
	if l.DigestTokensPerRun > 0 {
		d.DigestTokensPerRun = l.DigestTokensPerRun
	}
	if l.DigestTokensTotal > 0 {
		d.DigestTokensTotal = l.DigestTokensTotal
	}
	if l.RawBytesPerRun > 0 {
		d.RawBytesPerRun = l.RawBytesPerRun
	}
	if l.RawBytesTotal > 0 {
		d.RawBytesTotal = l.RawBytesTotal
	}
	if l.Clusters > 0 {
		d.Clusters = l.Clusters
	}
	if l.Exemplars > 0 {
		d.Exemplars = l.Exemplars
	}
	return d
}

func armLetter(arm int) string {
	if arm >= 0 && arm < 26 {
		return string(rune('A' + arm))
	}
	return "X" + strconv.Itoa(arm)
}

// safeName keeps a task id usable as one folder name.
func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '_'
		}
		return r
	}, strings.TrimLeft(s, "."))
}

func cleanAll(sc *scrubber, l []string) []string {
	if l == nil {
		return nil
	}
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = sc.clean(s)
	}
	return out
}

// load reads one sealed run folder. leak is true when any text of the run
// holds a protected secret; the run is then left out whole.
func load(ref improve.RunRef, base *scrubber, pb improve.PlaybookObject) (*run, bool, error) {
	read := func(name string) []byte {
		b, _ := os.ReadFile(filepath.Join(ref.Dir, name))
		return b
	}
	var rj struct {
		Outcome string `json:"outcome"`
		Y       struct {
			V *float64 `json:"v"`
		} `json:"y"`
	}
	if err := json.Unmarshal(read("run.json"), &rj); err != nil {
		return nil, false, fmt.Errorf("practice run %s has no readable result", ref.RunID)
	}
	var plan struct {
		Env   map[string]string `json:"env"`
		Files map[string]string `json:"files"`
	}
	_ = json.Unmarshal(read("plan.json"), &plan)
	var lit []string
	for _, v := range plan.Env {
		if len(v) >= 8 && v != "[redacted]" {
			lit = append(lit, v)
		}
	}
	slices.Sort(lit)
	sc := base.withLiteral(lit)

	var grade struct {
		Passed     bool               `json:"passed"`
		TestsRan   int                `json:"tests_ran"`
		Failures   int                `json:"failures"`
		Errors     int                `json:"errors"`
		Dimensions map[string]float64 `json:"dimensions"`
	}
	_ = json.Unmarshal(read("grade.json"), &grade)

	instrPath := plan.Env["BENCH_INSTRUCTION_FILE"]
	instrRaw := plan.Files[instrPath]
	if instrRaw == "" {
		for _, k := range sortedKeys(plan.Files) {
			if filepath.Base(k) == "instruction.md" {
				instrRaw = plan.Files[k]
				break
			}
		}
	}
	eventsRaw := read("events.jsonl")
	streamRaw := read("agent.stream.jsonl")
	stderrRaw := string(read("agent.stderr.txt"))
	diffRaw := string(read("workspace.diff"))

	for _, t := range []string{instrRaw, string(eventsRaw), string(streamRaw), stderrRaw, diffRaw} {
		if sc.leaks(t) {
			return nil, true, nil
		}
	}

	r := &run{ref: ref, failing: -1}
	r.instr = sc.clean(instrRaw)
	r.diff = sc.clean(diffRaw)
	r.steps = readSteps(eventsRaw, sc)
	for _, t := range []string{r.instr, r.diff} {
		if sc.leaks(t) {
			return nil, true, nil
		}
	}
	for _, s := range r.steps {
		if sc.leaks(s.Args) || sc.leaks(s.Result) {
			return nil, true, nil
		}
	}

	success := 0.0
	if rj.Y.V != nil {
		success = *rj.Y.V
	} else if rj.Outcome == "pass" {
		success = 1
	}
	it := improve.EvidenceItem{Task: ref.Task, Trial: ref.Trial, Outcome: rj.Outcome, Success: success, Version: 0}
	it.Arm = "candidate"
	if ref.Arm == 0 {
		it.Arm = "incumbent"
	}
	it.Instruction = r.instr
	it.Checks = []improve.EvidenceCheck{{Name: "tests", Passed: grade.Passed}}
	for _, k := range sortedKeys(grade.Dimensions) {
		it.Checks = append(it.Checks, improve.EvidenceCheck{Name: sc.clean(k), Passed: grade.Dimensions[k] >= 1})
	}
	it.TestsRan = grade.TestsRan
	it.TestsFailed = grade.Failures + grade.Errors
	it.Steps = len(r.steps)
	it.StopReason = stopReason(rj.Outcome, len(r.steps), plan.Env)
	it.CostUSD, it.Tokens = readCost(read("cost.json"))
	it.WallS = readWall(read("metrics.json"))

	for _, s := range r.steps {
		it.ToolCalls = append(it.ToolCalls, improve.EvidenceToolCall{I: s.I, Tool: s.Tool, Args: clip(s.Args, argsClip), Exit: s.Exit, S: s.S, Error: s.Error})
		if s.Error {
			it.Errors = append(it.Errors, clip(s.Result, errorClip))
		}
		if isBundleRead(s.Args, pb) {
			it.Opened = appendOnce(it.Opened, bundlePath(s.Args, pb))
		}
	}
	if st := strings.TrimSpace(sc.clean(stderrRaw)); st != "" {
		it.Errors = append(it.Errors, clip(st, errorClip))
	}
	if len(it.Errors) > maxErrors {
		it.Errors = it.Errors[len(it.Errors)-maxErrors:]
	}
	for i := len(r.steps) - 1; i >= 0; i-- {
		if r.steps[i].Error {
			r.failing = i
			s := r.steps[i]
			it.FailingStep = &improve.EvidenceStep{I: s.I, Tool: s.Tool, Args: clip(s.Args, argsClip), ErrorText: clip(s.Result, errorClip)}
			break
		}
	}
	if len(r.steps) > 0 {
		r.last = r.steps[len(r.steps)-1].Tool
	}
	if r.diff != "" {
		it.Final = &improve.EvidenceFinal{Kind: "diff", Text: clipLineCount(r.diff, finalLines)}
	}
	for _, p := range pb.Items {
		if p.File == improve.InstructionsKey || slices.Contains(it.Opened, p.File) {
			it.ItemsInContext = append(it.ItemsInContext, p.ID)
		}
	}
	if len(it.Errors) > 0 {
		r.errSig = signature(it.Errors[0])
	}
	r.item = it
	r.raw = transcript(eventsRaw, streamRaw, sc)
	if sc.leaks(string(r.raw)) || sc.leaks(stderrRaw) {
		return nil, true, nil
	}
	return r, false, nil
}

// readSteps pairs tool calls with their results. When the agent's own
// stream recorded tool calls, the gateway's copies of them are skipped.
func readSteps(raw []byte, sc *scrubber) []step {
	var evs []event
	native := false
	sca := bufio.NewScanner(bytes.NewReader(raw))
	sca.Buffer(make([]byte, 1<<20), 16<<20)
	for sca.Scan() {
		var e event
		if json.Unmarshal(sca.Bytes(), &e) != nil {
			continue
		}
		if e.Type == "tool_call" && e.Source != "" && e.Source != "gateway" {
			native = true
		}
		evs = append(evs, e)
	}
	var calls []*step
	byID := map[string]*step{}
	for _, e := range evs {
		if native && e.Source == "gateway" {
			continue
		}
		switch e.Type {
		case "tool_call":
			s := &step{I: len(calls) + 1, Tool: sc.clean(e.ToolName), Args: sc.clean(e.Target), callTS: e.TS}
			calls = append(calls, s)
			if e.ToolCallID != "" {
				byID[e.ToolCallID] = s
			}
		case "tool_result":
			s := byID[e.ToolCallID]
			if s == nil {
				continue
			}
			s.Result = sc.clean(e.Summary)
			s.resTS = e.TS
			s.Exit, s.hasExit = parseExit(e.Summary)
			s.Error = e.IsError || (s.hasExit && s.Exit != 0)
			s.S = seconds(s.callTS, s.resTS)
		}
	}
	out := make([]step, len(calls))
	for i, s := range calls {
		out[i] = *s
	}
	return out
}

func parseExit(summary string) (int, bool) {
	rest, ok := strings.CutPrefix(summary, "exit ")
	if !ok {
		return 0, false
	}
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	if end < 0 {
		end = len(rest)
	}
	n, err := strconv.Atoi(rest[:end])
	return n, err == nil
}

func seconds(a, b string) float64 {
	ta, e1 := time.Parse(time.RFC3339Nano, a)
	tb, e2 := time.Parse(time.RFC3339Nano, b)
	if e1 != nil || e2 != nil || tb.Before(ta) {
		return 0
	}
	return float64(tb.Sub(ta).Milliseconds()) / 1000
}

func stopReason(outcome string, steps int, env map[string]string) string {
	switch outcome {
	case "timeout", "budget", "crash", "refusal":
		return outcome
	}
	for _, k := range []string{"BENCH_MAX_TURNS", "AGENT_MAX_STEPS"} {
		if n, err := strconv.Atoi(env[k]); err == nil && n > 0 && steps >= n {
			return "max_turns"
		}
	}
	return ""
}

func readCost(b []byte) (float64, *improve.EvidenceTokens) {
	var rows []struct {
		Tokens struct {
			Fresh     int `json:"fresh"`
			CacheRead int `json:"cache_read"`
			Out       int `json:"out"`
		} `json:"tokens"`
		USD struct {
			V float64 `json:"v"`
		} `json:"usd"`
	}
	if json.Unmarshal(b, &rows) != nil || len(rows) == 0 {
		return 0, nil
	}
	var usd float64
	t := &improve.EvidenceTokens{}
	for _, r := range rows {
		usd += r.USD.V
		t.Input += r.Tokens.Fresh + r.Tokens.CacheRead
		t.Output += r.Tokens.Out
		t.Cached += r.Tokens.CacheRead
	}
	return usd, t
}

func readWall(b []byte) float64 {
	var m struct {
		Wall struct {
			V float64 `json:"v"`
		} `json:"wall_time_s"`
	}
	_ = json.Unmarshal(b, &m)
	return m.Wall.V
}

// isBundleRead reports whether a tool call target reads a bundle path: a
// skill file or a file a playbook item lives in.
func isBundleRead(target string, pb improve.PlaybookObject) bool {
	return bundlePath(target, pb) != ""
}

func bundlePath(target string, pb improve.PlaybookObject) string {
	for _, p := range pb.Items {
		if p.File != "" && p.File != improve.InstructionsKey && strings.Contains(target, p.File) {
			return p.File
		}
	}
	for _, f := range strings.Fields(target) {
		f = strings.Trim(f, `"'`)
		if strings.HasSuffix(f, "/SKILL.md") {
			return f
		}
	}
	return ""
}

func appendOnce(l []string, s string) []string {
	if s == "" || slices.Contains(l, s) {
		return l
	}
	return append(l, s)
}

// transcript is events.jsonl and agent.stream.jsonl merged by timestamp
// (stream lines without one keep their order after the events of the same
// time), each line cleaned.
func transcript(events, stream []byte, sc *scrubber) []byte {
	type line struct {
		ts  string
		src int
		n   int
		b   string
	}
	var lines []line
	add := func(raw []byte, src int) {
		for i, l := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(l) == "" {
				continue
			}
			var h struct {
				TS string `json:"ts"`
			}
			_ = json.Unmarshal([]byte(l), &h)
			lines = append(lines, line{ts: h.TS, src: src, n: i, b: sc.clean(l)})
		}
	}
	add(events, 0)
	add(stream, 1)
	sort.SliceStable(lines, func(i, j int) bool {
		a, b := lines[i], lines[j]
		if a.ts != "" && b.ts != "" && a.ts != b.ts {
			return a.ts < b.ts
		}
		if a.src != b.src {
			return a.src < b.src
		}
		return a.n < b.n
	})
	var out bytes.Buffer
	for _, l := range lines {
		out.WriteString(l.b)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// clipLines cuts b to at most max bytes at a line end.
func clipLines(b []byte, max int) []byte {
	if len(b) <= max {
		return b
	}
	cut := bytes.LastIndexByte(b[:max], '\n')
	if cut < 0 {
		return nil
	}
	return b[:cut+1]
}

func clipLineCount(s string, n int) string {
	lines := strings.SplitAfter(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "") + "(clipped to " + strconv.Itoa(n) + " lines)\n"
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
