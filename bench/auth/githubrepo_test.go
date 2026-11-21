package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeRepoGitHub serves the API calls OpenCIInstallPR and Refs make.
func fakeRepoGitHub(t *testing.T, fileExists bool) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	mux.HandleFunc("/repos/acme/widgets", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]string{"default_branch": "main"})
	})
	mux.HandleFunc("/repos/acme/widgets/branches", func(w http.ResponseWriter, r *http.Request) {
		ok(w, []map[string]any{{"name": "main", "commit": map[string]string{"sha": "s1"}}})
	})
	mux.HandleFunc("/repos/acme/widgets/tags", func(w http.ResponseWriter, r *http.Request) {
		ok(w, []map[string]any{{"name": "v1", "commit": map[string]string{"sha": "s2"}}})
	})
	mux.HandleFunc("/repos/acme/widgets/git/ref/heads/main", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"object": map[string]string{"sha": "base-sha"}})
	})
	mux.HandleFunc("/repos/acme/widgets/contents/.github/workflows/heimdall.yml", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" contents")
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer token")
		}
		if r.Method == http.MethodGet {
			if !fileExists {
				w.WriteHeader(http.StatusNotFound)
				ok(w, map[string]string{"message": "Not Found"})
				return
			}
			ok(w, map[string]string{"sha": "old"})
			return
		}
		ok(w, map[string]any{})
	})
	mux.HandleFunc("/repos/acme/widgets/git/refs", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, "branch "+body["ref"]+" "+body["sha"])
		w.WriteHeader(http.StatusCreated)
		ok(w, map[string]any{})
	})
	mux.HandleFunc("/repos/acme/widgets/pulls", func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, "pull")
		w.WriteHeader(http.StatusCreated)
		ok(w, map[string]any{"number": 7, "html_url": "https://github.com/acme/widgets/pull/7"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &calls
}

func testConnector(api string) *GitHubRepoConnector {
	return NewGitHubRepoConnector(Config{PublicURL: "https://h.example", GitHub: &GitHubConfig{ClientID: "id", ClientSecret: "sec", APIURL: api}})
}

func TestOpenCIInstallPR(t *testing.T) {
	srv, calls := fakeRepoGitHub(t, false)
	c := testConnector(srv.URL)
	res, err := c.OpenCIInstallPR(context.Background(), "tok", CIInstall{Owner: "acme", Repo: "widgets", Branch: "heimdall/ci", Path: ".github/workflows/heimdall.yml", Content: "name: x\n", Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Number != 7 || res.Base != "main" || res.Branch != "heimdall/ci" {
		t.Fatalf("result %+v", res)
	}
	got := strings.Join(*calls, "|")
	if !strings.Contains(got, "branch refs/heads/heimdall/ci base-sha") || !strings.Contains(got, "PUT contents") || !strings.HasSuffix(got, "pull") {
		t.Fatalf("calls %s", got)
	}
}

func TestOpenCIInstallPRRefusesExistingFile(t *testing.T) {
	srv, calls := fakeRepoGitHub(t, true)
	_, err := testConnector(srv.URL).OpenCIInstallPR(context.Background(), "tok", CIInstall{Owner: "acme", Repo: "widgets", Path: ".github/workflows/heimdall.yml"})
	if !errors.Is(err, ErrWorkflowExists) {
		t.Fatalf("err %v", err)
	}
	for _, c := range *calls {
		if strings.HasPrefix(c, "branch") {
			t.Fatal("branch created although the file exists")
		}
	}
}

func TestRefs(t *testing.T) {
	srv, _ := fakeRepoGitHub(t, false)
	r, err := testConnector(srv.URL).Refs(context.Background(), "tok", "acme", "widgets")
	if err != nil {
		t.Fatal(err)
	}
	if r.DefaultBranch != "main" || len(r.Refs) != 2 || r.Refs[1].Kind != "tag" {
		t.Fatalf("refs %+v", r)
	}
	if _, err := testConnector(srv.URL).Refs(context.Background(), "tok", "..", "x"); err == nil {
		t.Fatal("bad owner accepted")
	}
}

func TestValidNames(t *testing.T) {
	for _, b := range []string{"main", "feature/x", "v1.2"} {
		if !ValidBranch(b) {
			t.Errorf("%s refused", b)
		}
	}
	for _, b := range []string{"../x", "-x", "a/", "/a", "x.lock", "a b"} {
		if ValidBranch(b) {
			t.Errorf("%s accepted", b)
		}
	}
	if ValidRepo("a/b", "c") || !ValidRepo("acme", "widgets.go") {
		t.Error("ValidRepo")
	}
}

func TestUnconfiguredConnector(t *testing.T) {
	c := NewGitHubRepoConnector(Config{})
	if c.Configured() {
		t.Fatal("configured without an app")
	}
}
