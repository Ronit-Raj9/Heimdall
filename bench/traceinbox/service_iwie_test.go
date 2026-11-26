package traceinbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A webhook source added to sources.yaml while the server runs turns the
// webhook on without a restart, and removing it turns it off again (group E
// integration, the follow-up of I18's p13-2 reload).
func TestWebhookGateFollowsSourcesFile(t *testing.T) {
	ib, _ := newTestInbox(t, &Config{}, map[string]string{"HOOK_TOKEN": "s3cret-token"})
	path := filepath.Join(t.TempDir(), SourcesFile)
	if err := os.WriteFile(path, []byte("sources: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ib.SourcesPath = path
	svc := NewService(ib)
	if svc.Gate() != nil {
		t.Fatal("no webhook source yet: the gate must be off")
	}
	if _, err := svc.Ingest(t.Context(), []byte(`{}`)); err != ErrOff {
		t.Fatalf("ingest before the source exists: got %v, want ErrOff", err)
	}

	touch := func(text string, at time.Time) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	later := time.Now().Add(time.Minute)
	touch("sources:\n  - name: hook\n    kind: webhook\n    token_env: HOOK_TOKEN\n", later)
	g := svc.Gate()
	if g == nil {
		t.Fatal("webhook source added while running: the gate must be on without a restart")
	}
	if !g.Check("Bearer s3cret-token") || g.Check("wrong") {
		t.Fatal("the rebuilt gate must check the token of the new source")
	}
	if svc.Gate() != g {
		t.Fatal("an unchanged webhook source must keep its gate (and its rate limiter)")
	}
	list, err := svc.List(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !list.IngestEnabled {
		t.Fatal("List must report ingest on once the webhook source exists")
	}

	touch("sources: []\n", later.Add(time.Minute))
	if svc.Gate() != nil {
		t.Fatal("webhook source removed while running: the gate must be off")
	}
}

// Group E security review (I18): the source list never serves a credential.
// Sources name their secrets by variable; List answers no variable value and
// no variable name, for a webhook and a pull source alike.
func TestInboxListServesNoSecret(t *testing.T) {
	cfg := &Config{Sources: []Source{
		{Name: "hook", Kind: KindWebhook, TokenEnv: "HOOK_TOKEN_IWIE"},
		{Name: "ls", Kind: KindLangSmith, Host: "https://api.smith.langchain.com", APIKeyEnv: "LS_KEY_IWIE", Project: "p1", Filters: Filters{Errors: true}},
	}}
	ib, _ := newTestInbox(t, cfg, map[string]string{"HOOK_TOKEN_IWIE": "hook-secret-value-1", "LS_KEY_IWIE": "ls-secret-value-2"})
	list, err := NewService(ib).List(t.Context(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"hook-secret-value-1", "ls-secret-value-2", "HOOK_TOKEN_IWIE", "LS_KEY_IWIE"} {
		if strings.Contains(string(b), s) {
			t.Errorf("GET /inbox serves %q: %s", s, b)
		}
	}
	if len(list.Sources) != 2 {
		t.Fatalf("sources %+v", list.Sources)
	}
}
