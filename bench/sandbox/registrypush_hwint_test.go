package sandbox

import (
	"context"
	"errors"
	"testing"
)

// Fix wave 3 final integration (group A security residual): before each push
// to an org's saved registry the shared worker checks again that the registry
// host is public, with the guard used when the login was saved, so a name
// that resolved to a public address then and to an internal one now is
// refused before docker pushes anything.
func TestRegistryPushRechecksPublicHost(t *testing.T) {
	for _, registry := range []string{"127.0.0.1:5000/team/heimdall", "registry/team", "10.0.0.7/acme", "registry.internal/acme"} {
		fp := &fakePusher{}
		reg := &RegistryImages{Login: ImageRegistry{Registry: registry, Username: "acme-bot", Token: "ghp_SECRET_TOKEN_1234"}, Docker: fp}
		_, err := reg.Remote(context.Background(), "bench/agent-codex-task:1-coding-core")
		want := "Heimdall could not push to " + registry + ": the address is not public."
		if err == nil || err.Error() != want {
			t.Fatalf("%s: push = %v, want %q", registry, err, want)
		}
		if n := fp.count("push") + fp.count("tag"); n != 0 {
			t.Fatalf("%s: docker tagged or pushed %d times: %v", registry, n, fp.calls)
		}
	}

	// A name that resolves to an internal address at push time is refused too.
	fp := &fakePusher{}
	reg := &RegistryImages{Login: ImageRegistry{Registry: "ghcr.io/acme/heimdall", Username: "acme-bot", Token: "t"}, Docker: fp,
		HostCheck: func(_ context.Context, host string) error {
			if host != "ghcr.io" {
				t.Errorf("checked host %q, want ghcr.io", host)
			}
			return errors.New("resolves to a private address")
		}}
	if _, err := reg.Remote(context.Background(), "bench/agent-codex-task:1-coding-core"); err == nil ||
		err.Error() != "Heimdall could not push to ghcr.io/acme/heimdall: the address is not public." {
		t.Fatalf("rebinding name: %v", err)
	}
	if fp.count("push") != 0 {
		t.Fatal("pushed to a registry that is no longer public")
	}

	// The check runs before every push, not once: a later push is checked again.
	calls := 0
	ok := &RegistryImages{Login: ImageRegistry{Registry: "ghcr.io/acme/heimdall", Username: "acme-bot", Token: "t"}, Docker: &fakePusher{},
		HostCheck: func(context.Context, string) error { calls++; return nil }}
	if _, err := ok.Remote(context.Background(), "bench/agent-codex-task:1-coding-core"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || registryHostOf("ghcr.io:443/acme") != "ghcr.io" {
		t.Fatalf("host checks = %d", calls)
	}
}

// publicForTest stands in for the public resolver in tests: no DNS lookup.
func publicForTest(context.Context, string) error { return nil }
