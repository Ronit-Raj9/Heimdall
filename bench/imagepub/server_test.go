package imagepub

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

// testServer returns a Server whose crane is a script that records each call
// in marker and prints a pushed digest.
func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "crane-called")
	crane := filepath.Join(dir, "crane")
	script := "#!/bin/sh\necho \"$@\" >> " + marker + "\ncase \"$1\" in\n  push) echo 'registry-writer:5000/bench/a:1@sha256:" + hex64 + "' ;;\nesac\n"
	if err := os.WriteFile(crane, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	auth := filepath.Join(dir, "push.auth")
	writeFile(t, auth, "sync:secret\n")
	s := &Server{
		Writer:   "registry-writer:5000",
		AuthFile: auth,
		Manifest: filepath.Join(dir, "manifest"),
		Token:    "tok",
		Crane:    crane,
		Set:      func() (map[string]string, error) { return map[string]string{"bench/a:1": "agent a"}, nil },
		TempDir:  dir,
	}
	return s, marker
}

func publish(t *testing.T, s *Server, ref, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/publish?ref="+url.QueryEscape(ref), strings.NewReader("TARDATA"))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestPublishNeedsToken(t *testing.T) {
	s, marker := testServer(t)
	for _, tok := range []string{"", "wrong"} {
		if rec := publish(t, s, "bench/a:1", tok); rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: code %d", tok, rec.Code)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("crane ran without a token")
	}
}

func TestPublishRefusesNonPlatform(t *testing.T) {
	s, marker := testServer(t)
	for _, ref := range []string{"bench/b:1", "org-o1/bench/a:1", "registry:5000/bench/a:1"} {
		rec := publish(t, s, ref, "tok")
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"not a platform image"`) {
			t.Errorf("%s: code %d body %s", ref, rec.Code, rec.Body.String())
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("crane ran for a refused ref")
	}
}

func TestPublishPinsDigest(t *testing.T) {
	s, marker := testServer(t)
	rec := publish(t, s, "bench/a:1", "tok")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "sha256:"+hex64) {
		t.Fatalf("code %d body %s", rec.Code, rec.Body.String())
	}
	got, err := utils.ReadRegistryManifest(s.Manifest)
	if err != nil || got["bench/a:1"] != "sha256:"+hex64 {
		t.Fatalf("manifest = %v, %v", got, err)
	}
	calls, _ := os.ReadFile(marker)
	if strings.Contains(string(calls), "secret") {
		t.Fatalf("password on a command line: %s", calls)
	}
	if !strings.Contains(string(calls), "push --insecure") {
		t.Fatalf("calls = %s", calls)
	}
	// A second publish of the same tar size soon after reuses the digest.
	before := strings.Count(string(calls), "push")
	if rec := publish(t, s, "bench/a:1", "tok"); rec.Code != http.StatusOK {
		t.Fatalf("second: %d", rec.Code)
	}
	calls, _ = os.ReadFile(marker)
	if strings.Count(string(calls), "push") != before {
		t.Fatal("second publish pushed again")
	}
}
