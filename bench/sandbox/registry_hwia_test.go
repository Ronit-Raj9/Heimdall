package sandbox

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/bench/keystore"
)

// Group A security review (fix wave 3, N216): the registry sign-in made when
// an org saves its login runs from the server with an organization-supplied
// host, so without a client of its own it must reach public addresses only
// (wave 2 rule), and its errors never carry the token.
func TestRegistrySignInDefaultClientRefusesInternalAddress(t *testing.T) {
	srv := fakeV2(t, false)
	var hits atomic.Int32
	inner := srv.Config.Handler
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		inner.ServeHTTP(w, r)
	})
	host := strings.TrimPrefix(srv.URL, "https://")
	login := ImageRegistry{Registry: host + "/team/heimdall", Username: "acme-bot", Token: "good-token"}
	err := CheckRegistrySignIn(context.Background(), nil, login)
	var se *RegistrySignInError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "could not be reached") {
		t.Fatalf("sign-in to a loopback registry with the default client = %v, want refused", err)
	}
	if strings.Contains(err.Error(), "good-token") {
		t.Fatalf("sign-in error carries the token: %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the loopback registry was reached %d times", n)
	}

	// The connections store uses the same guarded client when none is set.
	c := &Connections{Keys: keystore.NewMemory(), Build: func(string, Credentials) (Platform, error) { return &checkPlatform{}, nil }}
	ctx := context.Background()
	if _, err := c.Connect(ctx, "org1", PlatformModal, "ak-1234567:as-12345678", "", "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SaveRegistry(ctx, "org1", PlatformModal, login.Registry, login.Username, login.Token, "u1"); !errors.As(err, &se) {
		t.Fatalf("save with a loopback registry = %v, want a sign-in refusal", err)
	}
	if r, _ := c.RegistryOf(ctx, "org1", PlatformModal); r != nil {
		t.Fatal("a login that could not be checked was saved")
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the loopback registry was reached %d times", n)
	}
}

// Disconnecting the platform removes its registry login from the keystore.
func TestDisconnectRemovesRegistryLogin(t *testing.T) {
	srv := fakeV2(t, false)
	host := strings.TrimPrefix(srv.URL, "https://")
	client := srv.Client()
	client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // test server
	keys := keystore.NewMemory()
	c := &Connections{Keys: keys, RegistryClient: client, Build: func(string, Credentials) (Platform, error) { return &checkPlatform{}, nil }}
	ctx := context.Background()
	if _, err := c.Connect(ctx, "org1", PlatformModal, "ak-1234567:as-12345678", "", "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SaveRegistry(ctx, "org1", PlatformModal, host+"/team/heimdall", "acme-bot", "good-token", "u1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Disconnect(ctx, "org1", PlatformModal, "u1"); err != nil {
		t.Fatal(err)
	}
	if r, err := c.RegistryOf(ctx, "org1", PlatformModal); err != nil || r != nil {
		t.Fatalf("registry after disconnect = %+v, %v", r, err)
	}
	stored, _ := keys.List(ctx, "org1", keystore.KindSecret)
	for _, k := range stored {
		if strings.HasPrefix(k.Provider, providerRegistry) {
			t.Fatalf("registry login kept after disconnect: %+v", k)
		}
	}
}
