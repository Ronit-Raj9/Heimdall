package handlers

// Public API v1 (docs/api-v1.md): /api/v1 is a versioned alias of the stable
// subset of the bench API. Each v1 route is served by one /api/bench route:
// the V1Alias middleware rewrites the request path before routing, so the
// v1 route runs the same handler, the same tenancy policy and the same
// token checks as its bench route. /api/bench stays the UI's internal API
// and may change without notice; /api/v1 changes only under the
// deprecation policy in docs/api-v1.md.

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// APIV1Prefix is the public, versioned API prefix.
const APIV1Prefix = "/api/v1"

// APIV1OpenAPIPath serves the OpenAPI document of API v1.
const APIV1OpenAPIPath = APIV1Prefix + "/openapi.json"

// APIV1Version is the version of the v1 contract in the OpenAPI document. A
// minor bump adds routes or fields; v1 never removes or renames one before
// its sunset (docs/api-v1.md).
const APIV1Version = "1.0.0"

// Response headers of every v1 answer.
const (
	APIVersionHeader  = "X-Heimdall-API-Version"
	DeprecationHeader = "Deprecation"
	SunsetHeader      = "Sunset"
)

// V1Route is one public route and the bench route that serves it. Path
// parameters keep their names, so the alias substitutes them one for one.
type V1Route struct {
	Method string
	Path   string // under APIV1Prefix, e.g. /api/v1/evaluations/{id}
	Bench  string // under APIPrefix, e.g. /api/bench/evaluations/{id}
	// Deprecated routes answer with Deprecation: true and, once a date is
	// set, Sunset: <HTTP date>; they keep working until the sunset.
	Deprecated bool
	Sunset     string
}

// V1Routes is the stable subset. Add a route here only when its bench
// route's request and response are fit to keep for the life of v1.
var V1Routes = []V1Route{
	v1("GET", "/agents", "/agents"),
	v1("POST", "/agents", "/agents"),                     // Y10: create from YAML or a template
	v1("GET", "/agents/templates", "/agents/templates"),  // Y10
	v1("GET", "/agents/{id}", "/agents/{id}/config"),     // Y10: the agent's YAML, fields and sha256
	v1("PUT", "/agents/{id}", "/agents/{id}/config"),     // Y10: update (yaml or fields, if_match)
	v1("POST", "/agents/{id}/test", "/agents/{id}/test"), // Y10: one sample task
	v1("GET", "/agents/{id}/versions", "/agents/{id}/versions"),
	v1("POST", "/agents/{id}/versions", "/agents/{id}/versions"),
	v1("GET", "/agents/{id}/versions/{n}", "/agents/{id}/versions/{n}"),
	v1("GET", "/agents/{id}/compatibility", "/agents/{id}/compatibility"),
	v1("GET", "/agents/{id}/report-card", "/agents/{id}/report-card"),

	v1("GET", "/benchmarks", "/suites"),
	v1("GET", "/benchmarks/catalog", "/benchmarks/catalog"),
	v1("GET", "/benchmarks/packs", "/evaluation-packs"),
	v1("GET", "/benchmarks/{id}/tasks/{task}", "/suites/{id}/tasks/{task}"),

	v1("GET", "/evaluations", "/evaluations"),
	v1("POST", "/evaluations", "/evaluations"),
	v1("POST", "/evaluations/estimate", "/evaluations/estimate"),
	v1("GET", "/evaluations/{id}", "/evaluations/{id}"),
	v1("POST", "/evaluations/{id}/cancel", "/evaluations/{id}/cancel"),
	v1("GET", "/evaluations/{id}/report", "/evaluations/{id}/report"),
	v1("GET", "/reports/{id}", "/evaluations/{id}/report"),
	v1("POST", "/estimate", "/estimate"),

	v1("GET", "/experiments/{id}/triage", "/experiments/{id}/triage"),

	v1("GET", "/leaderboard/agents", "/leaderboard/agents"),

	// Y11 pull-mode agents: what heimdall.serve calls.
	v1("POST", "/pull/next", "/pull/next"),
	v1("POST", "/pull/turns/{id}/answer", "/pull/turns/{id}/answer"),
	v1("GET", "/pull/agents", "/pull/agents"),

	v1("GET", "/runs", "/runs"),
	v1("GET", "/runs/{id}", "/runs/{id}"),
	v1("GET", "/runs/{id}/steps", "/runs/{id}/steps"),
	v1("GET", "/runs/{id}/trace", "/runs/{id}/trace"),
	v1("GET", "/runs/{id}/otlp", "/runs/{id}/otlp"),
}

