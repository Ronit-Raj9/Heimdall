package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/connectlocal"
)

// After login stores a token, eval, push and pull and whoami use it for
// that server when no -token or token variable is given (P5-1, P5-2).
func TestStoredLoginUsedByServerCommands(t *testing.T) {
	var seen []string
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") != "Bearer hm_stored" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"orgs":[{"id":"org_1","slug":"acme","role":"owner"}]}`))
	}))
	defer hs.Close()
	t.Setenv("HEIMDALL_CONFIG_DIR", t.TempDir())
	t.Setenv("HEIMDALL_TOKEN", "")
	t.Setenv("BENCH_WRITE_TOKEN", "")
	t.Setenv("HEIMDALL_URL", "")
	t.Setenv("HEIMDALL_SERVER", "")
	c, err := connectlocal.LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	c.Servers[connectlocal.NormalizeServer(hs.URL)] = &connectlocal.ServerCred{Token: "hm_stored", Org: "acme", Role: "owner", Scopes: []string{"read"}, CreatedAt: time.Now()}
	c.Default = connectlocal.NormalizeServer(hs.URL)
	if _, err := c.Save(); err != nil {
		t.Fatal(err)
	}

	for _, sub := range []string{"list", "run"} {
		o, err := parseEvalFlags(sub, []string{"-server", hs.URL})
		if err != nil {
			t.Fatalf("%s: %v", sub, err)
		}
		if o.conn.token != "hm_stored" {
			t.Fatalf("eval %s token %q", sub, o.conn.token)
		}
	}
	if o, code := syncFlags("pull", []string{"-server", hs.URL}); code != -1 || o.token != "hm_stored" {
		t.Fatalf("pull: %d %+v", code, o)
	}
	// Another server gets no stored token; an explicit variable wins.
	if o, _ := parseEvalFlags("list", []string{"-server", "http://other.example:1"}); o.conn.token != "" {
		t.Fatalf("other server got %q", o.conn.token)
	}
	t.Setenv("HEIMDALL_TOKEN", "hm_env")
	if o, _ := parseEvalFlags("list", []string{"-server", hs.URL}); o.conn.token != "hm_env" {
		t.Fatalf("env token lost: %q", o.conn.token)
	}
	t.Setenv("HEIMDALL_TOKEN", "")

	var out bytes.Buffer
	if code := whoami(context.Background(), "", &out); code != exitOK {
		t.Fatalf("whoami %d %s", code, out.String())
	}
	if s := out.String(); !strings.Contains(s, "acme") || !strings.Contains(s, "owner") || !strings.Contains(s, "stored login") || !strings.Contains(s, "Token works") || strings.Contains(s, "hm_stored") {
		t.Fatalf("whoami output:\n%s", s)
	}
	if seen[len(seen)-1] != "Bearer hm_stored" {
		t.Fatalf("auth %v", seen)
	}
}
