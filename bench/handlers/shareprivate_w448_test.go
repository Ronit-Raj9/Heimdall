package handlers

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/issuesync"
)

// TestSharePageHidesPrivateSourceTasks: once a benchmark is marked as built
// from a private repository, a share link that shows it is refused when it
// is made, and an existing link no longer opens, exports or draws a badge.
func TestSharePageHidesPrivateSourceTasks(t *testing.T) {
	r, root := shareRouter(t)
	res := shareCall(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1"})
	if res.StatusCode() != 201 {
		t.Fatalf("create before the mark: %d %s", res.StatusCode(), res.Body())
	}
	var sv ShareView
	if err := json.Unmarshal(res.Body(), &sv); err != nil || sv.Token == "" {
		t.Fatalf("view %s", res.Body())
	}
	if err := issuesync.MarkPrivateSource(root, "coding-core", "acme/secret-app", time.Now()); err != nil {
		t.Fatal(err)
	}
	want := "This cannot be shared outside your organization: it shows tasks of coding-core, a benchmark built from a private repository."
	for _, path := range []string{"/shared/" + sv.Token, "/shared/" + sv.Token + "/export?format=md", "/shared/" + sv.Token + "/badge.svg"} {
		res := shareCall(r, "GET", APIPrefix+path, nil)
		if res.StatusCode() != 403 || !bytes.Contains(res.Body(), []byte(want)) {
			t.Fatalf("open %s: %d %s", path, res.StatusCode(), res.Body())
		}
		if bytes.Contains(res.Body(), []byte("Codex v2 check")) || bytes.Contains(res.Body(), []byte(testExp)) {
			t.Fatalf("refusal of %s leaks the item: %s", path, res.Body())
		}
	}
	res = shareCall(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1"})
	if res.StatusCode() != 403 || !bytes.Contains(res.Body(), []byte(want)) {
		t.Fatalf("create after the mark: %d %s", res.StatusCode(), res.Body())
	}
	// Marking again changes nothing.
	if err := issuesync.MarkPrivateSource(root, "coding-core", "acme/secret-app", time.Now()); err != nil {
		t.Fatal(err)
	}
	if list, _ := issuesync.PrivateSources(root); len(list) != 1 {
		t.Fatalf("private sources %+v", list)
	}
}

// TestNamesPrivateSource: a run trace or leaderboard answer names the
// benchmark by id, id@version, its twins or the repository.
func TestNamesPrivateSource(t *testing.T) {
	names := map[string]string{"issues-acme-secret": "issues-acme-secret", "issues-acme-secret-twins": "issues-acme-secret", "acme/secret": "issues-acme-secret"}
	for _, body := range []string{
		`{"run":{"suite":"issues-acme-secret@3","task":"t1"}}`,
		`{"entries":[{"suite":"issues-acme-secret-twins@1"}]}`,
		`{"tasks":{"issues-acme-secret":["t1"]}}`,
		`{"repo":"Acme/Secret"}`,
	} {
		if got := namesPrivateSource([]byte(body), names); got != "issues-acme-secret" {
			t.Errorf("%s: %q", body, got)
		}
	}
	if got := namesPrivateSource([]byte(`{"suite":"issues-acme-public@1","note":"see issues-acme-secret later"}`), names); got != "" {
		t.Errorf("free text matched: %q", got)
	}
}

// TestFirstPrivateMarksShareRefusal: the weekly sync's first private run
// marks the benchmark for the share check.
func TestFirstPrivateMarksShareRefusal(t *testing.T) {
	root := t.TempDir()
	if err := markPrivateSource(root, issuesync.Repo{Repo: "acme/secret", Benchmark: "issues-acme-secret"}); err != nil {
		t.Fatal(err)
	}
	list, err := issuesync.PrivateSources(root)
	if err != nil || len(list) != 1 || list[0].Benchmark != "issues-acme-secret" || list[0].Repo != "acme/secret" {
		t.Fatalf("private sources %+v %v", list, err)
	}
}

// TestShareRecordedSources: a benchmark recorded private (by any path:
// issue work job, issue emitter, task from a commit, find tasks, weekly
// sync, all through issuesync.RecordSource) refuses create, open, export
// and badge; a benchmark recorded public still shares; a record file that
// cannot be read refuses every share.
func TestShareRecordedSources(t *testing.T) {
	want := "This cannot be shared outside your organization"

	// Public record: shares, opens, exports and draws the badge.
	r, root := shareRouter(t)
	if err := issuesync.RecordSource(root, "coding-core", "acme/open", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	res := shareCall(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1"})
	if res.StatusCode() != 201 {
		t.Fatalf("public create: %d %s", res.StatusCode(), res.Body())
	}
	var sv ShareView
	_ = json.Unmarshal(res.Body(), &sv)
	paths := []string{"/shared/" + sv.Token, "/shared/" + sv.Token + "/export?format=md", "/shared/" + sv.Token + "/badge.svg"}
	for _, p := range paths {
		if res := shareCall(r, "GET", APIPrefix+p, nil); res.StatusCode() != 200 {
			t.Fatalf("public %s: %d %s", p, res.StatusCode(), res.Body())
		}
	}

	// The same benchmark recorded private later (the repository turned
	// private): every route refuses.
	if err := issuesync.RecordSource(root, "coding-core", "acme/open", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if res := shareCall(r, "GET", APIPrefix+p, nil); res.StatusCode() != 403 || !bytes.Contains(res.Body(), []byte(want)) {
			t.Fatalf("private %s: %d %s", p, res.StatusCode(), res.Body())
		}
	}
	if res := shareCall(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1"}); res.StatusCode() != 403 {
		t.Fatalf("private create: %d %s", res.StatusCode(), res.Body())
	}
	// Recording it public again does not undo private.
	if err := issuesync.RecordSource(root, "coding-core", "acme/open", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if res := shareCall(r, "GET", APIPrefix+paths[0], nil); res.StatusCode() != 403 {
		t.Fatalf("private turned public: %d", res.StatusCode())
	}

	// An unreadable record file refuses every share, made or opened.
	r2, root2 := shareRouter(t)
	res = shareCall(r2, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1"})
	_ = json.Unmarshal(res.Body(), &sv)
	if err := os.MkdirAll(filepath.Join(root2, "issuesync"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root2, filepath.FromSlash(issuesync.PrivateSourcesFile)), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/shared/" + sv.Token, "/shared/" + sv.Token + "/export?format=md", "/shared/" + sv.Token + "/badge.svg"} {
		if res := shareCall(r2, "GET", APIPrefix+p, nil); res.StatusCode() != 403 || !bytes.Contains(res.Body(), []byte(want)) {
			t.Fatalf("unreadable %s: %d %s", p, res.StatusCode(), res.Body())
		}
	}
	if res := shareCall(r2, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1"}); res.StatusCode() != 403 {
		t.Fatalf("unreadable create: %d %s", res.StatusCode(), res.Body())
	}
}
