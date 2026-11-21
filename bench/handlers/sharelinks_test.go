package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// shareRouter is an org router with the report card, share and reproduce
// areas over one evaluation (lane X14).
func shareRouter(t *testing.T) (*router.Router, string) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(ReportCardEvaluationsDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ev := schemas.Evaluation{ID: "ev_1", Status: "done", CreatedAt: time.Now(), CreatedBy: "usr_secret_person",
		Spec: schemas.EvaluationSpec{Name: "Codex v2 check", Mode: "versions", TasksPerBenchmark: 8, Trials: 2, Arms: []schemas.EvaluationArm{
			{Label: "A", Agent: "codex", Version: 1, Model: "gpt-5.4-mini"},
			{Label: "B", Agent: "codex", Version: 2, Model: "gpt-5.4-mini"},
		}},
		Children: []schemas.EvaluationChild{{Benchmark: "coding-core", ExperimentID: testExp, Status: "done"}},
	}
	data, _ := json.Marshal(ev)
	if err := os.WriteFile(filepath.Join(dir, "ev_1.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "codex.yaml"), []byte("id: codex\nenv:\n  OPENAI_API_KEY: sk-abcdefghijklmnopqrstuvwxyz123456\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Index: leaderboardIndex()}}
	r := router.New()
	registerReportCard(env, r)
	registerShareLinks(env, r)
	eh := &evaluationsHandler{env: env, now: time.Now}
	r.GET(APIPrefix+"/evaluations/{id}", env.R(eh.get))
	h := &reproduceHandler{env: env, now: time.Now}
	r.GET(APIPrefix+"/evaluations/{id}/reproduce", env.R(h.get))
	return r, root
}

func shareCall(r *router.Router, method, uri string, body any) *fasthttp.Response {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	if body != nil {
		b, _ := json.Marshal(body)
		ctx.Request.SetBody(b)
	}
	r.Handler(&ctx)
	var out fasthttp.Response
	ctx.Response.CopyTo(&out)
	return &out
}

func TestShareLinkLifecycle(t *testing.T) {
	r, _ := shareRouter(t)

	// Unknown kind and unknown item are refused.
	if res := shareCall(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "secrets", "target": "x"}); res.StatusCode() != 400 {
		t.Fatalf("bad kind: %d %s", res.StatusCode(), res.Body())
	}
	if res := shareCall(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_missing"}); res.StatusCode() != 404 {
		t.Fatalf("missing item: %d %s", res.StatusCode(), res.Body())
	}
	if res := shareCall(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "leaderboard", "target": "models", "query": "split=heldout"}); res.StatusCode() != 400 {
		t.Fatalf("held-out board must not be shareable: %d", res.StatusCode())
	}

	res := shareCall(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1", "label": "codex", "expires_in_days": 7})
	if res.StatusCode() != 201 {
		t.Fatalf("create: %d %s", res.StatusCode(), res.Body())
	}
	var sv ShareView
	if err := json.Unmarshal(res.Body(), &sv); err != nil || sv.Token == "" || sv.State != "active" || sv.ExpiresAt == nil || !strings.HasPrefix(sv.Path, "/s/") {
		t.Fatalf("view %+v %v", sv, err)
	}

	res = shareCall(r, "GET", APIPrefix+"/shared/"+sv.Token, nil)
	if res.StatusCode() != 200 {
		t.Fatalf("open: %d %s", res.StatusCode(), res.Body())
	}
	if bytes.Contains(res.Body(), []byte("usr_secret_person")) {
		t.Fatalf("shared page leaks who started it: %s", res.Body())
	}
	var item SharedItem
	if err := json.Unmarshal(res.Body(), &item); err != nil {
		t.Fatal(err)
	}
	if item.Share.Kind != "evaluation" || item.Data["evaluation"] == nil || item.Data["report"] == nil || !item.Share.Badge || len(item.Share.Exports) != 2 {
		t.Fatalf("item %+v", item.Share)
	}

	res = shareCall(r, "GET", APIPrefix+"/shared/"+sv.Token+"/badge.svg", nil)
	if res.StatusCode() != 200 || !bytes.HasPrefix(res.Body(), []byte("<svg")) || !bytes.Contains(res.Body(), []byte("codex")) {
		t.Fatalf("badge: %d %s", res.StatusCode(), res.Body())
	}
	if string(res.Header.Peek(lib.CORPHeader)) != lib.CORPCrossOrigin {
		t.Fatalf("badge must be embeddable")
	}

	res = shareCall(r, "GET", APIPrefix+"/shared/"+sv.Token+"/export?format=md", nil)
	if res.StatusCode() != 200 || !bytes.Contains(res.Body(), []byte("# Codex v2 check")) || !bytes.Contains(res.Body(), []byte("## By benchmark")) {
		t.Fatalf("shared export: %d %s", res.StatusCode(), res.Body())
	}

	// A token with another signature, or of another org, is 404.
	bad := sv.Token[:len(sv.Token)-3] + "AAA"
	if res := shareCall(r, "GET", APIPrefix+"/shared/"+bad, nil); res.StatusCode() != 404 {
		t.Fatalf("forged token: %d", res.StatusCode())
	}
	if res := shareCall(r, "GET", APIPrefix+"/shared/other"+sv.Token[strings.Index(sv.Token, "."):], nil); res.StatusCode() != 404 {
		t.Fatalf("other org token: %d", res.StatusCode())
	}

	res = shareCall(r, "GET", APIPrefix+"/shares", nil)
	var list ShareList
	if err := json.Unmarshal(res.Body(), &list); err != nil || len(list.Shares) != 1 {
		t.Fatalf("list %s", res.Body())
	}

	if res := shareCall(r, "POST", APIPrefix+"/shares/"+sv.ID+"/revoke", nil); res.StatusCode() != 200 {
		t.Fatalf("revoke: %d %s", res.StatusCode(), res.Body())
	}
	if res := shareCall(r, "GET", APIPrefix+"/shared/"+sv.Token, nil); res.StatusCode() != 410 {
		t.Fatalf("revoked link: %d", res.StatusCode())
	}
	if res := shareCall(r, "GET", APIPrefix+"/shared/"+sv.Token+"/badge.svg", nil); res.StatusCode() != 410 {
		t.Fatalf("revoked badge: %d", res.StatusCode())
	}
}

