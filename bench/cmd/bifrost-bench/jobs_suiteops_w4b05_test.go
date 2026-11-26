package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/keystore"
)

// p11-2: a refused stored token must not block a public repository. The
// clone is tried once more without the token.
func TestCheckoutRetriesWithoutRejectedToken(t *testing.T) {
	ks := keystore.NewMemory()
	if _, err := ks.Create(context.Background(), "org1", keystore.KindSecret, keystore.Put{Provider: suiteOpsGitHubProvider, Label: "gh", Value: "tok-w4b05"}, "u"); err != nil {
		t.Fatal(err)
	}
	env := jobEnv{OrgID: "org1", Hosted: true, Services: handlers.Services{Keys: ks}}
	var calls [][]string
	old := suiteOpsGitClone
	suiteOpsGitClone = func(_ context.Context, args, env []string) error {
		calls = append(calls, env)
		if slices.ContainsFunc(env, func(e string) bool { return strings.HasPrefix(e, "GIT_CONFIG_VALUE_0=") }) {
			return errors.New("fatal: could not read Username")
		}
		return nil
	}
	t.Cleanup(func() { suiteOpsGitClone = old })
	log := &testJobLog{}
	dir, cleanup, err := suiteOpsRepo{GitHub: "benjaminp/six"}.checkout(context.Background(), env, 1, log)
	if err != nil {
		t.Fatalf("checkout failed: %v\n%s", err, log.String())
	}
	cleanup()
	if dir == "" || len(calls) != 2 {
		t.Fatalf("clone calls %d, want 2 (with the token, then without)", len(calls))
	}
	if !strings.Contains(log.String(), "the connected GitHub token was refused; trying without it (public repositories only)") {
		t.Fatalf("log misses the retry line:\n%s", log.String())
	}
	if strings.Contains(log.String(), "tok-w4b05") {
		t.Fatal("the token is in the log")
	}
}

func TestCheckoutBothClonesFailText(t *testing.T) {
	ks := keystore.NewMemory()
	if _, err := ks.Create(context.Background(), "org1", keystore.KindSecret, keystore.Put{Provider: suiteOpsGitHubProvider, Label: "gh", Value: "tok-w4b05"}, "u"); err != nil {
		t.Fatal(err)
	}
	env := jobEnv{OrgID: "org1", Hosted: true, Services: handlers.Services{Keys: ks}}
	old := suiteOpsGitClone
	suiteOpsGitClone = func(context.Context, []string, []string) error { return errors.New("fatal: not found") }
	t.Cleanup(func() { suiteOpsGitClone = old })
	_, _, err := suiteOpsRepo{GitHub: "acme/private"}.checkout(context.Background(), env, 1, &testJobLog{})
	want := "Could not copy github.com/acme/private: the connected GitHub account was refused and the repository is not public. Reconnect GitHub in Settings > GitHub."
	if err == nil || err.Error() != want {
		t.Fatalf("error %v, want %q", err, want)
	}
}
