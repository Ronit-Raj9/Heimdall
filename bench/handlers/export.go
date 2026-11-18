package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// W10 export (docs/export.md): experiment results as Every Eval Ever records
// or a flat run table, and a suite as Harbor task folders in one tar. Only
// sealed, seal-checked runs are read, and the Harbor export served here never
// holds a hidden test, an expected answer or a reference solution: each
// task's verifier is withheld (the CLI's -include-hidden is the only way to
// get it).

// Export formats served by GET /experiments/{id}/export.
const (
	ExportFormatEEE   = "eee"
	ExportFormatJSONL = "jsonl"
	ExportFormatCSV   = "csv"
)

// ErrExportRefused is returned by an ExportSource for an export the data
// does not allow, such as synthetic results without allow_synthetic. The
// handler answers 409 with the message.
var ErrExportRefused = errors.New("export refused")

// ExportSource builds export bodies. cmd/bifrost-bench implements it with
// bench/export over the store and the suite loader.
type ExportSource interface {
	// ExperimentExport returns the experiment's results in format (eee,
	// jsonl or csv) and the body's content type.
	ExperimentExport(ctx context.Context, expID, format string, allowSynthetic bool) (body []byte, contentType string, err error)
	// SuiteHarbor returns the suite's admitted public tasks as a gzip tar
	// of Harbor task folders, verifiers withheld.
	SuiteHarbor(ctx context.Context, suiteID string) ([]byte, error)
}

// exportTimeout bounds building one export.
const exportTimeout = 2 * time.Minute

// ExportHandler serves the export downloads.
type ExportHandler struct {
	mode schemas.Mode
	src  ExportSource
}

// NewExportHandler creates the export handler. src may be nil.
func NewExportHandler(mode schemas.Mode, src ExportSource) *ExportHandler {
	return &ExportHandler{mode: mode, src: src}
}

// RegisterRoutes registers GET /experiments/{id}/export and
// GET /suites/{id}/export.
func (h *ExportHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/experiments/{id}/export", lib.ChainMiddlewares(h.getExperimentExport, middlewares...))
	r.GET(APIPrefix+"/suites/{id}/export", lib.ChainMiddlewares(h.getSuiteExport, middlewares...))
}

func (h *ExportHandler) getExperimentExport(ctx *fasthttp.RequestCtx) {
	expID, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return
	}
	format := string(ctx.QueryArgs().Peek("format"))
	ext := map[string]string{ExportFormatEEE: ".eee.jsonl", ExportFormatJSONL: ".runs.jsonl", ExportFormatCSV: ".runs.csv"}[format]
	if ext == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "format must be eee, jsonl or csv")
		return
	}
	if h.src == nil {
		sendUnavailable(ctx, h.mode, "export")
		return
	}
	wctx, cancel := workContext(ctx, exportTimeout)
	defer cancel()
	allow := string(ctx.QueryArgs().Peek("allow_synthetic")) == "true"
	body, ctype, err := h.src.ExperimentExport(wctx, expID, format, allow)
	if errors.Is(err, ErrExportRefused) {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "export of experiment "+expID, err)
		return
	}
	h.download(ctx, ctype, expID+ext, body)
}

func (h *ExportHandler) getSuiteExport(ctx *fasthttp.RequestCtx) {
	suiteID, ok := idParam(ctx, h.mode, "id", "suite")
	if !ok {
		return
	}
	if f := string(ctx.QueryArgs().Peek("format")); f != "harbor" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "format must be harbor")
		return
	}
	if h.src == nil {
		sendUnavailable(ctx, h.mode, "export")
		return
	}
	wctx, cancel := workContext(ctx, exportTimeout)
	defer cancel()
	body, err := h.src.SuiteHarbor(wctx, suiteID)
	if errors.Is(err, ErrExportRefused) {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "harbor export of suite "+suiteID, err)
		return
	}
	h.download(ctx, "application/gzip", suiteID+".harbor.tar.gz", body)
}

// download answers an attachment. name is built from an id that passed
// validID plus a fixed extension, so it needs no quoting.
func (h *ExportHandler) download(ctx *fasthttp.RequestCtx, ctype, name string, body []byte) {
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType(ctype)
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.Header.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	ctx.SetBody(body)
}
