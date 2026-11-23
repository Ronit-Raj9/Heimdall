package main

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/sandbox"
)

type okCheckPlatform struct{ sandbox.Platform }

func (okCheckPlatform) Check(context.Context) error { return nil }

// Fix wave 3 (N216): a platform can pull Heimdall's images once its card has
// a saved image registry, and not before.
func TestPlatformImagesReadyAfterSave(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); ok && u == "bot" && p == "tok-12345678" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	client := srv.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test server
	conns := &sandbox.Connections{Keys: keystore.NewMemory(), RegistryClient: client,
		Build: func(string, sandbox.Credentials) (sandbox.Platform, error) { return okCheckPlatform{}, nil }}
	old := platformRegistryConns
	platformRegistryConns = func(*hostedEnv) *sandbox.Connections { return conns }
	defer func() { platformRegistryConns = old }()
	ctx := context.Background()
	env := &hostedEnv{}
	if _, err := conns.Connect(ctx, "org1", "modal", "ak-1234567:as-12345678", "", "u1"); err != nil {
		t.Fatal(err)
	}
	if platformImagesReady(ctx, env, "org1", "modal") {
		t.Fatal("ready without a registry")
	}
	if _, err := conns.SaveRegistry(ctx, "org1", "modal", strings.TrimPrefix(srv.URL, "https://")+"/team/heimdall", "bot", "tok-12345678", "u1"); err != nil {
		t.Fatal(err)
	}
	if !platformImagesReady(ctx, env, "org1", "modal") {
		t.Fatal("not ready after the registry was saved")
	}
	if platformImagesReady(ctx, env, "org2", "modal") {
		t.Fatal("another org's registry counted")
	}
	platformRegistryConns = func(*hostedEnv) *sandbox.Connections { return nil }
	if platformImagesReady(ctx, env, "org1", "modal") {
		t.Fatal("ready without a keystore")
	}
}
