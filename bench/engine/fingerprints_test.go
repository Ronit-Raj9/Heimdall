package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	suitepkg "github.com/maximhq/bifrost/bench/suite"
)

// fingerprintBench is the planner test bench with lock fingerprints computed
// from its current suite, so the lock is fingerprinted and unchanged.
func fingerprintBench(t *testing.T) *bench {
	t.Helper()
	b := newBench(t)
	fps, err := suitepkg.Fingerprints(b.suite, FingerprintOptions(context.Background(), b.reg, b.suite))
	if err != nil {
		t.Fatal(err)
	}
	b.suite.Lock.Fingerprints = fps
	return b
}

func TestPlanLockFingerprints(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(t *testing.T, b *bench)
		smoke   bool
		wantErr string
		wantLog string
	}{
		{name: "unchanged", mutate: func(*testing.T, *bench) {}},
		{name: "legacy lock warns", mutate: func(_ *testing.T, b *bench) { b.suite.Lock.Fingerprints = nil },
			wantLog: suitepkg.LegacyLockWarning},
		{name: "verifier command", mutate: func(_ *testing.T, b *bench) { b.suite.Verifier.Command = []string{"pytest", "-x"} },
			wantErr: "suite lock fingerprints drifted, refusing to plan: suite coding-core: settings changed (verifier)"},
		{name: "verifier timeout", mutate: func(_ *testing.T, b *bench) { b.suite.Verifier.TimeoutS = 1 },
			wantErr: "suite coding-core: settings changed (verifier)"},
		{name: "required test id", mutate: func(_ *testing.T, b *bench) { b.suite.Tasks[0].Verifier.Required = []string{"t::other"} },
			wantErr: "task t01: settings changed (verifier)"},
		{name: "protected path", mutate: func(_ *testing.T, b *bench) { b.suite.Tasks[1].Protected = []string{"setup.py"} },
			wantErr: "task t02: settings changed (protected)"},
		{name: "task timeout", mutate: func(_ *testing.T, b *bench) { b.suite.Tasks[0].Timeouts.AgentS = 5 },
			wantErr: "task t01: settings changed (timeouts)"},
		{name: "task file", mutate: func(t *testing.T, b *bench) {
			if err := os.WriteFile(filepath.Join(b.suite.Tasks[0].Dir, "fixtures", "notes.clean.md"), []byte("edited"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, wantErr: "task t01: files changed"},
		{name: "probe membership", mutate: func(_ *testing.T, b *bench) { b.suite.ProbeIDs = b.suite.ProbeIDs[:1] },
			wantErr: "suite coding-core: settings changed (probes)"},
		{name: "probe settings", mutate: func(_ *testing.T, b *bench) { b.suite.Probes[1].SafeBehavior = "changed" },
			wantErr: "probe model-outage: settings changed (safe_behavior)"},
		{name: "grader or environment version", mutate: func(_ *testing.T, b *bench) { b.suite.Grader = "other_grader" },
			wantErr: "settings changed (grader, parts)"},
		{name: "smoke experiment warns", smoke: true, mutate: func(_ *testing.T, b *bench) { b.suite.Verifier.Command = []string{"pytest", "-x"} },
			wantLog: "ignores lock fingerprint drift"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := fingerprintBench(t)
			tt.mutate(t, b)
			log := &warnLog{}
			e := testExperiment()
			e.Smoke = tt.smoke
			m, err := NewPlanner(b.reg, b.root, log).Plan(context.Background(), e)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil && !strings.Contains(err.Error(), "grader") {
				t.Fatalf("plan: %v", err)
			}
			if tt.wantLog != "" && !strings.Contains(log.joined(), tt.wantLog) {
				t.Fatalf("log %q does not say %q", log.joined(), tt.wantLog)
			}
			if m == nil {
				return
			}
			want, _ := suitepkg.FingerprintHash(b.suite.Lock.Fingerprints)
			if m.FingerprintHash != want {
				t.Fatalf("manifest fingerprint_hash %q, want %q", m.FingerprintHash, want)
			}
			if b.suite.Lock.Fingerprints == nil && m.FingerprintHash != "" {
				t.Fatal("a legacy lock recorded a fingerprint hash")
			}
		})
	}
}

func TestPlanRefusesStaleAdmissionFingerprint(t *testing.T) {
	b := fingerprintBench(t)
	tl := b.suite.Lock.Tasks["t01"]
	tl.Admission = &schemas.AdmissionRecord{Admitted: true, Fingerprint: "an-older-fingerprint"}
	b.suite.Lock.Tasks["t01"] = tl
	_, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), testExperiment())
	if err == nil || !strings.Contains(err.Error(), "task t01 admission was recorded for other lock fingerprints") {
		t.Fatalf("err = %v", err)
	}
	tl.Admission.Fingerprint = suitepkg.AdmissionFingerprint(b.suite.Lock.Fingerprints, "t01")
	b.suite.Lock.Tasks["t01"] = tl
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), testExperiment()); err != nil {
		t.Fatalf("a current admission fingerprint refused: %v", err)
	}
}

