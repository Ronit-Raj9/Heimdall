package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Run folder file names the API reads (DESIGN.md 16.1).
const (
	FileRun      = "run.json"
	FileEvents   = "events.jsonl"
	FileDiff     = "workspace.diff"
	FileReceipts = "receipts.json"
	FileMetrics  = "metrics.json"
	FileGates    = "gates.json"
	FileGrade    = "grade.json"
	FileCost     = "cost.json"
	FileSeal     = "seal.json"
	// FileEvidence is read internally for the gateway completeness flag and
	// never served: it holds the gateway rows.
	FileEvidence  = "evidence.json"
	FileCanonical = "canonical.json"
)

// runFileTypes is the allowlist of run files the API serves, with the type
// each is served as. The raw agent stream, gateway rows and proxy ledger are
// left out on purpose: content logs can hold secrets the agent printed and
// stay local (DESIGN.md 14). Nothing is ever served as HTML.
var runFileTypes = map[string]string{
	FileRun:       "application/json; charset=utf-8",
	FileEvents:    "application/x-ndjson; charset=utf-8",
	FileDiff:      "text/plain; charset=utf-8",
	FileReceipts:  "application/json; charset=utf-8",
	FileMetrics:   "application/json; charset=utf-8",
	FileGates:     "application/json; charset=utf-8",
	FileGrade:     "application/json; charset=utf-8",
	FileCost:      "application/json; charset=utf-8",
	FileSeal:      "application/json; charset=utf-8",
	FileCanonical: "application/json; charset=utf-8",
}

// RunsHandler serves one run: its record, events, diff, receipts and files.
type RunsHandler struct {
	mode           schemas.Mode
	index          ReadIndex
	suites         SuiteSource // optional: the task voice bounds of the Voice tab (runvoice.go)
	heldOutVisible bool        // server config held_out.visible (WithHeldOutVisible)
	breakpoints    bool        // this server can hold model calls (WithBreakpoints)
	archiveRoot    string      // project root of the archive sidecar (UI fix wave D1); "" marks none
}

// WithBreakpoints sets whether this server can hold a run's model calls
// (Services.Holds is set), which Debug with breakpoints needs. The default,
// false, answers debug_allowed.breakpoints false with the reason, so the
// page offers Replay and Fork only (N212).
func (h *RunsHandler) WithBreakpoints(held bool) *RunsHandler {
	h.breakpoints = held
	return h
}

// breakpointsReason is the run page line where the Debug button would be on
// a server that cannot hold model calls.
const breakpointsReason = "Pausing before model calls needs a worker this server controls. On this server, use Replay or Fork from a step."

// WithSuites lets the Voice tab read a run's task voice bounds from its suite.
func (h *RunsHandler) WithSuites(s SuiteSource) *RunsHandler {
	h.suites = s
	return h
}

// WithHeldOutVisible sets whether runs of held-out tasks are served (server
// config held_out.visible). The default, false, answers 404 for every route
// of a run whose experiment's frozen plan holds its task out: its record,
// events, diff, files, raw evidence, artifacts and trace all carry the
// instruction or the agent's work on it. Listings keep the run's ids.
func (h *RunsHandler) WithHeldOutVisible(visible bool) *RunsHandler {
	h.heldOutVisible = visible
	return h
}

// heldOutRun reports whether the run's task is held out in its experiment's
// frozen plan (manifest held_out_tasks), the split every result surface
// reads. A run the index lists without a manifest has no frozen plan to
// hold it out.
func heldOutRun(index ReadIndex, rv *RunView) bool {
	m, ok := index.Manifest(rv.Slot.ExpID)
	if !ok || m == nil {
		return false
	}
	return slices.Contains(m.HeldOutTasks, rv.Slot.Task)
}

// refuseHeldOutRun answers 404 for a held-out task's run unless held-out
// content is visible, and reports whether it did.
func refuseHeldOutRun(ctx *fasthttp.RequestCtx, mode schemas.Mode, index ReadIndex, visible bool, rv *RunView) bool {
	if visible || !heldOutRun(index, rv) {
		return false
	}
	lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound,
		"This run is of a held-out task: its content stays private.")
	return true
}

