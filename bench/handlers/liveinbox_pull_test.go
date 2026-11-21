package handlers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/livescore"
)

func TestLivePullCheckHosted(t *testing.T) {
	root := t.TempDir()
	cfg := &lib.Config{Root: root}
	keys := keystore.NewMemory()
	s := Services{OrgID: "org-a", Hosted: true, Keys: keys}
	c := LivePullCheck(context.Background(), s, cfg)
	if c.Ready || len(c.Steps) != 1 || c.Steps[0].Link != PullLinkLiveSetup {
		t.Fatalf("unconfigured: %+v", c)
	}
	text, _ := livescore.Template(livescore.InitOptions{URL: "https://gw.example.com", TokenEnv: "BF_TOKEN", BaselineKey: "a", CandidateKey: "b"})
	_ = os.MkdirAll(filepath.Join(root, livescore.Dir), 0o755)
	_ = os.WriteFile(filepath.Join(root, livescore.Dir, livescore.ConfigFile), []byte(text), 0o644)
	t.Setenv("BF_TOKEN", "server-env")
	c = LivePullCheck(context.Background(), s, cfg)
	if c.Ready || len(c.Steps) != 1 || c.Steps[0].Link != PullLinkSecrets || c.Steps[0].Secret != "BF_TOKEN" {
		t.Fatalf("missing secret: %+v", c)
	}
	if _, err := keys.Create(context.Background(), "org-a", keystore.KindSecret, keystore.Put{Provider: "secret", Label: "BF_TOKEN", Value: "x"}, "t"); err != nil {
		t.Fatal(err)
	}
	if c = LivePullCheck(context.Background(), s, cfg); !c.Ready || !c.Secrets[0].Set {
		t.Fatalf("ready: %+v", c)
	}
	if c = LivePullCheck(context.Background(), Services{OrgID: "org-b", Hosted: true, Keys: keys}, cfg); c.Ready {
		t.Fatal("org-b sees org-a's secret")
	}
}

func TestCheckPullURL(t *testing.T) {
	for _, u := range []string{"https://gw.example.com/v1", "https://api.smith.langchain.com"} {
		if err := CheckPullURL(u); err != nil {
			t.Fatalf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"http://gw.example.com", "https://127.0.0.1", "https://[::1]", "https://192.168.0.2", "https://u:p@gw.example.com", "https://metadata.internal", "https://localhost"} {
		if err := CheckPullURL(u); err == nil {
			t.Fatalf("%s accepted", u)
		}
	}
}