func v1(method, path, bench string) V1Route {
	return V1Route{Method: method, Path: APIV1Prefix + path, Bench: APIPrefix + bench}
}

// v1Match finds the v1 route of method and the raw request path and returns
// it with the bench path to serve it. Matching is by whole segments; a
// literal segment wins over a parameter, so /benchmarks/catalog is never
// read as a benchmark id. otherMethod reports a path match under another
// method.
func v1Match(routes []V1Route, method, path string) (route V1Route, bench string, ok, otherMethod bool) {
	segs := strings.Split(strings.TrimPrefix(path, "/"), "/")
	best := -1
	for _, r := range routes {
		params, literals, hit := matchSegments(strings.Split(strings.TrimPrefix(r.Path, "/"), "/"), segs)
		if !hit {
			continue
		}
		if r.Method != method {
			otherMethod = true
			continue
		}
		if literals > best {
			best, route, ok = literals, r, true
			bench = substitute(r.Bench, params)
		}
	}
	return route, bench, ok, otherMethod
}

func matchSegments(pattern, segs []string) (params map[string]string, literals int, ok bool) {
	if len(pattern) != len(segs) {
		return nil, 0, false
	}
	params = map[string]string{}
	for i, p := range pattern {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			if segs[i] == "" {
				return nil, 0, false
			}
			params[p] = segs[i]
			continue
		}
		if p != segs[i] {
			return nil, 0, false
		}
		literals++
	}
	return params, literals, true
}

func substitute(pattern string, params map[string]string) string {
	parts := strings.Split(pattern, "/")
	for i, p := range parts {
		if v, ok := params[p]; ok {
			parts[i] = v
		}
	}
	return strings.Join(parts, "/")
}

// V1Alias serves /api/v1: GET /api/v1/openapi.json answers the v1 document;
// a v1 route has its path rewritten to its bench route and goes on to next,
// which routes and authorizes it like any bench request; any other /api/v1
// path is 404 (405 for a known path under another method). Everything else
// passes through untouched. hosted selects the hosted security schemes.
func V1Alias(mode schemas.Mode, version string, hosted bool) lib.Middleware {
	var once sync.Once
	var doc []byte
	var docErr error
	return func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			path := string(ctx.Request.URI().PathOriginal())
			if path != APIV1Prefix && !strings.HasPrefix(path, APIV1Prefix+"/") {
				next(ctx)
				return
			}
			method := string(ctx.Method())
			ctx.Response.Header.Set(APIVersionHeader, "1")
			if path == APIV1OpenAPIPath {
				if method != fasthttp.MethodGet && method != fasthttp.MethodHead {
					lib.SendError(ctx, mode, fasthttp.StatusMethodNotAllowed, lib.ErrTypeMethodNotAllowed, "method "+method+" not allowed on "+path)
					return
				}
				once.Do(func() { doc, docErr = json.MarshalIndent(BuildV1OpenAPI(version, mode, hosted), "", "  ") })
				if docErr != nil {
					lib.SendError(ctx, mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "encode openapi: "+docErr.Error())
					return
				}
				ctx.SetStatusCode(fasthttp.StatusOK)
				ctx.SetContentType("application/json; charset=utf-8")
				ctx.Response.Header.Set(lib.ModeHeader, string(mode))
				ctx.Response.Header.Set("Cache-Control", "no-store")
				ctx.SetBody(doc)
				return
			}
			lookup := method
			if method == fasthttp.MethodHead {
				lookup = fasthttp.MethodGet
			}
			route, bench, ok, other := v1Match(V1Routes, lookup, path)
			if !ok {
				if other {
					lib.SendError(ctx, mode, fasthttp.StatusMethodNotAllowed, lib.ErrTypeMethodNotAllowed, "method "+method+" not allowed on "+path)
					return
				}
				lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound,
					"route not found in API v1: "+path+" (routes: GET "+APIV1OpenAPIPath+")")
				return
			}
			if route.Deprecated {
				ctx.Response.Header.Set(DeprecationHeader, "true")
				if route.Sunset != "" {
					ctx.Response.Header.Set(SunsetHeader, route.Sunset)
				}
			}
			ctx.Request.URI().SetPath(bench)
			next(ctx)
		}
	}
}

