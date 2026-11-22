package handlers

// Reference board (gap G4-03): GET /api/bench/reference/board, the scores
// of known agents on the built-in library benchmarks, measured by the
// operator in the Heimdall reference organization and published one
// evaluation at a time with `bifrost-bench reference publish`. The route is
// public and read-only: it serves the published snapshot (reference
// package) and nothing of any organization's own data. An empty board is
// an empty list, never placeholder numbers.

import (
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/reference"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// ReferenceBoardSource loads the published board.
type ReferenceBoardSource interface {
	Load() (*reference.Board, error)
}

// ReferenceBoard is GET /reference/board.
type ReferenceBoard struct {
	// Org names the organization the runs belong to.
	Org string `json:"org"`
	// Benchmark is the benchmark filter, "" for every benchmark.
	Benchmark string `json:"benchmark"`
	// Benchmarks lists every benchmark with at least one entry, unfiltered.
	Benchmarks []string          `json:"benchmarks"`
	Entries    []reference.Entry `json:"entries"`
	UpdatedAt  *time.Time        `json:"updated_at,omitempty"`
}

// ReferenceHandler serves the reference board.
type ReferenceHandler struct {
	mode schemas.Mode
	src  ReferenceBoardSource
}

// NewReferenceHandler builds the handler; a nil src serves an empty board.
func NewReferenceHandler(mode schemas.Mode, src ReferenceBoardSource) *ReferenceHandler {
	return &ReferenceHandler{mode: mode, src: src}
}

// RegisterRoutes registers GET /reference/board.
func (h *ReferenceHandler) RegisterRoutes(r *router.Router, mw ...lib.Middleware) {
	r.GET(APIPrefix+"/reference/board", lib.ChainMiddlewares(h.board, mw...))
}

func (h *ReferenceHandler) board(ctx *fasthttp.RequestCtx) {
	benchmark := strings.TrimSpace(string(ctx.QueryArgs().Peek("benchmark")))
	if len(benchmark) > 200 {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "benchmark is too long")
		return
	}
	b := &reference.Board{Entries: []reference.Entry{}}
	if h.src != nil {
		loaded, err := h.src.Load()
		if err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "reference board unavailable")
			return
		}
		b = loaded
	}
	out := ReferenceBoard{
		Org: reference.OrgName, Benchmark: benchmark,
		Benchmarks: b.Benchmarks(), Entries: b.ForBenchmark(benchmark), UpdatedAt: b.UpdatedAt,
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func init() {
	APIRoutes = append(APIRoutes, RouteDoc{
		Method: "GET", Path: APIPrefix + "/reference/board", Tag: "reference",
		Summary:  "Public reference board: scores of known agents on the built-in benchmarks, from real runs the operator published (empty until something is published)",
		Query:    []QueryDoc{{Name: "benchmark", Description: "Only this benchmark's entries (id, with or without @version)"}},
		Response: ReferenceBoard{},
	})
}
