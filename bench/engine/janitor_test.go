package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/utils"
)

// fakeDocker writes a shell script that logs its arguments and prints canned
// listings, so the janitor runs with no docker daemon.
func fakeDocker(t *testing.T, failList string) (utils.Docker, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> %q
case "$1 $2" in
  "%s") echo "daemon down" >&2; exit 1 ;;
  "ps -a") printf 'c1\tr_dead\nc2\tr_live\nc3\t\n' ;;
  "volume ls") printf 'v1\tr_dead\nv2\t<no value>\n' ;;
  "network ls") printf 'n1\tr_live\n' ;;
esac
exit 0
`, logPath, failList)
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, logPath
}

func TestJanitorSweep(t *testing.T) {
	tests := []struct {
		name     string
		failList string
		wantRm   []string
		wantErr  string
		want     SweepReport
	}{
		{"removes only stale run objects", "none", []string{"rm -f c1", "volume rm -f v1"}, "",
			SweepReport{Containers: 1, Volumes: 1, Keys: 2}},
		{"one kind failing does not stop the rest", "ps -a", []string{"volume rm -f v1"}, "list containers",
			SweepReport{Volumes: 1, Keys: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			docker, logPath := fakeDocker(t, tt.failList)
			active := map[string]bool{"r_live": true}
			rep, err := NewJanitor(b.gw, docker, nil).Sweep(context.Background(), active)
			if (tt.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if *rep != tt.want {
				t.Fatalf("report %+v, want %+v", *rep, tt.want)
			}
			raw, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			var removed []string
			for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				if strings.HasPrefix(line, "rm ") || strings.HasPrefix(line, "volume rm ") || strings.HasPrefix(line, "network rm ") {
					removed = append(removed, line)
				}
				if strings.Contains(line, " ls") || strings.HasPrefix(line, "ps") {
					if !strings.Contains(line, "--filter label=bifrost-bench=1") {
						t.Errorf("listing without the bench label filter: %s", line)
					}
				}
			}
			if strings.Join(removed, "|") != strings.Join(tt.wantRm, "|") {
				t.Fatalf("removed %v, want %v", removed, tt.wantRm)
			}
			if !b.gw.active["r_live"] || b.w.count("revoke_stale") != 1 {
				t.Fatal("stale keys not revoked with the active set")
			}
		})
	}
}

func TestStaleObjects(t *testing.T) {
	out := []byte("a\tr_1\nb\tr_2\nc\t\nd\t<no value>\n\n")
	got := staleObjects(out, map[string]bool{"r_2": true})
	if strings.Join(got, ",") != "a" {
		t.Fatalf("got %v", got)
	}
}

func TestSchedulerRunsJanitor(t *testing.T) {
	b := newBench(t)
	docker, _ := fakeDocker(t, "none")
	m := b.plan(t, testExperiment())
	if _, err := NewScheduler(b.reg, b.root, nil, NewJanitor(b.gw, docker, nil)).Run(context.Background(), m, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	if b.w.count("revoke_stale") != 1 {
		t.Fatal("janitor did not sweep at scheduler start")
	}
}
