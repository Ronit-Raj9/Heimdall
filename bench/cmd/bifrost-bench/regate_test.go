package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

// regateFixture is three counted runs as sealed and as regated: r_1 loses its
// veto, r_2 keeps it, r_3 never had one.
func regateFixture() (sealed, regated []schemas.SealedRun) {
	g := func(gate, version string, fired bool) schemas.GateResult {
		return schemas.GateResult{Gate: gate, Version: version, Effect: schemas.GateVeto, Fired: fired}
	}
	run := func(id, task string, o schemas.Outcome, gates ...schemas.GateResult) schemas.SealedRun {
		return schemas.SealedRun{Slot: schemas.Slot{Arm: "B", Task: task, Condition: "clean"}, Run: schemas.Run{RunID: id, Outcome: o}, Gates: gates}
	}
	sealed = []schemas.SealedRun{
		run("r_1", "t08", schemas.OutcomeIntegrityViolation, g("g1_protected_paths", "1.0.0", true), g("g2_test_infra", "1.0.0", false)),
		run("r_2", "t05", schemas.OutcomeIntegrityViolation, g("g1_protected_paths", "1.0.0", true), g("g2_test_infra", "1.0.0", false)),
		run("r_3", "t01", schemas.OutcomePass, g("g1_protected_paths", "1.0.0", false), g("g2_test_infra", "1.0.0", false)),
	}
	regated = []schemas.SealedRun{
		run("r_1", "t08", schemas.OutcomePass, g("g1_protected_paths", "1.1.0", false), g("g2_test_infra", "1.0.0", false)),
		run("r_2", "t05", schemas.OutcomeIntegrityViolation, g("g1_protected_paths", "1.0.0", true), g("g2_test_infra", "1.0.0", false)),
		run("r_3", "t01", schemas.OutcomePass, g("g1_protected_paths", "1.1.0", false), g("g2_test_infra", "1.0.0", false)),
	}
	return sealed, regated
}

func TestGateSourceTables(t *testing.T) {
	sealed, regated := regateFixture()
	got := gateSourceTables(sealed, regated, map[string]string{"g1_protected_paths": "1.1.0", "g2_test_infra": "1.0.0"})
	for _, want := range []string{
		"| g1_protected_paths | veto | 1.0.0 | 1.1.0 | 2 | 1 | 1 |",
		"| g2_test_infra | veto | 1.0.0 | 1.0.0 | 0 | 0 | 0 |",
		"Outcomes changed in 1 of 3 counted runs (integrity_violation to pass: 1).",
		"| `r_1` | B | t08 | clean | integrity_violation | pass |",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "`r_2`") {
		t.Fatalf("unchanged run listed:\n%s", got)
	}
	none := gateSourceTables(sealed, sealed, nil)
	if !strings.Contains(none, "Outcomes changed in 0 of 3 counted runs.") || !strings.Contains(none, "| g1_protected_paths | veto | 1.0.0 | - | 2 | 2 | 3 |") {
		t.Fatalf("no-change table:\n%s", none)
	}
}

func TestGateSourceMarkdown(t *testing.T) {
	sealed, regated := regateFixture()
	info := &store.RegradeInfo{RegradeID: "rg_x", GateVersions: map[string]string{"g1_protected_paths": "1.1.0"},
		Note: "G1 and G4 1.1.0 keep moved lines", CreatedAt: time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)}
	info.SuiteLockHash = "8d94484578c0c921229d"
	got := gateSourceMarkdown(info, "520215ace7b304de232f", sealed, regated)
	for _, want := range []string{
		"## Gate source\n\n",
		"Gates recomputed after the fact by regrade `rg_x` (created 2026-11-14T08:00:00Z)",
		"No agent, model or grader ran again.",
		"**Post hoc.**",
		"Why: G1 and G4 1.1.0 keep moved lines\n",
		"| g1_protected_paths | veto | 1.0.0 | 1.1.0 | 2 | 1 | 1 |",
		"The suite lock changed after the freeze (now `8d94484578c0`); the lock entries of the 3 tasks",
		"The frozen lock is `520215ace7b3`.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestInsertSection(t *testing.T) {
	md := "# Title\n\nExperiment `e`, grade source `rg_x`.\n\n## Primary endpoint\n\nrows\n\n## Guards\n"
	tests := []struct {
		name, md, sec, want string
	}{
		{"before the first section", md, "## Gate source\n\ntext\n",
			"# Title\n\nExperiment `e`, grade source `rg_x`.\n\n## Gate source\n\ntext\n\n## Primary endpoint\n\nrows\n\n## Guards\n"},
		{"nothing to insert", md, "", md},
		{"no section to put it before", "# Title\n", "## Gate source\n", "# Title\n\n## Gate source\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := insertSection(tt.md, tt.sec); got != tt.want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

func TestCheckSuiteDrift(t *testing.T) {
	frozen := schemas.SuiteLock{Suite: "coding-core",
		Tasks:  map[string]schemas.TaskLock{"t08": {Version: "1.0.0", RepoTreeSHA256: "aa"}},
		Probes: map[string]schemas.ProbeLock{"stale-notes": {Version: "1.0.0", SHA256: "bb"}},
		Fingerprints: &schemas.LockFingerprints{Version: 1, Suite: schemas.Fingerprint{Settings: "s", Files: "f"},
			Tasks:  map[string]schemas.Fingerprint{"t08": {Settings: "ts", Files: "tf"}},
			Probes: map[string]schemas.Fingerprint{"stale-notes": {Settings: "ps", Files: "pf"}}}}
	raw, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "suite.lock")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	frozenHash := utils.SHA256Bytes(raw)
	m := &schemas.Manifest{SuiteLockHash: frozenHash, Slots: []schemas.Slot{
		{Task: "t08", Condition: "clean"}, {Task: "t08", Condition: "stale-notes"}}}
	// now returns the current suite: the frozen lock with edit applied.
	now := func(edit func(l *schemas.SuiteLock)) *schemas.Suite {
		l := frozen
		l.Tasks = map[string]schemas.TaskLock{"t08": frozen.Tasks["t08"]}
		l.Probes = map[string]schemas.ProbeLock{"stale-notes": frozen.Probes["stale-notes"]}
		edit(&l)
		return &schemas.Suite{ID: "coding-core", Lock: &l, LockHash: "sha256-now"}
	}
	probeAdded := func(l *schemas.SuiteLock) { l.Probes["memory-trap"] = schemas.ProbeLock{Version: "1.0.0"} }
	tests := []struct {
		name  string
		suite *schemas.Suite
		lock  string
		err   string
	}{
		{"same lock hash", &schemas.Suite{LockHash: frozenHash}, "", ""},
		{"unused probe added", now(probeAdded), path, ""},
		{"lock changed, no frozen lock given", now(probeAdded), "", "pass the frozen lock with -suite-lock"},
		{"frozen lock with another hash", now(probeAdded), writeTemp(t, `{"suite":"x"}`), "the manifest froze"},
		{"used task changed", now(func(l *schemas.SuiteLock) { l.Tasks["t08"] = schemas.TaskLock{Version: "1.0.1"} }), path, "task t08 changed"},
		{"used probe changed", now(func(l *schemas.SuiteLock) { l.Probes["stale-notes"] = schemas.ProbeLock{Version: "2.0.0"} }), path, "probe stale-notes changed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSuiteDrift(m, tt.suite, tt.lock)
			if tt.err == "" && err != nil || tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)) {
				t.Fatalf("err = %v, want %q", err, tt.err)
			}
		})
	}
}

func writeTemp(t *testing.T, data string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "other.lock")
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