// NewRunsHandler creates the runs handler. index may be nil.
func NewRunsHandler(mode schemas.Mode, index ReadIndex) *RunsHandler {
	return &RunsHandler{mode: mode, index: index}
}

// RegisterRoutes registers the run routes.
func (h *RunsHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	p := APIPrefix + "/runs/{id}"
	r.GET(p, lib.ChainMiddlewares(h.getRun, middlewares...))
	r.GET(p+"/events", lib.ChainMiddlewares(h.getRunEvents, middlewares...))
	r.GET(p+"/diff", lib.ChainMiddlewares(h.getRunDiff, middlewares...))
	r.GET(p+"/receipts", lib.ChainMiddlewares(h.getRunReceipts, middlewares...))
	r.GET(p+"/files/{name}", lib.ChainMiddlewares(h.getRunFile, middlewares...))
	r.GET(p+"/evidence/{name}", lib.ChainMiddlewares(h.getRunEvidence, middlewares...))
	r.GET(p+"/artifacts/{artifact}/{name}", lib.ChainMiddlewares(h.getRunArtifact, middlewares...)) // artifact files (handlers/runartifacts.go)
	r.GET(p+"/otlp", lib.ChainMiddlewares(h.getRunOTLP, middlewares...))                            // OpenTelemetry trace download (handlers/runotlp.go)
	r.GET(p+"/steps", lib.ChainMiddlewares(h.getRunSteps, middlewares...))                          // unified step timeline (handlers/runsteps.go)
	r.GET(p+"/trace", lib.ChainMiddlewares(h.getRunTrace, middlewares...))                          // trace download: json, jsonl or otlp (handlers/runtrace.go)
	r.GET(p+"/remote", lib.ChainMiddlewares(h.getRunRemote, middlewares...))                        // Remote coverage: channel summary of a remote agent's run (handlers/runremote.go)
	r.GET(p+"/a2a", lib.ChainMiddlewares(h.getRunA2A, middlewares...))                              // B1: the agent gateway witness of an a2a run (handlers/runa2a.go)
	r.GET(p+"/visual/{name}", lib.ChainMiddlewares(h.getRunVisual, middlewares...))                 // X1 vision grading: capture, reference and diff images (handlers/runvisual.go)
	r.GET(p+"/voice", lib.ChainMiddlewares(h.getRunVoice, middlewares...))                          // X8 voice agents: the call timeline (handlers/runvoice.go)
	r.GET(p+"/voice/audio/{track}", lib.ChainMiddlewares(h.getRunVoiceAudio, middlewares...))       // X8 voice agents: call audio, caller or agent
	r.GET(p+"/agents", lib.ChainMiddlewares(h.getRunAgents, middlewares...))                        // Multi-agent: tree, lanes, handoffs and blame (handlers/runagents.go)
	r.GET(p+"/codescan", lib.ChainMiddlewares(h.getRunCodeScan, middlewares...))                    // X4: code scan of added lines (handlers/runcodescan.go)
	r.GET(p+"/services", lib.ChainMiddlewares(h.getRunServices, middlewares...))                    // X2: services of the run and their logs (handlers/runservices.go)
	r.GET(p+"/services/{service}/log", lib.ChainMiddlewares(h.getRunServiceLog, middlewares...))    // X2
	r.GET(p+"/frames", lib.ChainMiddlewares(h.getRunFrames, middlewares...))                        // screen replay frames (handlers/runframes.go)
	r.GET(p+"/frames/{n}", lib.ChainMiddlewares(h.getRunFrame, middlewares...))
	r.GET(p+"/recordings", lib.ChainMiddlewares(h.getRunRecordings, middlewares...))    // Z03: video, HAR and console (handlers/runrecordings.go)
	r.GET(p+"/recordings/{n}", lib.ChainMiddlewares(h.getRunRecording, middlewares...)) // Z03
}

