package utils

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

const (
	iwiaPinA = "registry.example/library/task-a@sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	iwiaPinB = "registry.example/library/task-b@sha256:" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	iwiaB    = "bench/task-b:1"
)

// iwiaPolicy is an org policy on a shared host whose platform pins
// bench/task-a:1 to iwiaPinA and bench/task-b:1 to iwiaPinB.
func iwiaPolicy() *DockerPolicy {
	return &DockerPolicy{OrgID: "o1", TagPrefix: OrgImagePrefix("o1"), Platform: []string{"registry.example/library/"},
		PlatformPin: func(ref string) (string, bool) {
			switch ref {
			case "bench/task-a:1":
				return iwiaPinA, true
			case iwiaB:
				return iwiaPinB, true
			}
			return "", false
		}}
}

// SEC-C C2: `docker tag <platform image A> <plain platform name B>` under an
// org policy lands in the org's namespace, and later calls see that tag.
func TestTagOfOtherPlatformImageOntoPlatformNameIsNamespaced(t *testing.T) {
	p := iwiaPolicy()
	got, err := p.Rewrite([]string{"tag", iwiaPinA, iwiaB})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tag", iwiaPinA, "org-o1/" + iwiaB}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Rewrite = %v, want %v", got, want)
	}
	if ns, ok := p.Namespaced(iwiaB); !ok || ns != "org-o1/"+iwiaB {
		t.Fatalf("Namespaced = %q %v", ns, ok)
	}
	// A plain name that is no platform image, and a local build onto a
	// platform name, are namespaced too.
	for _, args := range [][]string{{"tag", "org-o1/mine:1", "python:3.12"}, {"tag", "bench/task-a:1", "bench/coding-core-base:1"}} {
		got, err := iwiaPolicy().Rewrite(args)
		if err != nil || got[2] != "org-o1/"+args[2] {
			t.Fatalf("Rewrite(%v) = %v %v", args, got, err)
		}
	}
	// Without the platform's pins nothing stays plain.
	q := iwiaPolicy()
	q.PlatformPin = nil
	if got, _ := q.Rewrite([]string{"tag", iwiaPinB, iwiaB}); got[2] != "org-o1/"+iwiaB {
		t.Fatalf("no pins: %v", got)
	}
}

// A tag into a registry outside the platform (the org's own registry, used
// to push an image to a cloud sandbox) keeps its name: it is no platform
// name and no other org reads it.
func TestTagIntoOrgRegistryKeepsName(t *testing.T) {
	got, err := iwiaPolicy().Rewrite([]string{"tag", iwiaB, "ghcr.io/acme/heimdall:task-b"})
	if err != nil || got[2] != "ghcr.io/acme/heimdall:task-b" {
		t.Fatalf("Rewrite = %v %v", got, err)
	}
	// A name under the platform's own registry prefix is a platform name.
	got, err = iwiaPolicy().Rewrite([]string{"tag", iwiaPinA, "registry.example/library/task-b:1"})
	if err != nil || got[2] != "org-o1/registry.example/library/task-b:1" {
		t.Fatalf("platform registry name: %v %v", got, err)
	}
}

// SEC-C C2 end to end: an org lock that pins platform image A's digest for
// the name of platform image B never writes the plain name B; the org's run
// uses its own tag.
func TestOrgPinOfOtherPlatformImageKeepsPlainName(t *testing.T) {
	d, log := fakeImageDocker(t)
	p := iwiaPolicy()
	d.Policy = p
	if _, err := d.EnsureImage(context.Background(), LocalImage{Tag: iwiaB, Pinned: iwiaPinA}, nil); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(log)
	calls := string(raw)
	if !strings.Contains(calls, "tag "+iwiaPinA+" org-o1/"+iwiaB) {
		t.Fatalf("calls:\n%s", calls)
	}
	for _, line := range strings.Split(calls, "\n") {
		if strings.HasPrefix(line, "tag ") && strings.HasSuffix(line, " "+iwiaB) {
			t.Fatalf("wrote the plain platform name: %s", line)
		}
	}
	got, err := p.Rewrite([]string{"run", "--rm", iwiaB})
	if err != nil || got[len(got)-1] != "org-o1/"+iwiaB {
		t.Fatalf("run uses %v %v", got, err)
	}
	// The platform's own pin for B still lands under the plain name.
	d2, log2 := fakeImageDocker(t)
	d2.Policy = iwiaPolicy()
	if _, err := d2.EnsureImage(context.Background(), LocalImage{Tag: iwiaB, Pinned: iwiaPinB}, nil); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(log2); !strings.Contains(string(raw), "tag "+iwiaPinB+" "+iwiaB+"\n") {
		t.Fatalf("platform pin calls:\n%s", raw)
	}
}

// An org build can never carry the platform build mark: a --label of it is
// dropped and the command line sets it empty, which wins over any LABEL in
// the Dockerfile.
func TestOrgBuildCannotSetPlatformMark(t *testing.T) {
	dir := t.TempDir()
	p := &DockerPolicy{OrgID: "o1", TagPrefix: OrgImagePrefix("o1"), Builder: "b"}
	got, err := p.Rewrite([]string{"build", "-t", "bench/x:1", "--label", LabelPlatformBuild + "=1", dir})
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(got, " ")
	if strings.Contains(line, LabelPlatformBuild+"=1") || !strings.HasSuffix(line, "--label "+LabelPlatformBuild+"= "+dir) {
		t.Fatalf("org build = %v", got)
	}
}
