package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	FileEvidence = "evidence.json"
)

// runFileTypes is the allowlist of run files the API serves, with the type
// each is served as. The raw agent stream, gateway rows and proxy ledger are
// left out on purpose: content logs can hold secrets the agent printed and
// stay local (DESIGN.md 14). Nothing is ever served as HTML.
var runFileTypes = map[string]string{
	FileRun:      "application/json; charset=utf-8",
	FileEvents:   "application/x-ndjson; charset=utf-8",
	FileDiff:     "text/plain; charset=utf-8",
	FileReceipts: "application/json; charset=utf-8",
	FileMetrics:  "application/json; charset=utf-8",
	FileGates:    "application/json; charset=utf-8",
	FileGrade:    "application/json; charset=utf-8",
	FileCost:     "application/json; charset=utf-8",
	FileSeal:     "application/json; charset=utf-8",
}

// RunsHandler serves one run: its record, events, diff, receipts and files.
type RunsHandler struct {
	mode  schemas.Mode
	index ReadIndex
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
		Slot: rv.Slot, Run: rv.Run, Sealed: rv.Sealed, SealHash: rv.SealHash, GradeSource: GradeSourceOriginal,
		Gates: []schemas.GateResult{}, Receipts: []schemas.Receipt{},
	}
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
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
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
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.RunReceipts{RunID: rv.Run.RunID, Receipts: receipts})
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
