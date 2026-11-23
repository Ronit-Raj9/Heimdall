package main

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/sharedpool"
	"github.com/maximhq/bifrost/bench/utils"
)

type recordDocker struct{ calls []string }

func (r *recordDocker) MustRunEnv(_ context.Context, _ []string, args ...string) (*utils.DockerResult, error) {
	r.calls = append(r.calls, strings.Join(args, " "))
	switch args[0] {
	case "image":
		if args[1] == "inspect" {
			return &utils.DockerResult{Stdout: []byte("sha256:abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd")}, nil
		}
	case "push":
		return &utils.DockerResult{Stdout: []byte("x: digest: sha256:1111111111111111111111111111111111111111111111111111111111111111 size: 1")}, nil
	}
	return &utils.DockerResult{}, nil
}

// Fix wave 3 (N216 fix 2): a platform with an image registry gets images
// pushed there (RemoteImage) and Modal pulls them with the login; without
// one, nothing is set and the driver refuses with its reason.
func TestPlatformImagesUseOrgRegistry(t *testing.T) {
	m, err := sandbox.NewModal(sandbox.Credentials{Token: "ak-1:as-2"}, "python3")
	if err != nil {
		t.Fatal(err)
	}
	old := registryHostCheck
	registryHostCheck = func(context.Context, string) error { return nil } // no DNS in tests
	t.Cleanup(func() { registryHostCheck = old })
	d := &recordDocker{}
	pa := &sharedpool.PlatformAssignment{Name: "modal", Token: "ak-1:as-2",
		Registry: &sharedpool.RegistryLogin{Registry: "ghcr.io/acme/heimdall", Username: "bot", Token: "ghp_SECRET"}}
	remote := platformRemoteImage(pa, m, d)
	if remote == nil {
		t.Fatal("no RemoteImage with a registry")
	}
	ref, err := remote(context.Background(), "bench/coding-core-base:1")
	if err != nil || ref != "ghcr.io/acme/heimdall/bench-coding-core-base@sha256:1111111111111111111111111111111111111111111111111111111111111111" {
		t.Fatalf("remote = %q %v", ref, err)
	}
	if platformRemoteImage(&sharedpool.PlatformAssignment{Name: "modal"}, m, d) != nil {
		t.Fatal("RemoteImage set without a registry")
	}
}