// getRun handles GET /api/bench/runs/{id}: the run record with its grade,
// metrics, gates and receipts. The grade is the one the run was sealed with;
// regrades are read through the experiment's analysis. A sealed run is
// checked against its seal when the index can verify (SealVerifier): a
// mismatch, or a check that could not run, is reported in seal_error beside
// the files, never passed over.
func (h *RunsHandler) getRun(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	id := rv.Run.RunID
	out := schemas.RunDetail{
		Slot: rv.Slot, Run: rv.Run, Sealed: rv.Sealed, SealHash: rv.SealHash, Superseded: rv.Superseded, GradeSource: GradeSourceOriginal,
		Gates: []schemas.GateResult{}, Receipts: []schemas.Receipt{},
	}
	out.Archived = archiveMarkOf(h.archiveRoot, archiveRuns, id) // UI fix wave D1: an archived run still opens by id
	if sv, isVerifier := h.index.(SealVerifier); isVerifier && rv.Sealed {
		if err := sv.VerifySeal(ctx, id); err != nil {
			out.SealError = err.Error()
		}
	}
	metrics, err := readMetrics(h.index, id)
	if err != nil {
		sendDepError(ctx, h.mode, "read metrics of run "+id, err)
		return
	}
	out.Metrics = metrics
	var grade schemas.Grade
	found, err := readJSONFile(h.index, id, FileGrade, &grade)
	if err != nil {
		sendDepError(ctx, h.mode, "read grade of run "+id, err)
		return
	}
	if found {
		out.Grade = &grade
	}
	var evidence struct {
		Contract     *schemas.RunEvidenceContract  `json:"evidence_contract,omitempty"`
		AdapterFault *schemas.AdapterFaultEvidence `json:"adapter_fault,omitempty"`
	}
	// evidence.json is not served (it holds the gateway rows), so it is read
	// as an internal file; only the two fields above leave the server.
	if _, err := readInternalJSON(h.index, id, FileEvidence, &evidence); err != nil {
		sendDepError(ctx, h.mode, "read evidence of run "+id, err)
		return
	}
	out.Evidence = evidence.Contract
	out.AdapterFault = evidence.AdapterFault
	var canonical schemas.CanonicalRun
	if found, err := readJSONFile(h.index, id, FileCanonical, &canonical); err != nil {
		sendDepError(ctx, h.mode, "read canonical run "+id, err)
		return
	} else if found {
		out.Canonical = &canonical
	} else {
		out.Canonical = legacyCanonicalView(rv)
	}
	for name, dst := range map[string]any{FileGates: &out.Gates, FileReceipts: &out.Receipts} {
		if _, err := readJSONFile(h.index, id, name, dst); err != nil {
			sendDepError(ctx, h.mode, "read "+name+" of run "+id, err)
			return
		}
	}
	// A file holding JSON null decodes to a nil slice; the contract is a list.
	if out.Gates == nil {
		out.Gates = []schemas.GateResult{}
	}
	if out.Receipts == nil {
		out.Receipts = []schemas.Receipt{}
	}
	if err := fillScenario(h.index, id, &out); err != nil {
		sendDepError(ctx, h.mode, "read scenario evidence of run "+id, err)
		return
	}
	// The output kind by the run list's rule, so the page never guesses "diff".
	out.Output, out.TaskTitle = loadRunSuiteFacts(ctx, h.suites, rv.Slot)
	out.SuiteTitle = suiteTitle(rv.Slot.Suite)
	if string(ctx.QueryArgs().Peek("failure")) == "1" && rv.Run.Outcome != schemas.OutcomePass {
		out.Failure = runFailureGroup(ctx, h.index, rv)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, plainServed(h.withDebugAllowed(ctx, rv, out)))
}

