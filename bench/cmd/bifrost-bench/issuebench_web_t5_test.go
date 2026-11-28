package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/issuebench"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/valyala/fasthttp"
)

// Lane T5: From issues on the hosted site through the web's own request
// bodies (bench/ui/lib/store/apis/issuebenchApi.ts and fromIssues.tsx
// startBody): scan with the real scanner against a recorded GitHub API,
// start with a model and a cap, poll, accept, list. The build runs as an
// issuebench.work job on the fake shared worker of the W10 test.

// t5GitHub serves the GraphQL and REST calls scan.Scan makes for the
// fixture repository: one merged pull request (#12) that closes issue #11.
type t5GitHub struct {
	f     *ibeFixture
	diff  string
	mu    sync.Mutex
	calls []string
}

func (g *t5GitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.calls = append(g.calls, r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization"))
	g.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+ibeToken {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
		return
	}
	c := g.f.candidate
	switch r.URL.Path {
	case "/graphql":
		var q struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		var data any
		switch {
		case strings.Contains(q.Query, "pullRequests"):
			files := []map[string]any{}
			for _, p := range append(append([]string{}, c.CodeFiles...), c.TestFiles...) {
				files = append(files, map[string]any{"path": p, "additions": 3, "deletions": 1, "changeType": "MODIFIED"})
			}
			human := map[string]any{"__typename": "User", "login": "alice"}
			is := map[string]any{"__typename": "Issue", "number": c.Issue, "title": c.IssueTitle, "body": c.IssueBody, "createdAt": c.IssueCreated,
				"author": human, "repository": map[string]any{"nameWithOwner": ibeRepo}}
			pr := map[string]any{
				"number": c.PR, "title": "Fix add", "body": "", "createdAt": c.PRMerged.Add(-time.Hour), "mergedAt": c.PRMerged,
				"baseRefName": "main", "baseRefOid": c.BaseSHA, "author": human,
				"mergeCommit":             map[string]any{"oid": c.MergeSHA},
				"commits":                 map[string]any{"totalCount": 1, "nodes": []map[string]any{{"commit": map[string]any{"message": "fix add"}}}},
				"files":                   map[string]any{"totalCount": len(files), "nodes": files},
				"closingIssuesReferences": map[string]any{"totalCount": 1, "nodes": []map[string]any{is}},
			}
			data = map[string]any{"repository": map[string]any{"pullRequests": map[string]any{
				"pageInfo": map[string]any{"hasNextPage": false, "endCursor": "c1"},
				"nodes":    []map[string]any{pr},
			}}}
		default:
			data = map[string]any{"repository": map[string]any{"nameWithOwner": ibeRepo, "defaultBranchRef": map[string]any{"name": "main"}, "primaryLanguage": map[string]any{"name": "Python"}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	case "/repos/" + ibeRepo + "/commits/" + c.MergeSHA:
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": c.MergeSHA, "parents": []map[string]any{{"sha": c.BaseSHA}}})
	case "/repos/" + ibeRepo + "/compare/" + c.BaseSHA + "..." + c.MergeSHA:
		_, _ = io.WriteString(w, g.diff)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// t5Call sends one request through the org's real routes.
func t5Call(t *testing.T, oenv handlers.OrgEnv, method, path, body string) (int, []byte) {
	t.Helper()
	r := router.New()
	for _, area := range handlers.OrgAreas() {
		area(oenv, r)
	}
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(handlers.APIPrefix + path)
	if body != "" {
		ctx.Request.Header.SetContentType("application/json")
		ctx.Request.SetBodyString(body)
	}
	r.Handler(&ctx)
	return ctx.Response.StatusCode(), append([]byte(nil), ctx.Response.Body()...)
}

func TestIssuebenchHostedWebBodiesEndToEnd(t *testing.T) {
	f := newIbeFixture(t)
	f.candidate.IssueBody = "Calling add(2, 3) gives -1 instead of 5. The function should return the sum of both numbers it is given."
	chat, _ := ibeChat(t)
	s := newIbeServer(t, chat.URL)
	ibeSeams(t, s.gw, chat.URL)
	var serverCalls []string
	var serverMu sync.Mutex
	ibeServerGit(t, f, &serverCalls, &serverMu)

	gh := &t5GitHub{f: f, diff: ibeGit(t, f.bare, "diff", f.candidate.BaseSHA, f.candidate.MergeSHA)}
	ghSrv := httptest.NewServer(gh)
	defer ghSrv.Close()
	oldAPI := ibGitHubAPI
	ibGitHubAPI = ghSrv.URL
	t.Cleanup(func() { ibGitHubAPI = oldAPI })

	// The org's manager with the real stages (real scanner), made the way
	// the org area makes it.
	m := handlers.IssueBenchManager(s.root, func() pipeline.Stages { return issuebenchStagesFor(s.oenv) })

	// Readiness with no worker: the page's words, and a start is refused.
	code, body := t5Call(t, s.oenv, "GET", "/issuebench/readiness", "")
	if code != http.StatusOK {
		t.Fatalf("readiness: %d %s", code, body)
	}

	// 1. Scan: the body of scanIssues.
	code, body = t5Call(t, s.oenv, "POST", "/issuebench/runs", `{"repo":"`+ibeRepo+`","limit":10}`)
	if code != http.StatusCreated {
		t.Fatalf("scan: %d %s", code, body)
	}
	var run pipeline.State
	if err := json.Unmarshal(body, &run); err != nil {
		t.Fatal(err)
	}
	if len(run.Items) != 1 || run.Items[0].Candidate.Issue != f.candidate.Issue || run.Items[0].Candidate.BaseSHA != f.candidate.BaseSHA {
		t.Fatalf("scan found %+v funnel %+v", run.Items, run.Funnel)
	}
	if code, body := t5Call(t, s.oenv, "POST", "/issuebench/runs/"+run.ID+"/start", `{"id":"x","consent":true,"provider":"openai","model":"judge-model","budget_usd":1}`); code != http.StatusConflict ||
		!strings.Contains(string(body), "No worker online") {
		t.Fatalf("start with no worker: %d %s", code, body)
	}

	// 2. Start: the body startBody gives, plus the cap.
	w := &ibeWorker{}
	s.startWorker(t, f, w)
	if code, body := t5Call(t, s.oenv, "POST", "/issuebench/runs/"+run.ID+"/start", `{"consent":true,"provider":"openai","model":"judge-model","budget_usd":-1}`); code != http.StatusBadRequest {
		t.Fatalf("negative cap: %d %s", code, body)
	}
	if code, body := t5Call(t, s.oenv, "POST", "/issuebench/runs/"+run.ID+"/start", `{"consent":true,"provider":"openai","model":"judge-model","budget_usd":1}`); code != http.StatusAccepted {
		t.Fatalf("start: %d %s", code, body)
	}

	// 3. Poll GET /runs/{id} until the issue is ready or failed.
	var cur pipeline.State
	ibeWait(t, "the issue to be ready", 60*time.Second, func() bool {
		code, body := t5Call(t, s.oenv, "GET", "/issuebench/runs/"+run.ID+"?since=0", "")
		if code != http.StatusOK || json.Unmarshal(body, &cur) != nil || len(cur.Items) != 1 {
			return false
		}
		st := cur.Items[0].Status
		return st == pipeline.StatusReady || st == pipeline.StatusFailed
	})
	m.Wait()
	it := cur.Items[0]
	if it.Status != pipeline.StatusReady {
		var b strings.Builder
		for _, l := range cur.Log {
			b.WriteString(l.Line + "\n")
		}
		t.Fatalf("item %s: %s (fix %q)\n%s", it.Status, it.Reason, it.Fix, b.String())
	}
	if v := it.Validation; v == nil || len(v.FailToPass) != 1 || len(v.PassToPass) != 1 {
		t.Fatalf("validation %+v", it.Validation)
	}
	if it.Options.BudgetUSD != 1 {
		t.Fatalf("item cap %v, want the start's 1", it.Options.BudgetUSD)
	}
	page, err := s.runner.List(context.Background(), ibeOrg, jobs.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var builds int
	for _, j := range page.Jobs {
		if j.Kind != issuebench.WorkJobKind {
			continue
		}
		var p issuebench.WorkParams
		if err := json.Unmarshal(j.Params, &p); err != nil {
			t.Fatal(err)
		}
		if p.Request.Mode == issuebench.WorkModeBuild {
			builds++
			if p.Request.MaxUSD != 1 {
				t.Fatalf("build job cap %v, want 1", p.Request.MaxUSD)
			}
		}
	}
	if builds != 1 {
		t.Fatalf("%d build jobs", builds)
	}

	// 4. Accept: the body of reviewIssue; the task is emitted.
	code, body = t5Call(t, s.oenv, "POST", "/issuebench/runs/"+run.ID+"/issues/"+strconv.Itoa(f.candidate.Issue)+"/review", `{"decision":"accept"}`)
	if code != http.StatusOK {
		t.Fatalf("accept: %d %s", code, body)
	}
	if err := json.Unmarshal(body, &cur); err != nil || cur.Benchmark == "" {
		t.Fatalf("accept answer %s: %v", body, err)
	}

	// 5. List: the run shows with its benchmark and the accepted issue.
	code, body = t5Call(t, s.oenv, "GET", "/issuebench/runs", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}
	var list handlers.IssueRunList
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range list.Runs {
		if r.ID == run.ID && r.Benchmark == cur.Benchmark && len(r.Items) == 1 && r.Items[0].Status == pipeline.StatusAccepted {
			found = true
		}
	}
	if !found {
		t.Fatalf("list %s", body)
	}
	ibeWait(t, "lint, lock and admit", 10*time.Second, func() bool {
		s.amu.Lock()
		defer s.amu.Unlock()
		return len(*s.admit) == len(pipeline.AdmitKinds)
	})
	gh.mu.Lock()
	defer gh.mu.Unlock()
	if len(gh.calls) == 0 {
		t.Fatal("the scan made no GitHub call")
	}
}
