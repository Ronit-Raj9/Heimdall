package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/livescore"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// hostedLiveEnv is a hosted job env of org with a project at a temp root.
func hostedLiveEnv(t *testing.T, keys keystore.Store, org string) jobEnv {
	t.Helper()
	root := t.TempDir()
	return jobEnv{App: &app{cfg: &lib.Config{Root: root}, logger: schemas.NoopLogger{}}, OrgID: org, ProjectRoot: root, Hosted: true,
		Services: handlers.Services{OrgID: org, Hosted: true, Keys: keys}}
}

func writeLiveConfig(t *testing.T, env jobEnv, url string) {
	t.Helper()
	text, err := livescore.Template(livescore.InitOptions{URL: url, TokenEnv: "BF_TOKEN", BaselineKey: "vk-a", CandidateKey: "vk-b"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(env.ProjectRoot, livescore.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, livescore.ConfigFile), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeInboxSources(t *testing.T, env jobEnv, yaml string) {
	t.Helper()
	dir := filepath.Join(env.ProjectRoot, traceinbox.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, traceinbox.SourcesFile), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
}

func addSecret(t *testing.T, keys keystore.Store, org, name, value string) {
	t.Helper()
	if _, err := keys.Create(context.Background(), org, keystore.KindSecret, keystore.Put{Provider: "secret", Label: name, Value: value}, "test"); err != nil {
		t.Fatal(err)
	}
}

func TestHostedLivePullRefusesPrivateURL(t *testing.T) {
	keys := keystore.NewMemory()
	addSecret(t, keys, "org-a", "BF_TOKEN", "tok-a")
	for _, u := range []string{"http://gw.example.com", "https://10.0.0.5:8080", "https://localhost:8080", "https://169.254.169.254"} {
		env := hostedLiveEnv(t, keys, "org-a")
		writeLiveConfig(t, env, u)
		_, _, err := validateLivePull(t.Context(), env, nil)
		if !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), "https") {
			t.Fatalf("%s: want a public https refusal, got %v", u, err)
		}
	}
}

func TestHostedLivePullTokenFromOrgSecret(t *testing.T) {
	keys := keystore.NewMemory()
	t.Setenv("BF_TOKEN", "server-env-must-not-count")
	envA := hostedLiveEnv(t, keys, "org-a")
	writeLiveConfig(t, envA, "https://gw.example.com")
	_, _, err := validateLivePull(t.Context(), envA, nil)
	if !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), "Settings > Secrets") || !strings.Contains(err.Error(), "BF_TOKEN") {
		t.Fatalf("missing secret: want the Settings > Secrets step, got %v", err)
	}
	addSecret(t, keys, "org-a", "BF_TOKEN", "tok-a")
	if _, _, err := validateLivePull(t.Context(), envA, nil); err != nil {
		t.Fatalf("org secret set: %v", err)
	}
	if got := handlers.OrgSecretGetenv(t.Context(), envA.Services)("BF_TOKEN"); got != "tok-a" {
		t.Fatalf("token from org secret = %q", got)
	}
	// Another org with the same project shape does not see org-a's secret.
	envB := hostedLiveEnv(t, keys, "org-b")
	writeLiveConfig(t, envB, "https://gw.example.com")
	if _, _, err := validateLivePull(t.Context(), envB, nil); !errors.Is(err, jobs.ErrInvalidParams) {
		t.Fatalf("org-b used org-a's secret: %v", err)
	}
	if got := handlers.OrgSecretGetenv(t.Context(), envB.Services)("BF_TOKEN"); got != "" {
		t.Fatalf("org-b read %q", got)
	}
	// The judge is refused without the org's own provider key.
	if _, _, err := validateLivePull(t.Context(), envA, []byte(`{"judge":true}`)); !errors.Is(err, jobs.ErrInvalidParams) {
		t.Fatalf("judge without a judge section: %v", err)
	}
}

func TestHostedInboxPull(t *testing.T) {
	keys := keystore.NewMemory()
	envA := hostedLiveEnv(t, keys, "org-a")
	writeInboxSources(t, envA, "sources:\n  - name: gw\n    kind: bifrost\n    url: https://192.168.1.10\n    token_env: BF_TOKEN\n    filters:\n      errors: true\n")
	addSecret(t, keys, "org-a", "BF_TOKEN", "tok-a")
	if _, _, err := validateInboxPull(t.Context(), envA, nil); !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), "https") {
		t.Fatalf("private source: %v", err)
	}
	ls := "sources:\n  - name: ls\n    kind: langsmith\n    host: https://api.smith.langchain.com\n    api_key_env: LS_KEY\n    project: p1\n    filters:\n      errors: true\n"
	writeInboxSources(t, envA, ls)
	if _, _, err := validateInboxPull(t.Context(), envA, nil); !errors.Is(err, jobs.ErrInvalidParams) || !strings.Contains(err.Error(), "LS_KEY") {
		t.Fatalf("missing LS_KEY: %v", err)
	}
	addSecret(t, keys, "org-a", "LS_KEY", "ls-a")
	if _, _, err := validateInboxPull(t.Context(), envA, nil); err != nil {
		t.Fatalf("org-a ready: %v", err)
	}
	envB := hostedLiveEnv(t, keys, "org-b")
	writeInboxSources(t, envB, ls)
	if _, _, err := validateInboxPull(t.Context(), envB, nil); !errors.Is(err, jobs.ErrInvalidParams) {
		t.Fatalf("org-b used org-a's secret: %v", err)
	}
	// otlp_dir reads a server folder: never pulled on the hosted server.
	writeInboxSources(t, envA, "sources:\n  - name: dir\n    kind: otlp_dir\n    path: /etc\n    filters:\n      errors: true\n")
	if _, _, err := validateInboxPull(t.Context(), envA, nil); !errors.Is(err, jobs.ErrInvalidParams) {
		t.Fatalf("otlp_dir hosted: %v", err)
	}
}

func TestHostedPullClientRefusesLoopbackAtConnect(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))
	defer srv.Close()
	_, err := handlers.HostedPullClient().Get(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "refused to connect") {
		t.Fatalf("want a connect-time refusal, got %v", err)
	}
}

func TestInboxSourceAppendsAndValidates(t *testing.T) {
	keys := keystore.NewMemory()
	env := hostedLiveEnv(t, keys, "org-a")
	writeInboxSources(t, env, "# my sources\nsources:\n  - name: webhook\n    kind: webhook\n    token_env: HEIMDALL_INBOX_TOKEN\n")
	if _, _, err := validateInboxSource(t.Context(), env, []byte(`{"name":"gw","kind":"bifrost","url":"https://10.1.2.3","token_env":"BF_TOKEN"}`)); !errors.Is(err, jobs.ErrInvalidParams) {
		t.Fatalf("private url accepted: %v", err)
	}
	raw := []byte(`{"name":"ls","kind":"langsmith","host":"https://api.smith.langchain.com","api_key_env":"LS_KEY","project":"p1"}`)
	if _, _, err := validateInboxSource(t.Context(), env, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := runInboxSource(t.Context(), env, &jobs.Job{Params: raw}, &lineLog{}, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(env.ProjectRoot, traceinbox.Dir, traceinbox.SourcesFile))
	if !strings.Contains(string(data), "# my sources") || !strings.Contains(string(data), "name: webhook") || !strings.Contains(string(data), "api_key_env: LS_KEY") {
		t.Fatalf("sources.yaml:\n%s", data)
	}
	if _, _, err := validateInboxSource(t.Context(), env, raw); !errors.Is(err, jobs.ErrInvalidParams) {
		t.Fatalf("duplicate name accepted: %v", err)
	}
}
