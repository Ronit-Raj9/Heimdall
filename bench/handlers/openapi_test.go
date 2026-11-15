package handlers_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/valyala/fasthttp"
)

// fetchOpenAPI builds the real server with no dependencies and reads its
// description, the way a client does.
func fetchOpenAPI(t *testing.T) (map[string]any, *fasthttp.RequestCtx) {
	t.Helper()
	cfg := &lib.Config{RunsRoot: t.TempDir(), Server: lib.ServerConfig{Listen: "127.0.0.1:0", Mode: schemas.ModeRecorded}}
	srv, err := server.New(cfg, server.Deps{Version: "test"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(handlers.OpenAPIPath)
	ctx.Request.SetHost("127.0.0.1")
	srv.Handler()(ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("GET %s: status %d: %s", handlers.OpenAPIPath, ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var doc map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &doc); err != nil {
		t.Fatalf("decode openapi: %v", err)
	}
	return doc, ctx
}

// TestOpenAPICoversEveryRoute fails when the server registers an /api/ route
// that handlers.APIRoutes does not describe. Add the route to the table in
// handlers/openapi.go.
func TestOpenAPICoversEveryRoute(t *testing.T) {
	doc, ctx := fetchOpenAPI(t)
	if got := string(ctx.Response.Header.Peek(lib.ModeHeader)); got != string(schemas.ModeRecorded) {
		t.Errorf("X-Bench-Mode = %q, want recorded", got)
	}
	if doc["openapi"] != "3.1.0" || doc["x-bench-mode"] != "recorded" {
		t.Errorf("openapi = %v, x-bench-mode = %v", doc["openapi"], doc["x-bench-mode"])
	}
	missing, _ := doc["x-bench-undocumented"].([]any)
	if len(missing) > 0 {
		var lines []string
		for _, m := range missing {
			lines = append(lines, "  "+m.(string))
		}
		t.Errorf("%d registered route(s) missing from handlers.APIRoutes (handlers/openapi.go):\n%s", len(missing), strings.Join(lines, "\n"))
	}
}

// TestAPIRoutesAreRegistered fails on a table entry for a route the server no
// longer registers: a stale description is as wrong as a missing one.
func TestAPIRoutesAreRegistered(t *testing.T) {
	doc, _ := fetchOpenAPI(t)
	paths := doc["paths"].(map[string]any)
	var stale []string
	for _, rd := range handlers.APIRoutes {
		item, ok := paths[rd.Path].(map[string]any)
		if !ok || item[strings.ToLower(rd.Method)] == nil {
			stale = append(stale, rd.Method+" "+rd.Path)
		}
	}
	if len(stale) > 0 {
		t.Errorf("handlers.APIRoutes describes routes the server does not register:\n  %s", strings.Join(stale, "\n  "))
	}
}

// TestOpenAPIRefsResolve walks the document and checks every $ref points at
// a component that exists.
func TestOpenAPIRefsResolve(t *testing.T) {
	doc, _ := fetchOpenAPI(t)
	comps := doc["components"].(map[string]any)["schemas"].(map[string]any)
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if ref, ok := x["$ref"].(string); ok {
				name := strings.TrimPrefix(ref, "#/components/schemas/")
				if _, found := comps[name]; !found {
					t.Errorf("dangling $ref %s", ref)
				}
			}
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	walk(doc)
}

func TestBuildOpenAPI(t *testing.T) {
	registered := map[string][]string{
		"GET":  {handlers.APIPrefix + "/experiments/{id}/verdict", handlers.APIPrefix + "/brand-new", "/{filepath:*}", "/"},
		"POST": {handlers.APIPrefix + "/experiments/plan"},
	}
	doc, undocumented := handlers.BuildOpenAPI(registered, "", schemas.ModeSynthetic)
	if want := []string{"GET " + handlers.APIPrefix + "/brand-new"}; !slices.Equal(undocumented, want) {
		t.Fatalf("undocumented = %v, want %v", undocumented, want)
	}
	paths := doc["paths"].(map[string]map[string]any)
	var keys []string
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	want := []string{handlers.APIPrefix + "/brand-new", handlers.APIPrefix + "/experiments/plan", handlers.APIPrefix + "/experiments/{id}/verdict"}
	if !slices.Equal(keys, want) {
		t.Fatalf("paths = %v, want %v (UI routes must be left out)", keys, want)
	}
	tests := []struct {
		name, path, method, opID string
		security                 bool
	}{
		{"read route", handlers.APIPrefix + "/experiments/{id}/verdict", "get", "getExperimentsByIdVerdict", false},
		{"write route", handlers.APIPrefix + "/experiments/plan", "post", "postExperimentsPlan", true},
		{"undocumented route", handlers.APIPrefix + "/brand-new", "get", "getBrandNew", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			op := paths[tc.path][tc.method].(map[string]any)
			if op["operationId"] != tc.opID {
				t.Errorf("operationId = %v, want %s", op["operationId"], tc.opID)
			}
			if _, ok := op["security"]; ok != tc.security {
				t.Errorf("security present = %v, want %v", ok, tc.security)
			}
		})
	}
	if doc["info"].(map[string]any)["version"] != "dev" {
		t.Errorf("empty version should read dev")
	}
}

// TestOpenAPISchemasMatchContract compares the derived component of every
// type that has a contract key list (testdata/contract/<Type>.keys) with
// that list: the description, the Go type and the UI type agree.
func TestOpenAPISchemasMatchContract(t *testing.T) {
	doc, _ := fetchOpenAPI(t)
	comps := doc["components"].(map[string]any)["schemas"].(map[string]any)
	files, err := filepath.Glob(filepath.Join("testdata", "contract", "*.keys"))
	if err != nil {
		t.Fatalf("glob contract: %v", err)
	}
	checked := 0
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".keys")
		comp, ok := comps[name].(map[string]any)
		if !ok {
			continue // the type is not reachable from any described route, or named differently in Go
		}
		checked++
		t.Run(name, func(t *testing.T) {
			required, optional := readKeys(t, f)
			// Optional (omitempty) properties may be newer than the key list:
			// the handler contract test catches them once a response carries
			// them. Required ones must agree both ways.
			reqGot := asStrings(comp["required"])
			for _, k := range required {
				if !slices.Contains(reqGot, k) {
					t.Errorf("contract key %q is required but the schema does not require it", k)
				}
			}
			for _, k := range reqGot {
				if !slices.Contains(required, k) && !slices.Contains(optional, k) {
					t.Errorf("schema requires %q, which is not in the contract", k)
				}
			}
		})
	}
	if checked < 10 {
		t.Errorf("only %d contract types matched a component; the derivation lost type names", checked)
	}
}

func readKeys(t *testing.T, path string) (required, optional []string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasSuffix(line, "?"):
			optional = append(optional, strings.TrimSuffix(line, "?"))
		default:
			required = append(required, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return required, optional
}

func asStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
