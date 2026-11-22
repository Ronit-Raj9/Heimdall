package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// fakeOrphanDocker writes a stand-in docker binary: `ps` prints ps.txt,
// `network ls` prints nets.txt, every other call succeeds. Every call is
// logged on one line.
func fakeOrphanDocker(t *testing.T, ps, nets string) (utils.Docker, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	psPath, netsPath := filepath.Join(dir, "ps.txt"), filepath.Join(dir, "nets.txt")
	if err := os.WriteFile(psPath, []byte(ps), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(netsPath, []byte(nets), 0o644); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> %[1]q
case "$1 $2" in
  "ps -a") cat %[2]q ;;
  "network ls") cat %[3]q ;;
esac
exit 0
`, logPath, psPath, netsPath)
	bin := filepath.Join(dir, "docker")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return utils.Docker{Binary: bin}, logPath
}

func orphanLine(id, name string, created time.Time, run, project, compose string) string {
	return strings.Join([]string{id, name, created.UTC().Format("2006-01-02 15:04:05 -0700 MST"), run, project, compose}, "\t") + "\n"
}

func TestSweepOrphanRuns(t *testing.T) {
	old := utils.Project()
	utils.SetProject("84875ded00904c69")
	defer utils.SetProject(old)
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	day := now.Add(-27 * time.Hour)
	young := now.Add(-10 * time.Minute)
	const proj = "84875ded00904c69"
	ps := orphanLine("c1", "bench-r_5mw5fo5cm9-svc-agentdojo", day, "r_5mw5fo5cm9", proj, "") + // removed
		orphanLine("c2", "bench-tp-r_93pkv9f5l4", day, "r_93pkv9f5l4", "", "") + // removed: no project label
		orphanLine("c3", "bench-r_active-svc-x", day, "r_active", proj, "") + // kept: active run
		orphanLine("c4", "bench-r_young", young, "r_young", proj, "") + // kept: young
		orphanLine("c5", "bifrost-bench-postgres-1", day, "", "", "bifrost-bench") + // kept: compose, no run label
		orphanLine("c6", "bench-r_compose", day, "r_compose", proj, "bifrost-bench") + // kept: compose project
		orphanLine("c7", "bench-r_other", day, "r_other", "ffffffffffffffff", "") + // kept: other project
		orphanLine("c8", "agent-r_named", day, "r_named", proj, "") + // kept: not a bench- name
		orphanLine("c9", "bench-r_notime", time.Time{}, "r_notime", proj, "") // kept: unknown age (zero time parses; see below)
	ps = strings.Replace(ps, "0001-01-01 00:00:00 +0000 UTC", "not a time", 1)
	nets := "n1\tbench-net-r_5mw5fo5cm9\t2026-11-21 16:49:15.68234497 +0000 UTC\tr_5mw5fo5cm9\t" + proj + "\t\n" + // removed
		"n2\tbench-hnet-r_5mw5fo5cm9\t2026-11-21 16:49:15.995720792 +0000 UTC\tr_5mw5fo5cm9\t" + proj + "\t\n" + // removed
		"n3\tbench-evalnet\t2026-11-21 05:54:11.717500548 +0000 UTC\t\t\t\n" + // kept: no run label
		"n4\tbench-upstream\t2026-11-21 05:54:11.717847669 +0000 UTC\t\t\tbifrost-bench\n" + // kept: compose, no run
		"n5\tbench-net-r_active\t2026-11-21 16:49:15 +0000 UTC\tr_active\t" + proj + "\t\n" // kept: active run
	d, logPath := fakeOrphanDocker(t, ps, nets)
	active := func() map[string]bool { return map[string]bool{"r_active": true} }
	rep := sweepOrphanRuns(context.Background(), d, active, now, orphanMinAge, schemas.NoopLogger{})
	if rep.Containers != 2 || rep.Networks != 2 {
		t.Fatalf("report %+v", rep)
	}
	if strings.Join(rep.Runs, ",") != "r_5mw5fo5cm9,r_93pkv9f5l4" {
		t.Fatalf("runs %v", rep.Runs)
	}
	var removals []string
	for _, c := range orphanCalls(t, logPath) {
		if strings.HasPrefix(c, "rm ") || strings.HasPrefix(c, "network rm ") {
			removals = append(removals, c)
		}
	}
	want := []string{"rm -f -v c1", "rm -f -v c2", "network rm n1", "network rm n2"}
	if strings.Join(removals, "|") != strings.Join(want, "|") {
		t.Fatalf("removals %q, want %q (containers before networks)", removals, want)
	}
	all := strings.Join(orphanCalls(t, logPath), "\n")
	for _, keep := range []string{"c3", "c4", "c5", "c6", "c7", "c8", "c9", "n3", "n4", "n5"} {
		if strings.Contains(all, "rm -f -v "+keep) || strings.Contains(all, "network rm "+keep) {
			t.Errorf("%s removed", keep)
		}
	}
	if !strings.Contains(all, "ps -a --no-trunc --filter label="+utils.LabelRun) || !strings.Contains(all, "network ls --no-trunc --filter label="+utils.LabelRun) {
		t.Errorf("listings not filtered by the run label:\n%s", all)
	}
}

// orphanCalls returns the logged docker calls.
func orphanCalls(t *testing.T, logPath string) []string {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

// N151 step 3: before the org project's first download the shared runner
// still plans from the org's folder, so a download before a later lease
// brings the org's benchmarks in for the life of the loop.
func TestSharedOrgProjectUsesRootWithoutBenchmarks(t *testing.T) {
	libRoot, orgA, _ := libraryFixture(t)
	t.Setenv(suite.LibraryEnv, libRoot)
	root := filepath.Join(t.TempDir(), "org_a") // not downloaded yet: no folder at all
	a := &app{cfg: &lib.Config{Root: t.TempDir(), BenchmarksRoot: "benchmarks"}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), reg: &schemas.Registry{}}
	reg, planRoot := sharedOrgProject(a, root)
	if planRoot != root {
		t.Fatalf("plan root %q, want the org folder %q", planRoot, root)
	}
	if fi, err := os.Stat(filepath.Join(root, "benchmarks")); err != nil || !fi.IsDir() {
		t.Fatalf("benchmarks folder not created: %v", err)
	}
	if _, err := engine.LoadSuite(context.Background(), reg, "mini"); err != nil {
		t.Fatalf("library suite not loadable before the download: %v", err)
	}
	copyTree(t, orgA, root) // the download before the next lease
	if _, err := engine.LoadSuite(context.Background(), reg, "a-own"); err != nil {
		t.Fatalf("org suite downloaded after the loop started is not loadable: %v", err)
	}
}
