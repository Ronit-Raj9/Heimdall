package imagepub

import "testing"

func TestValidRef(t *testing.T) {
	good := []string{"bench/agent-codex:1", "bench/task-hd-harbor-bfcl-t1:0123456789ab", "bench/coding-core-base:1"}
	bad := []string{
		"org-x/bench/a:1",
		"bench/a@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		"registry:5000/bench/a:1",
		"bench/a",
		"bench/../a:1",
		"",
		"bench/:1",
		"docker.io/bench/a:1",
	}
	for _, r := range good {
		if !ValidRef(r) {
			t.Errorf("ValidRef(%q) = false, want true", r)
		}
	}
	for _, r := range bad {
		if ValidRef(r) {
			t.Errorf("ValidRef(%q) = true, want false", r)
		}
	}
}
