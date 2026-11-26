package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/traceinbox"
)

// inbox.source with remove deletes the named source and keeps the comments
// and the other sources of the file (wave 4, p13-2).
func TestInboxSourceJobRemove(t *testing.T) {
	env := hostedLiveEnv(t, keystore.NewMemory(), "org-a")
	writeInboxSources(t, env, "# my sources\nsources:\n  - name: webhook\n    kind: webhook\n    token_env: HEIMDALL_INBOX_TOKEN\n"+
		"  - name: ls\n    kind: langsmith\n    host: https://api.smith.langchain.com\n    api_key_env: LS_KEY\n    project: p1\n    filters:\n      errors: true\n")
	raw := []byte(`{"name":"ls","remove":true}`)
	if _, _, err := validateInboxSource(t.Context(), env, raw); err != nil {
		t.Fatal(err)
	}
	log := &lineLog{}
	if _, err := runInboxSource(t.Context(), env, &jobs.Job{Params: raw}, log, nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(env.ProjectRoot, traceinbox.Dir, traceinbox.SourcesFile)
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "name: ls") || !strings.Contains(string(data), "# my sources") || !strings.Contains(string(data), "name: webhook") {
		t.Fatalf("sources.yaml:\n%s", data)
	}
	if !slices.Contains(log.lines, "removed source ls") {
		t.Fatalf("log: %q", log.lines)
	}
	if _, err := traceinbox.LoadConfig(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateInboxSource(t.Context(), env, raw); err == nil {
		t.Fatal("removing a source that is not there must be refused")
	}
}
