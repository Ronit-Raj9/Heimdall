package handlers

import (
	"errors"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceexport"
	"github.com/valyala/fasthttp"
)

// getRunOTLP handles GET /api/bench/runs/{id}/otlp: the run as an
// OpenTelemetry trace in OTLP/JSON (traceexport, docs/trace-export.md), sent
// as a download named <run id>.otlp.json. It is served only from a sealed run
// whose folder verifies against its seal (SealVerifier); without a verifier
// the route answers 503, and a run that does not verify is 409. Tool
// arguments and results are left out unless ?payloads=true; then they are
// redacted and capped.
func (h *RunsHandler) getRunOTLP(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	runID := rv.Run.RunID
	if !rv.Sealed {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
			"run "+runID+" is not sealed: only sealed runs are exported as traces")
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
				"run "+runID+" does not match its seal and is not exported: "+err.Error())
			return
		}
		sendDepError(ctx, h.mode, "verify seal of run "+runID, err)
		return
	}
	in, err := traceexport.LoadFiles(func(name string) ([]byte, error) { return h.index.RunFile(runID, name) })
	if err != nil {
		sendDepError(ctx, h.mode, "load run "+runID+" for export", err)
		return
	}
	in.Slot = rv.Slot
	if rv.SealHash != "" {
		in.SealHash = rv.SealHash
	}
	payloads := string(ctx.QueryArgs().Peek("payloads")) == "true"
	td, err := traceexport.Build(in, traceexport.Options{Payloads: payloads})
	if err != nil {
		sendDepError(ctx, h.mode, "export run "+runID, err)
		return
	}
	body, err := traceexport.Marshal(td)
	if err != nil {
		sendDepError(ctx, h.mode, "export run "+runID, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/json")
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	// The run id passed idParam (validID): letters, digits, '-', '_' and '.'
	// only, so it needs no quoting or escaping.
	ctx.Response.Header.Set("Content-Disposition", `attachment; filename="`+runID+`.otlp.json"`)
	ctx.SetBody(body)
}