// BuildV1OpenAPI is the OpenAPI 3.1 document of API v1: every V1Route,
// described by its bench route's RouteDoc under the v1 path. Hosted servers
// describe session and API-token security with the token scope each route
// needs (x-heimdall-token-scope); local servers the write token. It is pure.
func BuildV1OpenAPI(version string, mode schemas.Mode, hosted bool) map[string]any {
	table := map[string]RouteDoc{}
	for _, rd := range append(append([]RouteDoc(nil), APIRoutes...), OrgRouteDocs...) {
		table[rd.Method+" "+rd.Path] = rd
	}
	g := newSchemaGen()
	g.hosted = hosted
	paths := map[string]map[string]any{}
	routes := append([]V1Route(nil), V1Routes...)
	sort.SliceStable(routes, func(i, j int) bool { return routes[i].Path < routes[j].Path })
	for _, r := range routes {
		rd, ok := table[r.Method+" "+r.Bench]
		if !ok {
			rd = RouteDoc{Method: r.Method, Tag: "v1", Summary: "Served by " + r.Method + " " + r.Bench}
		}
		rd.Path = r.Path
		op := g.operation(rd)
		op["operationId"] = operationID(r.Method, APIPrefix+strings.TrimPrefix(r.Path, APIV1Prefix))
		rule := hostedRule(r.Method, r.Bench)
		op["x-heimdall-token-scope"] = tenancy.RequiredScope(r.Method, r.Bench, rule)
		if hosted {
			hostedSecurity(op, r.Method, rule)
		}
		if r.Deprecated {
			op["deprecated"] = true
			if r.Sunset != "" {
				op["x-heimdall-sunset"] = r.Sunset
			}
		}
		key := openAPIPath(r.Path)
		if paths[key] == nil {
			paths[key] = map[string]any{}
		}
		paths[key][strings.ToLower(r.Method)] = op
	}
	g.components["ModeField"] = map[string]any{
		"type": "object", "required": []string{"mode"},
		"properties": map[string]any{"mode": map[string]any{
			"type": "string", "enum": []string{string(schemas.ModeLive), string(schemas.ModeRecorded), string(schemas.ModeSynthetic)},
			"description": "Where the data came from. Only live is a real measurement; recorded replays a stored bundle and synthetic is made up.",
		}},
	}
	schemes := securitySchemes(hosted)
	if hosted {
		if t, ok := schemes["apiToken"].(map[string]any); ok {
			t["description"] = "An organization API token (hm_...). Its role and its scopes (read, evaluate, admin) decide what it may do; each operation names the scope it needs in x-heimdall-token-scope."
		}
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "Heimdall API",
			"version": APIV1Version,
			"description": "Public, versioned Heimdall API. Every JSON response is the payload object plus a top-level mode field; raw responses carry mode in the X-Bench-Mode header. " +
				"Routes are stable for the life of v1: fields and routes may be added, never removed or renamed before a deprecation with at least 6 months to its Sunset date (docs/api-v1.md).",
			"x-server-version": version,
		},
		"servers": []any{map[string]any{"url": "/"}},
		"paths":   paths,
		"components": map[string]any{
			"schemas":         g.components,
			"securitySchemes": schemes,
		},
		"x-bench-mode": string(mode),
	}
}