// withDebugAllowed adds debug_allowed, computed by the rule the debug routes
// apply, and the source condition of a debug run (DebugSpec.FromCondition,
// read from its frozen plan) to a run detail.
func (h *RunsHandler) withDebugAllowed(ctx context.Context, rv *RunView, out schemas.RunDetail) schemas.RunDetail {
	var m *schemas.Manifest
	if h.index != nil {
		m, _ = h.index.Manifest(rv.Slot.ExpID)
	}
	if out.Run.Debug != nil && out.Run.Debug.FromCondition == "" && m != nil && m.Experiment.Debug != nil {
		d := *out.Run.Debug
		d.FromCondition = m.Experiment.Debug.FromCondition
		out.Run.Debug = &d
	}
	// Skills in this run (handlers/runskills.go): left out when the steps
	// cannot be read, so the page never says "no sign it was read" unseen.
	if m != nil && h.index != nil {
		if st, err := readRunSteps(h.index, rv.Run.RunID); err == nil {
			out.Skills = runSkillsOf(m, rv.Slot, st.Steps)
		}
	}
	probe := conditionProbe(ctx, h.suites, rv, m)
	out.DebugAllowed = runDebugAllowed(rv.Slot.Condition, probe, rv.Sealed, out.Run.Debug)
	if a := out.DebugAllowed; a != nil && a.Fork {
		a.Breakpoints = h.breakpoints
		if !h.breakpoints {
			a.BreakpointsReason = breakpointsReason
		}
	}
	return out
}

// legacyCanonicalView gives old sealed runs a read-only canonical shape. It
// carries only identity that was already stored in the legacy run and marks
// the missing evidence explicitly. It never writes or upgrades the old run.
func legacyCanonicalView(rv *RunView) *schemas.CanonicalRun {
	if rv == nil {
		return nil
	}
	run := rv.Run
	return &schemas.CanonicalRun{
		CanonicalVersion: "legacy",
		SchemaVersion:    "legacy",
		RunID:            run.RunID,
		Experiment:       run.ExpID,
		Task: schemas.CanonicalTaskIdentity{
			SuiteID:     rv.Slot.Suite,
			TaskID:      rv.Slot.Task,
			TaskVersion: rv.Slot.TaskVersion,
			TaskHash:    rv.Slot.TaskHash,
			Condition:   rv.Slot.Condition,
			Trial:       rv.Slot.Trial,
		},
		Arm:            rv.Slot.Arm,
		ModelRequested: "",
		EvidenceStatus: schemas.EvidenceStatus{Level: schemas.EvidenceUnknown, UnknownReasons: []string{"legacy run has no canonical.json"}},
	}
}

