package handlers

import (
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// List paging for the lists that had none (UI fix wave, lane G3): the
// experiments, evaluations, agents, suites, suite tasks, clusters and catalog
// lists take optional limit, offset and q. A request with none of them gets
// the list exactly as before; a request with any of them gets one page of
// the list in its usual order and the paging fields beside it, the same
// shape GET /runs answers (total, limit, offset).
const (
	listPageDefaultLimit = 100
	listPageMaxLimit     = 500
	listQueryMaxLen      = 200
)

// listPaging is the paging asked for by one request. on is false when the
// request named none of limit, offset and q.
type listPaging struct {
	on     bool
	q      string // lower-cased, trimmed; "" matches every row
	limit  int
	offset int
}

// ListPage is the paging part of a paged list answer: how many rows matched
// q (before limit and offset), and the limit and offset applied.
type ListPage struct {
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// readListPaging reads limit, offset and q. limit defaults to 100 and is
// capped at 500; a limit or offset that is not a non-negative integer, or a
// q longer than 200 bytes, is a 400 (answered here, false returned).
func readListPaging(ctx *fasthttp.RequestCtx, mode schemas.Mode) (listPaging, bool) {
	args := ctx.QueryArgs()
	p := listPaging{limit: listPageDefaultLimit}
	p.on = args.Has("limit") || args.Has("offset") || args.Has("q")
	if !p.on {
		return p, true
	}
	for name, dst := range map[string]*int{"limit": &p.limit, "offset": &p.offset} {
		raw := strings.TrimSpace(string(args.Peek(name)))
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, name+" must be a non-negative integer")
			return p, false
		}
		*dst = n
	}
	p.limit = min(max(p.limit, 1), listPageMaxLimit)
	q := strings.TrimSpace(string(args.Peek("q")))
	if len(q) > listQueryMaxLen {
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "q must be at most 200 characters")
		return p, false
	}
	p.q = strings.ToLower(q)
	return p, true
}

// match reports whether any of texts contains q, ignoring case. An empty q
// matches.
func (p listPaging) match(texts ...string) bool {
	if p.q == "" {
		return true
	}
	for _, t := range texts {
		if strings.Contains(strings.ToLower(t), p.q) {
			return true
		}
	}
	return false
}

// window returns the bounds of the page within n matching rows.
func (p listPaging) window(n int) (lo, hi int) {
	lo = min(p.offset, n)
	hi = min(lo+p.limit, n)
	return lo, hi
}

// page returns the ListPage of n matching rows.
func (p listPaging) page(n int) ListPage {
	return ListPage{Total: n, Limit: p.limit, Offset: p.offset}
}

// pageItems keeps the items whose texts match q, then cuts the page. The
// order of items is kept.
func pageItems[T any](p listPaging, items []T, texts func(T) []string) ([]T, ListPage) {
	kept := make([]T, 0, len(items))
	for _, it := range items {
		if p.match(texts(it)...) {
			kept = append(kept, it)
		}
	}
	lo, hi := p.window(len(kept))
	return kept[lo:hi], p.page(len(kept))
}
