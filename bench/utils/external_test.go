package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestExternalAPIsOrder(t *testing.T) {
	s := &schemas.Suite{Root: "/suite", External: []schemas.ExternalAPI{{ID: "a"}}}
	tk := &schemas.Task{Dir: "/suite/tasks/t", External: []schemas.ExternalAPI{{ID: "b"}}}
	refs := ExternalAPIs(s, tk)
	if len(refs) != 2 || refs[0].API.ID != "a" || refs[0].Base != "/suite" || refs[1].API.ID != "b" || refs[1].Base != "/suite/tasks/t" {
		t.Fatalf("refs = %+v", refs)
	}
	if got := ExternalAPIs(nil, nil); len(got) != 0 {
		t.Fatalf("nil suite and task: %+v", got)
	}
}

func TestCheckExternalAPIs(t *testing.T) {
	good := schemas.ExternalAPI{ID: "acme-rates", BaseURL: "https://api.acme.test/v1",
		Allow:   []schemas.ExternalAllow{{Methods: []string{"GET"}, PathPrefix: "/rates"}},
		Headers: map[string]string{"Accept": "application/json"}, AuthEnv: "ACME_KEY", AuthHeader: "X-Api-Key", Mode: "replay"}
	if err := CheckExternalAPIs([]ExternalRef{{API: good}}); err != nil {
		t.Fatalf("good api refused: %v", err)
	}
	cases := []struct {
		name string
		edit func(a *schemas.ExternalAPI)
		want string
	}{
		{"id upper case", func(a *schemas.ExternalAPI) { a.ID = "Acme" }, "host-name safe"},
		{"id with dot", func(a *schemas.ExternalAPI) { a.ID = "a.b" }, "host-name safe"},
		{"ftp url", func(a *schemas.ExternalAPI) { a.BaseURL = "ftp://x/" }, "http(s)"},
		{"user info", func(a *schemas.ExternalAPI) { a.BaseURL = "https://u:p@x/" }, "user info"},
		{"query", func(a *schemas.ExternalAPI) { a.BaseURL = "https://x/v1?k=1" }, "query"},
		{"relative prefix", func(a *schemas.ExternalAPI) { a.Allow[0].PathPrefix = "rates" }, "path_prefix"},
		{"dotdot prefix", func(a *schemas.ExternalAPI) { a.Allow[0].PathPrefix = "/a/../b" }, "path_prefix"},
		{"lower method", func(a *schemas.ExternalAPI) { a.Allow[0].Methods = []string{"get"} }, "method"},
		{"host header", func(a *schemas.ExternalAPI) { a.Headers = map[string]string{"Host": "evil"} }, "reserved"},
		{"bad env", func(a *schemas.ExternalAPI) { a.AuthEnv = "A-B" }, "auth_env"},
		{"bad auth header", func(a *schemas.ExternalAPI) { a.AuthHeader = "Proxy-Authorization" }, "auth_header"},
		{"bad mode", func(a *schemas.ExternalAPI) { a.Mode = "cache" }, "mode"},
		{"negative cap", func(a *schemas.ExternalAPI) { a.MaxCalls = -1 }, "negative"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := good
			a.Allow = append([]schemas.ExternalAllow(nil), good.Allow...)
			c.edit(&a)
			err := CheckExternalAPIs([]ExternalRef{{API: a}})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
	t.Run("duplicate id", func(t *testing.T) {
		err := CheckExternalAPIs([]ExternalRef{{API: good}, {API: good}})
		if err == nil || !strings.Contains(err.Error(), "used twice") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestExternalCassette(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "c.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, ok, err := ExternalCassette(ExternalRef{API: schemas.ExternalAPI{ID: "a", Cassette: "c.jsonl"}, Base: dir})
	if err != nil || !ok || p != filepath.Join(dir, "c.jsonl") {
		t.Fatalf("existing: %q %v %v", p, ok, err)
	}
	if _, ok, err := ExternalCassette(ExternalRef{API: schemas.ExternalAPI{ID: "a", Cassette: "missing.jsonl"}, Base: dir}); err != nil || ok {
		t.Fatalf("missing: %v %v", ok, err)
	}
	if p, ok, err := ExternalCassette(ExternalRef{API: schemas.ExternalAPI{ID: "a"}, Base: dir}); err != nil || ok || p != "" {
		t.Fatalf("none: %q %v %v", p, ok, err)
	}
	if _, _, err := ExternalCassette(ExternalRef{API: schemas.ExternalAPI{ID: "a", Cassette: "../escape.jsonl"}, Base: dir}); err == nil {
		t.Fatal("cassette outside its folder accepted")
	}
	if err := os.Mkdir(filepath.Join(dir, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ExternalCassette(ExternalRef{API: schemas.ExternalAPI{ID: "a", Cassette: "d"}, Base: dir}); err == nil {
		t.Fatal("folder accepted as a cassette")
	}
}

func TestExternalMode(t *testing.T) {
	cases := []struct {
		name, mode, exp string
		have            bool
		want, err       string
	}{
		{"default with cassette", "", "", true, "replay", ""},
		{"default without cassette", "", "", false, "", "no cassette"},
		{"replay without cassette", "replay", "", false, "", "missing"},
		{"live needs opt in", "live", "", true, "", "opt in"},
		{"record needs opt in", "record", "", false, "", "opt in"},
		{"experiment live", "", "live", false, "live", ""},
		{"experiment record", "replay", "record", false, "record", ""},
		{"experiment replay wins over live", "live", "replay", true, "replay", ""},
		{"experiment replay without cassette", "live", "replay", false, "", "missing"},
		{"bad experiment mode", "", "cache", true, "", "not live, record or replay"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ExternalMode(schemas.ExternalAPI{ID: "a", Mode: c.mode}, c.exp, c.have)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %q %v, want %q", got, err, c.want)
			}
		})
	}
}

func TestRunExternalMode(t *testing.T) {
	dir := t.TempDir()
	ref := ExternalRef{API: schemas.ExternalAPI{ID: "a", Cassette: "c.jsonl"}, Base: dir}
	if _, err := RunExternalMode(&schemas.RunContext{}, ref); err == nil {
		t.Fatal("admission without a cassette accepted")
	}
	rc := &schemas.RunContext{Manifest: &schemas.Manifest{Experiment: schemas.Experiment{External: "record"}}}
	if got, err := RunExternalMode(rc, ref); err != nil || got != "record" {
		t.Fatalf("record: %q %v", got, err)
	}
}

func TestExternalUnrepeatable(t *testing.T) {
	for mode, want := range map[string]bool{"": false, "replay": false, "live": true, "record": true} {
		m := &schemas.Manifest{Experiment: schemas.Experiment{External: mode}}
		if got := ExternalUnrepeatable(m); got != want {
			t.Fatalf("%q: %v, want %v", mode, got, want)
		}
	}
	if ExternalUnrepeatable(nil) {
		t.Fatal("nil manifest unrepeatable")
	}
}

func TestExternalEnv(t *testing.T) {
	handles := []schemas.ServiceHandle{
		{Spec: schemas.ServiceSpec{ID: "fs", Role: schemas.ServiceRoleMCP}, URL: "http://toolproxy:9200/s/fs"},
		{Spec: schemas.ServiceSpec{ID: "acme", Role: schemas.ServiceRoleExternal}, URL: ExternalURL("acme")},
	}
	env := map[string]string{}
	if err := AddExternalEnv(env, handles); err != nil {
		t.Fatal(err)
	}
	if got := env[EnvExternalAPIs]; got != `{"acme":{"url":"http://toolproxy:9200/x/acme"}}` {
		t.Fatalf("env = %q", got)
	}
	empty := map[string]string{}
	if err := AddExternalEnv(empty, handles[:1]); err != nil || len(empty) != 0 {
		t.Fatalf("no external apis: %v %v", empty, err)
	}
	if h := ExternalAuthHeader(schemas.ExternalAPI{}); h != "Authorization" {
		t.Fatalf("default auth header %q", h)
	}
	if h := ExternalAuthHeader(schemas.ExternalAPI{AuthHeader: "x-api-key"}); h != "X-Api-Key" {
		t.Fatalf("auth header %q", h)
	}
}
