package environments

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestPickAgent(t *testing.T) {
	tests := []struct {
		names []string
		want  string
	}{
		{[]string{"bench-r_1"}, "bench-r_1"},
		{[]string{"bench-r_1-s1", "bench-r_1-s10", "bench-r_1-s2", "bench-r_1-svc-kv", "bench-tp-r_1"}, "bench-r_1-s10"},
		{[]string{"bench-r_1-svc-kv", "bench-r_10", "bench-r_1-prep-abc"}, ""},
		{nil, ""},
	}
	for _, tt := range tests {
		if got := pickAgent("r_1", tt.names); got != tt.want {
			t.Errorf("pickAgent(%v) = %q, want %q", tt.names, got, tt.want)
		}
	}
}

func TestEnvChanges(t *testing.T) {
	container := []string{"PATH=/opt/bench/shim:/usr/bin", "LANG=C", "SECRET=s3cr3t", "HOME=/home/agent", "BENCH_WRAP_F0_0=abc", "TZ=UTC"}
	image := []string{"PATH=/usr/bin", "LANG=C", "HOME=/root", "TZ=Asia/Kolkata"}
	want := []string{`ENV PATH="/usr/bin"`, "ENV SECRET=", `ENV HOME="/root"`, "ENV BENCH_WRAP_F0_0=", `ENV TZ="Asia/Kolkata"`}
	if got := envChanges(container, image); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q", got)
	}
	if got := envChanges([]string{"PATH=/x"}, nil); !reflect.DeepEqual(got, []string{"ENV PATH=" + defaultPath}) {
		t.Fatalf("no image PATH: %q", got)
	}
}

func TestSnapshotWithFakeDocker(t *testing.T) {
	docker, logPath := fakeDocker(t)
	t.Setenv("FAKE_PS", "bench-r_1-s1 bench-r_1-s2 bench-r_1-svc-kv")
	e := newDockerWorkspace(DockerWorkspaceConfig{Docker: docker})
	rc := &schemas.RunContext{Run: &schemas.Run{RunID: "r_1", ExpID: "e_1"}, Dir: t.TempDir()}
	id, err := e.Snapshot(t.Context(), rc)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(rc.Dir, SnapshotRefFile))
	if err != nil || strings.TrimSpace(string(raw)) != id || !strings.HasPrefix(id, "sha256:3333") {
		t.Fatalf("ref %q %q %v", id, raw, err)
	}
	all := calls(t, logPath)
	commit := callWith(all, "commit")
	for _, part := range []string{"--change ENV SECRET=", `--change ENV PATH="/usr/bin"`, "--change LABEL bifrost-bench.run=r_1", "--change LABEL bifrost-bench.exp=e_1", "bench-r_1-s2 bench/snap-r_1:latest"} {
		if !strings.Contains(commit, part) {
			t.Errorf("commit %q lacks %q", commit, part)
		}
	}
	if strings.Contains(commit, "LANG") {
		t.Errorf("image variable blanked: %s", commit)
	}
	if callWith(all, "rm -f -v bench-r_1-s2") == "" {
		t.Error("frozen container not removed")
	}
	t.Setenv("FAKE_PS", "bench-r_1-svc-kv")
	if _, err := e.Snapshot(t.Context(), rc); err == nil || !strings.Contains(err.Error(), "is gone") {
		t.Fatalf("no agent container: %v", err)
	}
}
