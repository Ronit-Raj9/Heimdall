package handlers

import (
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

// X1 vision grading (docs/visual-grading.md): the images of a visual grade.

// VisualReader is optionally implemented by a ReadIndex. RunVisual returns
// the bytes of visual/<name> in a run folder (capture.png, reference.png or
// diff.png), or an error wrapping schemas.ErrNotFound. Without it the route
// answers 503.
type VisualReader interface {
	RunVisual(runID, name string) ([]byte, error)
}

// getRunVisual handles GET /api/bench/runs/{id}/visual/{name}: one image of
// the run's visual grade, inline as image/png. It is served only from a
// sealed run, only for a name the grade records (capture, reference or
// diff), and only when the bytes hash to both the grade's record and the
// seal. Every one of these files is a PNG the grader encoded itself from
// decoded pixels, never the agent's bytes; sniffing is still off and the
// response is sandboxed.
func (h *RunsHandler) getRunVisual(ctx *fasthttp.RequestCtx) {
	name := pathParam(ctx, "name")
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	runID := rv.Run.RunID
	if !rv.Sealed {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
			"run "+runID+" is not sealed: visual images are served only from a sealed run")
		return
	}
	reader, isReader := h.index.(VisualReader)
	if !isReader {
		sendUnavailable(ctx, h.mode, "visual image reader")
		return
	}
	var grade schemas.Grade
	found, err := readJSONFile(h.index, runID, FileGrade, &grade)
	if err != nil {
		sendDepError(ctx, h.mode, "read grade of run "+runID, err)
		return
	}
	rec := visualRecord(&grade, name)
	if !found || rec == nil {
		sendNotFound(ctx, h.mode, "visual image of run "+runID, name)
		return
	}
	sealed, err := sealedFileHash(h.index, runID, rec.Path)
	if err != nil {
		sendVisualConflict(ctx, h.mode, runID, name, err)
		return
	}
	if sv, isVerifier := h.index.(SealVerifier); isVerifier {
		wctx, cancel := workContext(ctx, readTimeout)
		err := sv.VerifySeal(wctx, runID)
		cancel()
		if err != nil {
			if errors.Is(err, schemas.ErrSealMismatch) {
				sendVisualConflict(ctx, h.mode, runID, name, err)
				return
			}
			sendDepError(ctx, h.mode, "verify seal of run "+runID, err)
			return
		}
	}
	data, err := reader.RunVisual(runID, name)
	if err != nil {
		if errors.Is(err, schemas.ErrNotFound) {
			sendNotFound(ctx, h.mode, "visual image of run "+runID, name)
			return
		}
		sendDepError(ctx, h.mode, "read visual image of run "+runID, err)
		return
	}
	got := utils.SHA256Bytes(data)
	if got != bareHash(rec.SHA256) || got != bareHash(sealed) {
		sendVisualConflict(ctx, h.mode, runID, name,
			fmt.Errorf("file hashes to %s, grade says %s, seal says %s: %w", got, rec.SHA256, sealed, schemas.ErrSealMismatch))
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("image/png")
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.Header.Set("Content-Security-Policy", "sandbox")
	ctx.SetBody(data)
}

// visualRecord is the grade's record of the image name, nil when the grade
// has none. Its path must be exactly visual/<name>.
func visualRecord(g *schemas.Grade, name string) *schemas.VisualImage {
	if g == nil || g.Visual == nil {
		return nil
	}
	var rec *schemas.VisualImage
	switch name {
	case "capture.png":
		rec = &g.Visual.Capture
	case "reference.png":
		rec = g.Visual.Reference
	case "diff.png":
		rec = g.Visual.Diff
	}
	if rec == nil || rec.Path != "visual/"+name || rec.SHA256 == "" {
		return nil
	}
	return rec
}

func sendVisualConflict(ctx *fasthttp.RequestCtx, mode schemas.Mode, runID, name string, err error) {
	what := "visual image " + name + " of run " + runID
	if errors.Is(err, schemas.ErrSealMismatch) {
		lib.SendError(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, what+" does not match its seal: "+err.Error())
		return
	}
	sendDepError(ctx, mode, "check seal of "+what, err)
}
