package handlers

// X4: GET /api/bench/runs/{id}/codescan (docs/code-scan.md).

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/codescan"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

// getRunCodeScan handles GET /api/bench/runs/{id}/codescan: the security
// findings on the lines the run's agent added, with the experiment's veto
// level. A sealed run is checked against its seal first when the index can
// verify; a mismatch is 409 and nothing is read. Findings come from the
// run's sealed code scan evidence when it was made by the current rules
// (sealed true), else the builtin rules run again now on the sealed diff
// (sealed false). A run with no diff answers 200 with scanned false and the
// reason. Snippets are redacted display text.
func (h *RunsHandler) getRunCodeScan(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	runID := rv.Run.RunID
	if sv, isVerifier := h.index.(SealVerifier); isVerifier && rv.Sealed {
		wctx, cancel := workContext(ctx, readTimeout)
		err := sv.VerifySeal(wctx, runID)
		cancel()
		if err != nil {
			if errors.Is(err, schemas.ErrSealMismatch) {
				lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "run "+runID+" does not match its seal: "+err.Error())
				return
			}
			sendDepError(ctx, h.mode, "verify seal of run "+runID, err)
			return
		}
	}
	rep, err := readRunCodeScan(h.index, rv)
	if err != nil {
		if errors.Is(err, schemas.ErrSealMismatch) {
			lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
			return
		}
		sendDepError(ctx, h.mode, "read code scan of run "+runID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, rep)
}

// readRunCodeScan builds a run's code scan report from evidence.json and
// workspace.diff, whose sha256 must match the evidence's record.
func readRunCodeScan(index ReadIndex, rv *RunView) (*schemas.CodeScanReport, error) {
	runID := rv.Run.RunID
	ev := &schemas.Evidence{}
	data, found, err := readInternal(index, runID, FileEvidence)
	if err != nil {
		return nil, err
	}
	if found {
		var part struct {
			Diff     *schemas.Diff             `json:"diff"`
			CodeScan *schemas.CodeScanEvidence `json:"codescan"`
		}
		if err := json.Unmarshal(data, &part); err != nil {
			return nil, fmt.Errorf("parse %s of run %s: %w", FileEvidence, runID, err)
		}
		ev.Diff, ev.CodeScan = part.Diff, part.CodeScan
	}
	diff, found, err := readRunFile(index, runID, FileDiff)
	if err != nil {
		return nil, err
	}
	if !found {
		diff = nil
	} else if ev.Diff != nil && ev.Diff.SHA256 != "" && bareHash(ev.Diff.SHA256) != utils.SHA256Bytes(diff) {
		return nil, fmt.Errorf("workspace.diff of run %s does not match the sha256 its evidence records: %w", runID, schemas.ErrSealMismatch)
	}
	var policy *schemas.CodeScanPolicy
	if m, ok := index.Manifest(rv.Slot.ExpID); ok && m != nil {
		policy = m.Experiment.CodeScan
	}
	scan, sealed := codescan.ForDiff(ev, diff)
	return codescan.BuildReport(runID, scan, sealed, policy), nil
}
