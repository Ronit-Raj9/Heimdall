package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestProbeExperimentAddsProbeConditions(t *testing.T) {
	m := &schemas.Manifest{ExpID: "e_parent", Experiment: schemas.Experiment{
		Name: "base", Seed: 7, Suite: "s@1",
		Arms:       map[string]schemas.ArmSpec{"A": {}, "B": {}},
		Primary:    schemas.Endpoint{Condition: schemas.ConditionClean},
		Conditions: []schemas.ConditionSpec{{ID: schemas.ConditionClean, Tasks: schemas.TaskSelector{IDs: []string{"t1", "t2"}}, Trials: 3}},
		Extends:    &schemas.ExtendsRef{ExpID: "e_old"},
	}}
	e, _, err := probeExperiment(m, []string{"p1", "p2"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "base-probes" || e.Seed != 8 || e.Extends != nil || e.ID != "" {
		t.Fatalf("follow-up identity wrong: %+v", e)
	}
	if len(e.Conditions) != 3 || e.Conditions[1].ID != "p1" || e.Conditions[2].ID != "p2" || e.Conditions[2].Trials != 3 || len(e.Conditions[2].Tasks.IDs) != 2 {
		t.Fatalf("conditions = %+v", e.Conditions)
	}
	if len(m.Experiment.Conditions) != 1 {
		t.Fatal("parent manifest was mutated")
	}
	e, _, err = probeExperiment(m, []string{"p1", "p2"}, []string{"p2"}, 5)
	if err != nil || len(e.Conditions) != 2 || e.Conditions[1].ID != "p2" || e.Conditions[1].Trials != 5 {
		t.Fatalf("subset: %+v %v", e, err)
	}
	if _, _, err := probeExperiment(m, []string{"p1"}, []string{"nope"}, 0); err == nil {
		t.Fatal("unknown probe accepted")
	}
	if _, _, err := probeExperiment(m, nil, nil, 0); err == nil || !strings.Contains(err.Error(), "no probes") {
		t.Fatalf("suite without probes: %v", err)
	}
}

func TestWriteFollowUpFileNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	e := &schemas.Experiment{Name: "x-ext", Suite: "s@1"}
	p1, err := writeFollowUpFile(dir, e)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := writeFollowUpFile(dir, e)
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatalf("second write reused %s", p1)
	}
	for _, p := range []string{p1, p2} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidateMigrateJob(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "codex.yaml"), []byte("id: codex\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := jobEnv{App: &app{cfg: &lib.Config{Root: root}, logger: schemas.NoopLogger{}}}
	ok := `{"agent":"agents/codex.yaml","from":"gpt-5-mini","to":["claude-haiku-4-5"],"suite":"coding-core@1.0.0"}`
	got, target, err := validateMigrateJob(context.Background(), env, json.RawMessage(ok))
	if err != nil {
		t.Fatal(err)
	}
	if p := got.(migrateJobParams); p.Trials != 3 || !strings.HasPrefix(target, "migrate:") {
		t.Fatalf("defaults: %+v %s", p, target)
	}
	bad := map[string]string{
		"escape":  `{"agent":"../x.yaml","from":"a","to":["b"],"suite":"s@1"}`,
		"notyaml": `{"agent":"agents/x","from":"a","to":["b"],"suite":"s@1"}`,
		"missing": `{"agent":"agents/x.yaml","from":"a","to":[],"suite":"s@1"}`,
		"same":    `{"agent":"agents/x.yaml","from":"a","to":["a"],"suite":"s@1"}`,
		"newline": `{"agent":"agents/x.yaml","from":"a\nb","to":["c"],"suite":"s@1"}`,
		"unknown": `{"agent":"agents/x.yaml","from":"a","to":["c"],"suite":"s@1","shell":"rm"}`,
		"trials":  `{"agent":"agents/x.yaml","from":"a","to":["c"],"suite":"s@1","trials":999}`,
	}
	for name, raw := range bad {
		if _, _, err := validateMigrateJob(context.Background(), env, json.RawMessage(raw)); !errors.Is(err, jobs.ErrInvalidParams) {
			t.Errorf("%s: want ErrInvalidParams, got %v", name, err)
		}
	}
}

func TestExperimentJobKindsRegistered(t *testing.T) {
	want := map[string]bool{kindExperimentExtend: false, kindExperimentMigrate: false, kindProbeRun: false}
	for _, k := range jobKinds {
		if _, ok := want[k.Kind]; ok {
			want[k.Kind] = k.Local && k.Hosted && k.Validate != nil && k.Run != nil
		}
	}
	for k, ok := range want {
		if !ok {
			t.Errorf("%s not registered for local and hosted", k)
		}
	}
}
