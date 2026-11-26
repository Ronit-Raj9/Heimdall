package utils

import (
	"reflect"
	"testing"
)

// SEC-C: an org's approved image tagged with a plain bench/ name must land in
// the org's namespace, never under the global tag another org's run uses.
func TestOrgTagOfApprovedImageIsNamespaced(t *testing.T) {
	evil := "docker.io/evil/x@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	p := &DockerPolicy{OrgID: "o1", TagPrefix: OrgImagePrefix("o1"), Approved: []string{evil}}
	for _, args := range [][]string{
		{"tag", evil, "bench/task-a:1"},
		{"image", "tag", evil, "bench/task-a:1"},
	} {
		got, err := p.Rewrite(args)
		if err != nil {
			t.Fatalf("Rewrite(%v): %v", args, err)
		}
		want := append(append([]string(nil), args[:len(args)-1]...), "org-o1/bench/task-a:1")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Rewrite(%v) = %v, want %v", args, got, want)
		}
	}
	if ns, ok := p.Namespaced("bench/task-a:1"); !ok || ns != "org-o1/bench/task-a:1" {
		t.Fatalf("Namespaced = %q, %v; later inspect and run must see the org tag", ns, ok)
	}
	got, err := p.Rewrite([]string{"image", "inspect", "bench/task-a:1"})
	if err != nil || got[2] != "org-o1/bench/task-a:1" {
		t.Fatalf("inspect after tag = %v, %v", got, err)
	}
}

// TestPlatformSourceTagStaysPlain (rewritten for SEC-C C2 by the group A
// integrator): a platform source keeps the plain tag only for its own
// platform name, the pin the platform's own lock gives that name.
func TestPlatformSourceTagStaysPlain(t *testing.T) {
	pin := "registry.example/library/task-a@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	p := &DockerPolicy{OrgID: "o1", TagPrefix: OrgImagePrefix("o1"), Approved: []string{pin}, Platform: []string{"registry.example/library/"},
		PlatformPin: func(ref string) (string, bool) {
			if ref == "bench/task-a:1" {
				return pin, true
			}
			return "", false
		}}
	got, err := p.Rewrite([]string{"tag", pin, "bench/task-a:1"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tag", pin, "bench/task-a:1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Rewrite = %v, want %v", got, want)
	}
}

func TestOrgTagAlreadyNamespacedIsKept(t *testing.T) {
	evil := "docker.io/evil/x@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	p := &DockerPolicy{OrgID: "o1", TagPrefix: OrgImagePrefix("o1"), Approved: []string{evil}}
	got, err := p.Rewrite([]string{"tag", evil, "org-o1/bench/task-a:1"})
	if err != nil {
		t.Fatal(err)
	}
	if got[2] != "org-o1/bench/task-a:1" {
		t.Fatalf("Rewrite = %v", got)
	}
}
