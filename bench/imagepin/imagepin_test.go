package imagepin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const dgA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const dgB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func TestPinsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := PinsPath(dir)
	if p, err := ReadPins(path); err != nil || len(p) != 0 {
		t.Fatalf("missing file: %v %v", p, err)
	}
	want := Pins{"bench/x-base:1": "reg.example:5000/library/x/bench/x-base@" + dgA, "task-x-t1": "reg.example:5000/library/x/task-x-t1@" + dgB}
	if err := WritePins(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPins(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v %v", got, err)
	}
	if err := WritePins(path, Pins{"a": "not-a-pin"}); err == nil {
		t.Fatal("bad pin written")
	}
	os.WriteFile(path, []byte("bench/x:1 reg/x:1\n"), 0o644)
	if _, err := ReadPins(path); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("bad line accepted: %v", err)
	}
	if filepath.Base(path) != ".suite.image-pins" {
		t.Fatal("pins file must keep the .suite. prefix (excluded from the files fingerprint)")
	}
}

func TestRepoFor(t *testing.T) {
	cases := []struct{ ns, suite, tag, repo, ref string }{
		{"", "coding-core", "bench/coding-core-base:1", "library/coding-core/bench/coding-core-base", "1"},
		{"lib", "swe", "swebench/sweb.eval.x86_64.django_1776_django-11099:latest", "lib/swe/swebench/sweb.eval.x86_64.django_1776_django-11099", "latest"},
		{"", "s", "docker.io/library/python:3.12", "library/s/python", "3.12"},
		{"", "s", "localhost:5000/Foo/Bar", "library/s/localhost-5000/foo/bar", "latest"},
	}
	for _, c := range cases {
		repo, ref := RepoFor(c.ns, c.suite, c.tag)
		if repo != c.repo || ref != c.ref {
			t.Errorf("RepoFor(%q,%q,%q) = %q %q, want %q %q", c.ns, c.suite, c.tag, repo, ref, c.repo, c.ref)
		}
	}
}

func TestParseScopesAndGrant(t *testing.T) {
	req := ParseScopes([]string{"repository:library/open/img:pull,push", "repository:library/gaia/img:pull registry:catalog:*", "repository:other/x/y:pull", "repository:BAD:pull"})
	if len(req) != 3 {
		t.Fatalf("scopes %+v", req)
	}
	p := &AccessPolicy{Namespace: "library", Restricted: map[string][]string{"gaia": {"org-ok"}}}
	org := p.Grant(Identity{OrgID: "org-1"}, req)
	if len(org) != 1 || org[0].Name != "library/open/img" || !reflect.DeepEqual(org[0].Actions, []string{"pull"}) {
		t.Fatalf("org grant %+v", org)
	}
	ok := p.Grant(Identity{OrgID: "org-ok"}, req)
	if len(ok) != 2 || ok[1].Name != "library/gaia/img" {
		t.Fatalf("allowed org grant %+v", ok)
	}
	sync := p.Grant(Identity{Sync: true}, req)
	if len(sync) != 2 || !reflect.DeepEqual(sync[0].Actions, []string{"pull", "push"}) {
		t.Fatalf("sync grant %+v", sync)
	}
	if g := p.Grant(Identity{}, req); len(g) != 0 {
		t.Fatalf("anonymous grant %+v", g)
	}
	if g := p.Grant(Identity{OrgID: "o"}, ParseScopes([]string{"repository:library/open/img:delete,*"})); len(g) != 0 {
		t.Fatalf("delete granted %+v", g)
	}
}

func TestAccessPolicyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.yaml")
	os.WriteFile(path, []byte("namespace: lib\nrestricted:\n  gaia: [org-a]\n  locomo: []\n"), 0o644)
	p, err := LoadAccessPolicy(path)
	if err != nil || p.Namespace != "lib" || len(p.Restricted["gaia"]) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	if p.mayPull("org-a", "locomo") || !p.mayPull("org-a", "gaia") || !p.mayPull("org-b", "open") {
		t.Fatal("restriction not applied")
	}
	os.WriteFile(path, []byte("namspace: typo\n"), 0o644)
	if _, err := LoadAccessPolicy(path); err == nil {
		t.Fatal("unknown field accepted")
	}
	if p, err := LoadAccessPolicy(""); err != nil || p.Namespace != DefaultNamespace {
		t.Fatalf("default %+v %v", p, err)
	}
}

