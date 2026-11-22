package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

func issueScanCall(t *testing.T, h *IssueBenchHandler, body string) (int, map[string]any) {
	t.Helper()
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI(IssueBenchPrefix + "/scan")
	ctx.Request.SetBodyString(body)
	tenancy.SetOrg(&ctx, &tenancy.Org{ID: "org1"})
	h.postScan(&ctx)
	var out map[string]any
	_ = json.Unmarshal(ctx.Response.Body(), &out)
	return ctx.Response.StatusCode(), out
}

func TestIssueScanRoute(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer pub" {
			w.WriteHeader(401)
			return
		}
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "pullRequests") {
			_, _ = io.WriteString(w, `{"data":{"repository":{"pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[]}}}}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":{"repository":{"nameWithOwner":"o/r","defaultBranchRef":{"name":"main"},"primaryLanguage":{"name":"Python"}}}}`)
	}))
	defer gh.Close()
	h := NewIssueBenchHandler(schemas.ModeLive, nil, "no master key", gh.URL)

	if st, _ := issueScanCall(t, h, `{"repo":"o/r"}`); st != 503 {
		t.Errorf("no keys, no token: %d", st)
	}
	if st, _ := issueScanCall(t, h, `{"repo":"bad repo","token":"pub"}`); st != 400 {
		t.Errorf("bad repo: %d", st)
	}
	if st, _ := issueScanCall(t, h, `{"repo":"o/r","token":"pub","max_prs":5000}`); st != 400 {
		t.Errorf("max_prs: %d", st)
	}
	if st, _ := issueScanCall(t, h, `{"repo":"o/r","token":"wrong"}`); st != 409 {
		t.Errorf("bad token: %d", st)
	}
	st, out := issueScanCall(t, h, `{"repo":"o/r","token":"pub"}`)
	if st != 200 || out["default_branch"] != "main" || out["language"] != "python" {
		t.Fatalf("scan: %d %v", st, out)
	}
	if _, ok := out["candidates"].([]any); !ok {
		t.Errorf("candidates not a list: %v", out)
	}
	if strings.Contains(string(mustJSON(out)), "pub") {
		t.Error("token echoed")
	}
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
