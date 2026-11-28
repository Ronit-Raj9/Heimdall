package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// unreadableKeys lists keys but cannot reveal any secret, as a store whose
// master key changed.
type unreadableKeys struct{ keystore.Store }

func (unreadableKeys) Reveal(context.Context, string, string) (string, error) {
	return "", errors.New("keystore: open: cipher: message authentication failed")
}

const statusTestToken = "ghp_secret_status_test_token"

func statusTestHandler(t *testing.T, unreadable bool) *GitHubHandler {
	t.Helper()
	mem := keystore.NewMemory()
	if _, err := mem.Create(context.Background(), "org_1", keystore.KindSecret, keystore.Put{Provider: auth.GitHubRepoProvider, Label: "octo", Value: statusTestToken}, "usr_1"); err != nil {
		t.Fatal(err)
	}
	keys := mem
	if unreadable {
		keys = unreadableKeys{mem}
	}
	conn := auth.NewGitHubRepoConnector(auth.Config{GitHub: &auth.GitHubConfig{ClientID: "id", ClientSecret: "secret"}})
	return NewGitHubHandler(testMode, conn, keys, "", nil)
}

func statusTestGet(t *testing.T, h *GitHubHandler, f func(*fasthttp.RequestCtx)) (*fasthttp.RequestCtx, map[string]any) {
	t.Helper()
	ctx := &fasthttp.RequestCtx{}
	tenancy.SetOrg(ctx, &tenancy.Org{ID: "org_1"})
	f(ctx)
	body := string(ctx.Response.Body())
	if strings.Contains(body, statusTestToken) || strings.Contains(body, "cipher") {
		t.Fatalf("answer carries the token or the key store error: %s", body)
	}
	var m map[string]any
	_ = json.Unmarshal(ctx.Response.Body(), &m)
	return ctx, m
}

// TestGitHubStatusNeedsReadableToken (round 2, Q-RG1): status says
// connected only when the saved token can be read, as the repository routes
// need it; a saved but unreadable token is "needs_reconnect" with the
// reason, and the repos route answers the same reason as a 409. The token
// is never answered.
func TestGitHubStatusNeedsReadableToken(t *testing.T) {
	h := statusTestHandler(t, false)
	ctx, st := statusTestGet(t, h, h.getStatus)
	if ctx.Response.StatusCode() != 200 || st["connected"] != true || st["state"] != GitHubStateConnected || st["login"] != "octo" {
		t.Fatalf("readable: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}

	h = statusTestHandler(t, true)
	ctx, st = statusTestGet(t, h, h.getStatus)
	if ctx.Response.StatusCode() != 200 || st["connected"] != false || st["state"] != GitHubStateNeedsReconnect || !strings.Contains(st["hint"].(string), "reconnect GitHub") {
		t.Fatalf("unreadable: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ctx, _ = statusTestGet(t, h, h.getRepos)
	if ctx.Response.StatusCode() != 409 || !strings.Contains(string(ctx.Response.Body()), "reconnect") {
		t.Fatalf("repos unreadable: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}

	// No saved token: not connected, no hint.
	empty := NewGitHubHandler(testMode, auth.NewGitHubRepoConnector(auth.Config{GitHub: &auth.GitHubConfig{ClientID: "id", ClientSecret: "secret"}}), keystore.NewMemory(), "", nil)
	ctx, st = statusTestGet(t, empty, empty.getStatus)
	if st["connected"] != false || st["state"] != GitHubStateNotConnected {
		t.Fatalf("none: %s", ctx.Response.Body())
	}
}
