package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeGitHub serves the GraphQL and REST calls Scan makes.
type fakeGitHub struct {
	t       *testing.T
	pages   [][]map[string]any // pull request pages
	issues  map[int]map[string]any
	parents map[string]string
	diffs   map[string]string // "base...head" -> diff
	calls   int
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls++
	if r.Header.Get("Authorization") != "Bearer tok" {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
		return
	}
	switch {
	case r.URL.Path == "/graphql":
		var q struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		var data any
		switch {
		case strings.Contains(q.Query, "pullRequests"):
			i := 0
			if a, ok := q.Variables["after"].(string); ok && a != "" {
				fmt.Sscanf(a, "c%d", &i)
			}
			data = map[string]any{"repository": map[string]any{"pullRequests": map[string]any{
				"pageInfo": map[string]any{"hasNextPage": i+1 < len(f.pages), "endCursor": fmt.Sprintf("c%d", i+1)},
				"nodes":    f.pages[i],
			}}}
		case strings.Contains(q.Query, "issueOrPullRequest"):
			n := int(q.Variables["number"].(float64))
			is := f.issues[n]
			if is == nil {
				data = map[string]any{"repository": map[string]any{"issueOrPullRequest": nil}}
			} else {
				data = map[string]any{"repository": map[string]any{"issueOrPullRequest": is}}
			}
		default:
			data = map[string]any{"repository": map[string]any{"nameWithOwner": "o/r", "defaultBranchRef": map[string]any{"name": "main"}, "primaryLanguage": map[string]any{"name": "Python"}}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	case strings.HasPrefix(r.URL.Path, "/repos/o/r/commits/"):
		sha := strings.TrimPrefix(r.URL.Path, "/repos/o/r/commits/")
		p, ok := f.parents[sha]
		if !ok {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sha": sha, "parents": []map[string]any{{"sha": p}}})
	case strings.HasPrefix(r.URL.Path, "/repos/o/r/compare/"):
		k := strings.TrimPrefix(r.URL.Path, "/repos/o/r/compare/")
		d, ok := f.diffs[k]
		if !ok {
			w.WriteHeader(404)
			return
		}
		_, _ = io.WriteString(w, d)
	default:
		f.t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
		w.WriteHeader(404)
	}
}

var (
	t0    = time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	human = map[string]any{"__typename": "User", "login": "alice"}
)

func issue(n int, body string, created time.Time) map[string]any {
	return map[string]any{"__typename": "Issue", "number": n, "title": fmt.Sprintf("Bug %d", n), "body": body, "createdAt": created,
		"author": human, "repository": map[string]any{"nameWithOwner": "o/r"}}
}

func pr(n int, author map[string]any, body string, closing []map[string]any, files []string, commits int) map[string]any {
	fs := []map[string]any{}
	for _, f := range files {
		fs = append(fs, map[string]any{"path": f, "additions": 3, "deletions": 1, "changeType": "MODIFIED"})
	}
	if closing == nil {
		closing = []map[string]any{}
	}
	return map[string]any{
		"number": n, "title": fmt.Sprintf("PR %d", n), "body": body, "createdAt": t0.Add(48 * time.Hour), "mergedAt": t0.Add(72 * time.Hour),
		"baseRefName": "main", "baseRefOid": "x", "author": author,
		"mergeCommit":             map[string]any{"oid": fmt.Sprintf("m%d", n)},
		"commits":                 map[string]any{"totalCount": commits, "nodes": []map[string]any{{"commit": map[string]any{"message": "work"}}}},
		"files":                   map[string]any{"totalCount": len(fs), "nodes": fs},
		"closingIssuesReferences": map[string]any{"totalCount": len(closing), "nodes": closing},
	}
}

func diff(files map[string]string) string {
	var b strings.Builder
	for _, p := range []string{"src/pkg/mod.py", "tests/test_mod.py"} {
		body, ok := files[p]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\nindex 1..2 100644\n--- a/%s\n+++ b/%s\n@@ -1,2 +1,3 @@\n def old():\n-    return 1\n%s", p, p, p, p, body)
	}
	return b.String()
}

const longBody = "Calling parse() on an empty header raises KeyError instead of returning an empty dict. Steps to reproduce below."

func TestScanFunnel(t *testing.T) {
	both := []string{"src/pkg/mod.py", "tests/test_mod.py"}
	good := diff(map[string]string{"src/pkg/mod.py": "+    return {}\n", "tests/test_mod.py": "+def test_empty():\n+    assert parse('') == {}\n"})
	f := &fakeGitHub{t: t,
		pages: [][]map[string]any{
			{
				pr(10, human, "", []map[string]any{issue(1, longBody, t0)}, both, 1),
				pr(11, map[string]any{"__typename": "Bot", "login": "dependabot"}, "Fixes #2", nil, both, 1),
				pr(12, human, "small cleanup", nil, both, 1),
				pr(13, human, "", []map[string]any{issue(3, longBody, t0)}, []string{"src/pkg/mod.py", "README.md"}, 1),
			},
			{
				pr(14, human, "", []map[string]any{issue(4, longBody+" Maybe add normalize_header_name?", t0)}, both, 1),
				pr(15, human, "This closes #5.", nil, both, 2),
				pr(16, human, "", []map[string]any{issue(6, longBody, t0.Add(100*time.Hour))}, both, 1),
				pr(17, human, "", []map[string]any{issue(7, longBody, t0)}, both, 1),
				pr(18, human, "", []map[string]any{issue(8, longBody, t0), issue(9, longBody, t0)}, both, 1),
			},
		},
		issues:  map[int]map[string]any{2: issue(2, longBody, t0), 5: issue(5, longBody, t0)},
		parents: map[string]string{"m10": "b10", "m14": "b14", "m15": "r15", "r15": "b15", "m17": "b17"},
		diffs: map[string]string{
			"b10...m10": good,
			"b14...m14": diff(map[string]string{"src/pkg/mod.py": "+    return normalize_header_name(h)\n", "tests/test_mod.py": "+def test_x():\n+    pass\n"}),
			"r15...m15": diff(map[string]string{"src/pkg/mod.py": "+    return {}\n"}), // rebase merge: last commit only
			"b15...m15": good,
			"b17...m17": diff(map[string]string{"src/pkg/mod.py": "+    return {}\n", "tests/test_mod.py": "+@pytest.mark.network\n+def test_live():\n+    pass\n"}),
		},
	}
	srv := httptest.NewServer(f)
	defer srv.Close()
	res, err := Scan(context.Background(), &Client{APIURL: srv.URL, Token: "tok"}, Options{Repo: "o/r"})
	if err != nil {
		t.Fatal(err)
	}
	if res.DefaultBranch != "main" || res.Language != "python" {
		t.Fatalf("meta %q %q", res.DefaultBranch, res.Language)
	}
	fn := res.Funnel
	if fn.MergedPRs != 9 || fn.Linked != 8 || fn.Qualified != 2 {
		t.Fatalf("funnel %+v", fn)
	}
	want := map[string]int{ReasonBot: 1, ReasonNoIssue: 1, ReasonNoTests: 1, ReasonLeak: 1, ReasonIssueAfterPR: 1, ReasonNetwork: 1, ReasonManyIssues: 1}
	for k, v := range want {
		if fn.Reasons[k] != v {
			t.Errorf("reason %s = %d, want %d (all %v)", k, fn.Reasons[k], v, fn.Reasons)
		}
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("candidates %+v", res.Candidates)
	}
	c := res.Candidates[0]
	if c.PR != 10 || c.Issue != 1 || c.BaseSHA != "b10" || c.MergeSHA != "m10" || c.Language != "python" {
		t.Errorf("candidate %+v", c)
	}
	if !strings.Contains(c.TestPatch, "tests/test_mod.py") || strings.Contains(c.TestPatch, "src/pkg") || !strings.Contains(c.FixPatch, "src/pkg/mod.py") {
		t.Errorf("patch split wrong:\n%s\n---\n%s", c.TestPatch, c.FixPatch)
	}
	if len(c.TestFiles) != 1 || len(c.CodeFiles) != 1 {
		t.Errorf("files %v %v", c.TestFiles, c.CodeFiles)
	}
	if c2 := res.Candidates[1]; c2.PR != 15 || c2.Issue != 5 || c2.BaseSHA != "b15" {
		t.Errorf("keyword + rebase candidate %+v", c2)
	}
	for _, d := range res.Dropped {
		if d.Skip == "" || d.FixPatch != "" || d.TestPatch != "" {
			t.Errorf("dropped entry %+v", d)
		}
	}
}

func TestScanMaxPRsAndAuth(t *testing.T) {
	both := []string{"src/a.py", "tests/test_a.py"}
	f := &fakeGitHub{t: t, pages: [][]map[string]any{{pr(1, human, "", nil, both, 1), pr(2, human, "", nil, both, 1)}, {pr(3, human, "", nil, both, 1)}}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	res, err := Scan(context.Background(), &Client{APIURL: srv.URL, Token: "tok"}, Options{Repo: "https://github.com/o/r", MaxPRs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if res.Funnel.MergedPRs != 2 || !res.Truncated {
		t.Errorf("max_prs: %+v truncated=%v", res.Funnel, res.Truncated)
	}
	_, err = Scan(context.Background(), &Client{APIURL: srv.URL, Token: "bad"}, Options{Repo: "o/r"})
	var ae *APIError
	if !asAPI(err, &ae) || ae.Status != 401 {
		t.Errorf("auth error %v", err)
	}
	if _, err := Scan(context.Background(), &Client{APIURL: srv.URL, Token: "tok"}, Options{Repo: "not a repo"}); err != ErrBadRepo {
		t.Errorf("bad repo %v", err)
	}
}

func asAPI(err error, out **APIError) bool {
	ae, ok := err.(*APIError)
	*out = ae
	return ok
}

func TestKeywordIssues(t *testing.T) {
	got := KeywordIssues("Fixes #12, closes o/r#13, resolves other/x#14, fixed: #15, mentions #16, Fix #12", "o/r")
	if fmt.Sprint(got) != "[12 13 15]" {
		t.Errorf("got %v", got)
	}
}

func TestHeuristicTestFile(t *testing.T) {
	for p, want := range map[string]bool{"tests/a.py": true, "pkg/test_a.py": true, "pkg/a_test.go": true, "src/a.spec.ts": true, "conftest.py": true, "src/a.py": false, "src/testing_utils.py": false} {
		if HeuristicTestFile(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}

func TestLeaksAndEnv(t *testing.T) {
	fix := "--- a/x.py\n+++ b/x.py\n@@ -1 +1 @@\n def f():\n-    return old_value\n+    return parse_header_value(x, strict=True)\n"
	if !Leaks("you should call parse_header_value here", fix) {
		t.Error("new identifier in issue not caught")
	}
	if Leaks("the f function returns old_value which is wrong", fix) {
		t.Error("old identifier counted as leak")
	}
	if !Leaks("patch: return parse_header_value(x, strict=True)", fix) {
		t.Error("whole line not caught")
	}
	if r := envReason("+++ b/t.py\n+    tok = os.environ[\"GITHUB_TOKEN\"]\n", ""); r != ReasonSecret {
		t.Errorf("secret: %q", r)
	}
	if r := envReason("+++ b/t.py\n+    requests.get(\"https://example.com\")\n", ""); r != ReasonNetwork {
		t.Errorf("network: %q", r)
	}
}

func TestScanSinceStops(t *testing.T) {
	both := []string{"src/a.py", "tests/test_a.py"}
	f := &fakeGitHub{t: t, pages: [][]map[string]any{{pr(1, human, "", nil, both, 1)}, {pr(2, human, "", nil, both, 1)}}}
	srv := httptest.NewServer(f)
	defer srv.Close()
	res, err := Scan(context.Background(), &Client{APIURL: srv.URL, Token: "tok"}, Options{Repo: "o/r", Since: t0.Add(1000 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Funnel.MergedPRs != 0 || res.Truncated || f.calls != 2 {
		t.Errorf("since: %+v truncated=%v calls=%d", res.Funnel, res.Truncated, f.calls)
	}
}