func TestReportExportFormats(t *testing.T) {
	r, _ := shareRouter(t)
	res := shareCall(r, "GET", APIPrefix+"/evaluations/ev_1/report/export?format=html", nil)
	body := string(res.Body())
	if res.StatusCode() != 200 || !strings.HasPrefix(body, "<!doctype html>") || !strings.Contains(body, "@media print") || strings.Contains(body, "<script") {
		t.Fatalf("html: %d %.300s", res.StatusCode(), body)
	}
	if !strings.Contains(string(res.Header.Peek("Content-Disposition")), "ev_1-report-card.html") {
		t.Fatalf("disposition %s", res.Header.Peek("Content-Disposition"))
	}
	if strings.Contains(body, "usr_secret_person") {
		t.Fatal("export leaks who started it")
	}
	res = shareCall(r, "GET", APIPrefix+"/evaluations/ev_1/report/export?format=json", nil)
	var out map[string]any
	if res.StatusCode() != 200 || json.Unmarshal(res.Body(), &out) != nil || out["report"] == nil {
		t.Fatalf("json: %d %s", res.StatusCode(), res.Body())
	}
	if res := shareCall(r, "GET", APIPrefix+"/evaluations/ev_nope/report/export", nil); res.StatusCode() != 404 {
		t.Fatalf("missing: %d", res.StatusCode())
	}
}

func TestReproduceBundle(t *testing.T) {
	r, _ := shareRouter(t)
	res := shareCall(r, "GET", APIPrefix+"/evaluations/ev_1/reproduce", nil)
	if res.StatusCode() != 200 || string(res.Header.ContentType()) != "application/zip" {
		t.Fatalf("bundle: %d %s", res.StatusCode(), res.Body())
	}
	zr, err := zip.NewReader(bytes.NewReader(res.Body()), int64(len(res.Body())))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		var b bytes.Buffer
		_, _ = b.ReadFrom(rc)
		rc.Close()
		got[f.Name] = b.String()
	}
	for _, want := range []string{"REPRODUCE.md", "evaluation-spec.json", "evaluation.json", "locks.json", "agents/codex.yaml"} {
		if _, ok := got[want]; !ok {
			t.Errorf("bundle lacks %s (has %v)", want, keysOf(got))
		}
	}
	if strings.Contains(got["agents/codex.yaml"], "sk-abcdefghijklmnop") {
		t.Fatal("bundle leaks a key")
	}
	if strings.Contains(got["evaluation.json"], "usr_secret_person") {
		t.Fatal("bundle leaks who started it")
	}
	if !strings.Contains(got["REPRODUCE.md"], "/api/bench/evaluations") || !strings.Contains(got["REPRODUCE.md"], "coding-core") {
		t.Fatalf("readme %s", got["REPRODUCE.md"])
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