func testIssuer(t *testing.T) *Issuer {
	t.Helper()
	dir := t.TempDir()
	if err := GenerateTokenKey(dir, ""); err != nil {
		t.Fatal(err)
	}
	if err := GenerateTokenKey(dir, ""); err == nil {
		t.Fatal("existing key overwritten")
	}
	if fi, _ := os.Stat(filepath.Join(dir, TokenKeyFile)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", fi.Mode().Perm())
	}
	i, err := LoadIssuer(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestIssueVerifies(t *testing.T) {
	i := testIssuer(t)
	now := time.Unix(1_800_000_000, 0)
	i.Now = func() time.Time { return now }
	tok, ttl, _, err := i.Issue("org:o1", []Access{{Type: "repository", Name: "library/s/x", Actions: []string{"pull"}}})
	if err != nil || ttl != 300 {
		t.Fatal(err, ttl)
	}
	c, err := VerifyToken(tok, i.CertDER())
	if err != nil {
		t.Fatal(err)
	}
	if c.Issuer != DefaultIssuer || c.Audience != DefaultService || c.Subject != "org:o1" || c.ExpiresAt != now.Add(TokenTTL).Unix() || len(c.Access) != 1 {
		t.Fatalf("claims %+v", c)
	}
	other := testIssuer(t)
	if _, err := VerifyToken(tok, other.CertDER()); err == nil {
		t.Fatal("verified with another key")
	}
	parts := strings.Split(tok, ".")
	if _, err := VerifyToken(parts[0]+"."+parts[1]+"x."+parts[2], i.CertDER()); err == nil {
		t.Fatal("tampered claims verified")
	}
	var head map[string]any
	raw, _ := decodeSeg(parts[0])
	json.Unmarshal(raw, &head)
	if head["alg"] != "ES256" || head["x5c"] == nil {
		t.Fatalf("header %v", head)
	}
}

func TestParseChallenge(t *testing.T) {
	s, p := parseChallenge(`Bearer realm="https://h/api/v1/registry/token",service="heimdall-library",scope="repository:a/b:pull"`)
	if s != "bearer" || p["realm"] != "https://h/api/v1/registry/token" || p["service"] != "heimdall-library" || p["scope"] != "repository:a/b:pull" {
		t.Fatalf("%s %v", s, p)
	}
}

// A fake token-auth registry: the client must follow the challenge, fetch
// a token with basic auth and read the index, then the amd64 manifest.
func TestClientTokenFlowAndLayers(t *testing.T) {
	const idx = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	const m1 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	const m2 = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	var tokenCalls int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			u, p, ok := r.BasicAuth()
			if !ok || u != "sync" || p != "pw" || r.URL.Query().Get("scope") == "" {
				w.WriteHeader(401)
				return
			}
			tokenCalls++
			json.NewEncoder(w).Encode(map[string]string{"token": "T"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer T" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="svc"`)
			w.WriteHeader(401)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/manifests/"+idx), strings.HasSuffix(r.URL.Path, "/manifests/1"):
			w.Header().Set("Docker-Content-Digest", idx)
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
			w.Write([]byte(`{"manifests":[{"digest":"` + m2 + `","platform":{"os":"unknown","architecture":"unknown"},"annotations":{"vnd.docker.reference.type":"attestation-manifest"}},{"digest":"` + m1 + `","platform":{"os":"linux","architecture":"amd64"}}]}`))
		case strings.HasSuffix(r.URL.Path, "/manifests/"+m1):
			w.Write([]byte(`{"layers":[{"digest":"` + dgA + `","size":100},{"digest":"` + dgB + `","size":20}]}`))
		case strings.Contains(r.URL.Path, "/b/manifests/"):
			w.Write([]byte(`{"layers":[{"digest":"` + dgA + `","size":100},{"digest":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","size":5}]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	c := &Client{Host: host, PlainHTTP: true, User: "sync", Password: "pw"}
	d, err := c.Digest(context.Background(), "library/s/a", "1")
	if err != nil || d != idx {
		t.Fatal(d, err)
	}
	rep := Layers(context.Background(), c, []Want{
		{Tag: "a:1", Pinned: host + "/library/s/a@" + idx},
		{Tag: "b:1", Pinned: host + "/library/s/b@" + "sha256:4444444444444444444444444444444444444444444444444444444444444444"},
		{Tag: "elsewhere", Pinned: "other.example/library/s/c@" + m1},
	})
	if rep.TotalBytes != 225 || rep.UniqueBytes != 125 || rep.SharedBytes != 100 || len(rep.SharedLayers) != 1 || rep.SharedLayers[0].Images != 2 {
		t.Fatalf("report %+v", rep)
	}
	if rep.Images[2].Error == "" {
		t.Fatal("image on another registry not reported")
	}
	if tokenCalls != 2 {
		t.Fatalf("token fetched %d times, want once per repository", tokenCalls)
	}
	if _, err := (&Client{Host: host, PlainHTTP: true, User: "sync", Password: "wrong"}).Digest(context.Background(), "library/s/a", "1"); err == nil {
		t.Fatal("refused credentials accepted")
	}
}

func decodeSeg(s string) ([]byte, error) {
	return base64RawURL(s)
}

func base64RawURL(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
