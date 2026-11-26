package deploy

import (
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Wave 4 image publisher: the registry's write side and the manifest stay
// with the publisher and the sync tool. The shared worker (and through it
// every build session and org container) reads the manifest, the pull
// credentials and the publisher token read-only and never joins the
// writer's network; only the publisher and the sync tool hold the write
// identity; the publisher has no Docker socket.
func TestRegistryWritersStayNarrow(t *testing.T) {
	b, err := os.ReadFile("hosted/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Services map[string]struct {
			Networks any      `yaml:"networks"`
			Volumes  []string `yaml:"volumes"`
		} `yaml:"services"`
		Networks map[string]struct {
			Internal bool `yaml:"internal"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	names := func(v any) []string {
		var out []string
		switch n := v.(type) {
		case []any:
			for _, x := range n {
				out = append(out, x.(string))
			}
		case map[string]any:
			for k := range n {
				out = append(out, k)
			}
		}
		return out
	}
	members := func(net string) []string {
		var out []string
		for name, s := range c.Services {
			if slices.Contains(names(s.Networks), net) {
				out = append(out, name)
			}
		}
		sort.Strings(out)
		return out
	}
	for net, want := range map[string][]string{
		"registrysync": {"image-publisher", "registry-sync", "registry-writer"},
		"imagepublish": {"image-publisher", "shared-runner"},
	} {
		if !c.Networks[net].Internal {
			t.Errorf("network %s is not internal", net)
		}
		if got := members(net); !slices.Equal(got, want) {
			t.Errorf("network %s joined by %v, want %v", net, got, want)
		}
	}

	const authDir = "${HEIMDALL_REGISTRY_AUTH_DIR:-/var/lib/heimdall-registry-auth}/"
	writable := map[string][]string{} // auth subfolder -> services that mount it writable
	readers := map[string][]string{}  // auth subfolder -> every service that mounts it
	for name, s := range c.Services {
		for _, v := range s.Volumes {
			if strings.Contains(v, "docker.sock") && name == "image-publisher" {
				t.Errorf("image-publisher mounts the Docker socket: %s", v)
			}
			if !strings.HasPrefix(v, authDir) {
				continue
			}
			sub, _, _ := strings.Cut(strings.TrimPrefix(v, authDir), ":")
			readers[sub] = append(readers[sub], name)
			if !strings.HasSuffix(v, ":ro") {
				writable[sub] = append(writable[sub], name)
			}
		}
	}
	for sub, who := range writable {
		if sub != "runner" || !slices.Equal(who, []string{"image-publisher"}) {
			t.Errorf("auth folder %s mounted writable by %v; only image-publisher may write runner/", sub, who)
		}
	}
	got := readers["sync"]
	sort.Strings(got)
	if !slices.Equal(got, []string{"image-publisher", "registry-sync"}) {
		t.Errorf("the write identity (sync/) is mounted by %v, want only image-publisher and registry-sync", got)
	}
	if !slices.Contains(readers["runner"], "shared-runner") {
		t.Errorf("shared-runner does not read runner/ (%v)", readers["runner"])
	}
}