// getRunEvents handles GET /api/bench/runs/{id}/events.
func (h *RunsHandler) getRunEvents(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	events, err := readEvents(h.index, rv.Run.RunID)
	if err != nil {
		sendDepError(ctx, h.mode, "read events of run "+rv.Run.RunID, err)
		return
	}
	complete, err := gatewayComplete(h.index, rv.Run.RunID)
	if err != nil {
		sendDepError(ctx, h.mode, "read evidence of run "+rv.Run.RunID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.RunEvents{RunID: rv.Run.RunID, Complete: complete, Events: events})
}

// getRunDiff handles GET /api/bench/runs/{id}/diff. The default is the
// schemas.RunDiff JSON; ?format=text or Accept: text/plain returns the patch
// itself as text/plain. A run without a diff yet answers 404.
func (h *RunsHandler) getRunDiff(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	data, found, err := readRunFile(h.index, rv.Run.RunID, FileDiff)
	if err != nil {
		sendDepError(ctx, h.mode, "read diff of run "+rv.Run.RunID, err)
		return
	}
	if !found {
		sendNotFound(ctx, h.mode, "diff of run", rv.Run.RunID)
		return
	}
	wantText := string(ctx.QueryArgs().Peek("format")) == "text" ||
		bytes.HasPrefix(ctx.Request.Header.Peek(fasthttp.HeaderAccept), []byte("text/plain"))
	if wantText {
		h.sendRaw(ctx, FileDiff, data)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.RunDiff{RunID: rv.Run.RunID, Diff: DiffSummary(data), Patch: string(data)})
}

// getRunReceipts handles GET /api/bench/runs/{id}/receipts. A clean run has
// no receipts and answers an empty list.
func (h *RunsHandler) getRunReceipts(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	receipts := []schemas.Receipt{}
	if _, err := readJSONFile(h.index, rv.Run.RunID, FileReceipts, &receipts); err != nil {
		sendDepError(ctx, h.mode, "read receipts of run "+rv.Run.RunID, err)
		return
	}
	if receipts == nil {
		receipts = []schemas.Receipt{}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, plainServed(schemas.RunReceipts{RunID: rv.Run.RunID, Receipts: receipts}))
}

// getRunFile handles GET /api/bench/runs/{id}/files/{name} for allowlisted
// names only. Any other name, including anything with a path in it, is 404.
func (h *RunsHandler) getRunFile(ctx *fasthttp.RequestCtx) {
	name := pathParam(ctx, "name")
	if _, ok := runFileTypes[name]; !ok {
		sendNotFound(ctx, h.mode, "run file", name)
		return
	}
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	data, found, err := readRunFile(h.index, rv.Run.RunID, name)
	if err != nil {
		sendDepError(ctx, h.mode, "read "+name, err)
		return
	}
	if !found {
		sendNotFound(ctx, h.mode, "run file", name)
		return
	}
	h.sendRaw(ctx, name, data)
}

// getRunEvidence handles explicit raw audit evidence downloads. These files
// are excluded from the ordinary run-file allowlist because they contain
// agent and service content. A caller must name one of the supported evidence
// files, and the run must still be sealed and verify against its seal.
func (h *RunsHandler) getRunEvidence(ctx *fasthttp.RequestCtx) {
	name := pathParam(ctx, "name")
	if !rawEvidenceNameOK(name) {
		sendNotFound(ctx, h.mode, "raw run evidence", name)
		return
	}
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	runID := rv.Run.RunID
	if !rv.Sealed {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
			"run "+runID+" is not sealed: raw evidence is served only from a sealed run")
		return
	}
	sv, isVerifier := h.index.(SealVerifier)
	if !isVerifier {
		sendUnavailable(ctx, h.mode, "seal verifier")
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	err := sv.VerifySeal(wctx, runID)
	cancel()
	if err != nil {
		if errors.Is(err, schemas.ErrSealMismatch) {
			lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
				"run "+runID+" does not match its seal and raw evidence is not served: "+err.Error())
			return
		}
		sendDepError(ctx, h.mode, "verify seal of run "+runID, err)
		return
	}
	data, err := h.index.RunFile(runID, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
			sendNotFound(ctx, h.mode, "raw run evidence", name)
			return
		}
		sendDepError(ctx, h.mode, "read "+name+" of run "+runID, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/octet-stream")
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.Header.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	ctx.SetBody(data)
}

// rawEvidenceNameOK keeps the raw endpoint narrow and prevents path input
// from reaching a store implementation. Session files use the positive
// session index emitted by the scenario runner.
func rawEvidenceNameOK(name string) bool {
	switch name {
	case "gateway.raw.jsonl", "gateway.requests.jsonl", "proxy.ledger.jsonl", "tools.jsonl", "agent.stream.jsonl", "agent.stderr.txt", "adapter.fault.json", "adapter.fault.jsonl":
		return true
	}
	for _, prefix := range []string{"agent.stream.", "agent.stderr."} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		suffix := strings.TrimPrefix(name, prefix)
		if strings.HasSuffix(suffix, ".jsonl") && prefix == "agent.stream." {
			suffix = strings.TrimSuffix(suffix, ".jsonl")
		} else if strings.HasSuffix(suffix, ".txt") && prefix == "agent.stderr." {
			suffix = strings.TrimSuffix(suffix, ".txt")
		} else {
			return false
		}
		return positiveSessionIndex(suffix)
	}
	return false
}