func TestSchedulerRefusesFingerprintDrift(t *testing.T) {
	b := fingerprintBench(t)
	m := b.plan(t, testExperiment())
	b.suite.Verifier.Command = []string{"pytest", "-x"}
	_, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{})
	if err == nil || !strings.Contains(err.Error(), "suite lock fingerprints drifted, refusing to run: suite coding-core: settings changed (verifier)") {
		t.Fatalf("err = %v", err)
	}
}

func TestSchedulerAcceptsLockThatOnlyGainedFingerprints(t *testing.T) {
	b := newBench(t)
	legacy, err := suitepkg.LegacyLockHash(b.suite.Lock)
	if err != nil {
		t.Fatal(err)
	}
	b.suite.LockHash = legacy
	m := b.plan(t, testExperiment())
	fps, err := suitepkg.Fingerprints(b.suite, FingerprintOptions(context.Background(), b.reg, b.suite))
	if err != nil {
		t.Fatal(err)
	}
	b.suite.Lock.Fingerprints = fps
	b.suite.LockHash = "hash-of-the-file-with-fingerprints"
	if _, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{}); err != nil {
		t.Fatalf("a manifest planned under the legacy lock no longer runs after fingerprints were added: %v", err)
	}
}

// digestEnv is the inspecting fake environment plus ImageDigester.
type digestEnv struct {
	inspectingEnv
	digests map[string][]string
}

func (e digestEnv) ImageRepoDigests(_ context.Context, ref string) ([]string, error) {
	return e.digests[ref], nil
}

func TestPlanLockImageIdentities(t *testing.T) {
	tests := []struct {
		name    string
		pin     bool
		digests map[string][]string
		rebuilt map[string]string   // local ids after another machine built the images
		pulled  map[string][]string // registry digests after
		wantErr string
	}{
		{name: "tag only plans with another local id", rebuilt: map[string]string{"bench/base:1": "sha256:other"}},
		{name: "pinned local id refuses another local id", pin: true, rebuilt: map[string]string{"bench/base:1": "sha256:other"},
			wantErr: "suite coding-core: image changed (base)"},
		{name: "registry digest with another local id", digests: map[string][]string{"bench/base:1": {"bench/base@sha256:d1"}},
			rebuilt: map[string]string{"bench/base:1": "sha256:other"}, pulled: map[string][]string{"bench/base:1": {"bench/base@sha256:d1"}}},
		{name: "registry digest refuses another digest", digests: map[string][]string{"bench/base:1": {"bench/base@sha256:d1"}},
			rebuilt: map[string]string{"bench/base:1": "sha256:base"}, pulled: map[string][]string{"bench/base:1": {"bench/base@sha256:d2"}},
			wantErr: "suite coding-core: image changed (base)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			b.suite.BaseImage = schemas.ImageSpec{Tag: "bench/base:1"}
			env := digestEnv{inspectingEnv{fakeEnv{part{"docker_workspace"}, b.w}, map[string]string{"bench/base:1": "sha256:base"}}, tt.digests}
			b.reg.Environments = []schemas.Environment{env}
			opts := FingerprintOptions(context.Background(), b.reg, b.suite)
			opts.PinLocal = tt.pin
			fps, err := suitepkg.Fingerprints(b.suite, opts)
			if err != nil {
				t.Fatal(err)
			}
			b.suite.Lock.Fingerprints = fps
			b.reg.Environments = []schemas.Environment{digestEnv{inspectingEnv{fakeEnv{part{"docker_workspace"}, b.w}, tt.rebuilt}, tt.pulled}}
			_, err = NewPlanner(b.reg, b.root, nil).Plan(context.Background(), testExperiment())
			switch {
			case tt.wantErr == "" && err != nil && strings.Contains(err.Error(), "fingerprints drifted"):
				t.Fatalf("refused: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