func positiveSessionIndex(s string) bool {
	if s == "" || (len(s) > 1 && s[0] == '0') || strings.Trim(s, "0123456789") != "" {
		return false
	}
	i, err := strconv.Atoi(s)
	return err == nil && i > 0
}

// DiffSummary describes a unified git diff: its sha256 and every changed
// path with its status. It is pure.
func DiffSummary(patch []byte) schemas.Diff {
	sum := sha256.Sum256(patch)
	d := schemas.Diff{Path: FileDiff, SHA256: "sha256:" + hex.EncodeToString(sum[:]), Files: []schemas.DiffFile{}}
	var cur *schemas.DiffFile
	for _, line := range strings.Split(string(patch), "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			path := line[len("diff --git "):]
			if i := strings.LastIndex(path, " b/"); i >= 0 {
				path = path[i+3:]
			}
			d.Files = append(d.Files, schemas.DiffFile{Path: path, Status: "modified"})
			cur = &d.Files[len(d.Files)-1]
		case cur == nil:
		case strings.HasPrefix(line, "new file mode"):
			cur.Status = "added"
		case strings.HasPrefix(line, "deleted file mode"):
			cur.Status = "deleted"
		}
	}
	return d
}

// lookup resolves the {id} run, or answers the error.
func (h *RunsHandler) lookup(ctx *fasthttp.RequestCtx) (*RunView, bool) {
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return nil, false
	}
	id, ok := idParam(ctx, h.mode, "id", "run")
	if !ok {
		return nil, false
	}
	rv, ok := h.index.Run(id)
	if !ok || rv == nil {
		sendNotFound(ctx, h.mode, "run", id)
		return nil, false
	}
	if refuseHeldOutRun(ctx, h.mode, h.index, h.heldOutVisible, rv) {
		return nil, false
	}
	return rv, true
}

// sendRaw writes a run file with its allowlisted type and the mode header.
func (h *RunsHandler) sendRaw(ctx *fasthttp.RequestCtx, name string, data []byte) {
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType(runFileTypes[name])
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.SetBody(data)
}

// readRunFile reads one allowlisted file of a run. A missing file returns
// found false and no error.
func readRunFile(index ReadIndex, runID, name string) ([]byte, bool, error) {
	if _, ok := runFileTypes[name]; !ok {
		return nil, false, nil
	}
	data, err := index.RunFile(runID, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read %s of run %s: %w", name, runID, err)
	}
	return data, true, nil
}

// readJSONFile decodes an allowlisted JSON run file into dst. A missing file
// leaves dst untouched and returns false.
func readJSONFile(index ReadIndex, runID, name string, dst any) (bool, error) {
	data, found, err := readRunFile(index, runID, name)
	if err != nil || !found {
		return false, err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return false, fmt.Errorf("parse %s of run %s: %w", name, runID, err)
	}
	return true, nil
}

// gatewayComplete reports whether the gateway returned every request row of
// the run, from evidence.json. No evidence yet means not complete.
func gatewayComplete(index ReadIndex, runID string) (bool, error) {
	data, err := index.RunFile(runID, FileEvidence)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("read %s of run %s: %w", FileEvidence, runID, err)
	}
	var ev struct {
		Requests *struct {
			Complete bool `json:"complete"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return false, fmt.Errorf("parse %s of run %s: %w", FileEvidence, runID, err)
	}
	return ev.Requests != nil && ev.Requests.Complete, nil
}

// readEvents reads a run's events.jsonl. A run without events yet has none.
func readEvents(index ReadIndex, runID string) ([]schemas.Event, error) {
	data, found, err := readRunFile(index, runID, FileEvents)
	if err != nil {
		return nil, err
	}
	events := []schemas.Event{}
	if !found {
		return events, nil
	}
	for i, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e schemas.Event
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("parse %s line %d of run %s: %w", FileEvents, i+1, runID, err)
		}
		events = append(events, e)
	}
	return events, nil
}
