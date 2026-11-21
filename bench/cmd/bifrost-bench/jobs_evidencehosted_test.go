package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

// hostedEvidenceSandbox is a hosted storage root with two orgs: org_a holds
// the recorded experiment e_smoke_sdoj, org_b holds nothing. mirror, when
// set, is the server's storage.mirror_dir.
func hostedEvidenceSandbox(t *testing.T, mirror string) (a, b jobEnv) {
	t.Helper()
	src := filepath.Join("..", "..", "testdata", "recorded", "e_smoke_sdoj")
	if _, err := os.Stat(src); err != nil {
		t.Skip("recorded experiment not present")
	}
	base := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	if err := tenancyCopy(src, filepath.Join(base, "storage", "orgs", "org_a", "e_smoke_sdoj")); err != nil {
		t.Fatal(err)
	}
	logger := utils.NewStdLogger(io.Discard, false)
	mk := func(org string) jobEnv {
		storeRoot := filepath.Join(base, "storage", "orgs", org)
		project := filepath.Join(base, "storage", "projects", org)
		for _, d := range []string{storeRoot, project} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		st, err := store.NewFSStore(storeRoot, logger)
		if err != nil {
			t.Fatal(err)
		}
		cfg := &lib.Config{Root: project, RunsRoot: storeRoot}
		if mirror != "" {
			cfg.Storage.MirrorDir = filepath.Join(base, mirror)
		}
		return jobEnv{App: &app{cfg: cfg, store: st, logger: logger}, OrgID: org, ProjectRoot: project, Hosted: true}
	}
	return mk("org_a"), mk("org_b")
}

func tenancyCopy(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
}

// hostedSpec is the hosted variant of a kind.
func hostedSpec(t *testing.T, kind string) jobKindSpec {
	t.Helper()
	jobKindsMu.Lock()
	defer jobKindsMu.Unlock()
	for _, s := range jobKinds {
		if s.Kind == kind && s.Hosted {
			return s
		}
	}
	t.Fatalf("no hosted variant of %s", kind)
	return jobKindSpec{}
}

func runHosted(t *testing.T, env jobEnv, kind, params string) (any, error, *opLog) {
	t.Helper()
	s := hostedSpec(t, kind)
	if _, _, err := s.Validate(context.Background(), env, json.RawMessage(params)); err != nil {
		return nil, err, &opLog{}
	}
	log := &opLog{}
	res, err := s.Run(context.Background(), env, &jobs.Job{Kind: kind, Params: json.RawMessage(params)}, log, func(any) {})
	return res, err, log
}

func TestHostedEvidenceVerifySeededOrgRun(t *testing.T) {
	a, _ := hostedEvidenceSandbox(t, "")
	res, err, log := runHosted(t, a, kindEvidenceVerify, `{}`)
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, log.text())
	}
	m := res.(map[string]any)
	items := m["experiments"].([]hostedVerifyItem)
	if m["ok"] != true || len(items) != 1 || items[0].ExpID != "e_smoke_sdoj" || items[0].Seals == 0 || !items[0].OK {
		t.Fatalf("result %+v", m)
	}
	// Tampering with a sealed file is caught.
	runs, _ := filepath.Glob(filepath.Join(a.App.store.Root(), "e_smoke_sdoj", "slots", "*", "runs", "*", "grade.json"))
	if len(runs) == 0 {
		t.Skip("recorded run has no grade.json")
	}
	_ = os.Chmod(filepath.Dir(runs[0]), 0o755)
	_ = os.Chmod(runs[0], 0o644)
	if err := os.WriteFile(runs[0], []byte(`{"tampered":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err, _ := runHosted(t, a, kindEvidenceVerify, `{"exp":"e_smoke_sdoj"}`); err == nil {
		t.Fatal("verify passed a tampered run")
	}
}

func TestHostedEvidenceOrgIsolation(t *testing.T) {
	a, b := hostedEvidenceSandbox(t, "mirror")
	// org_b can not name org_a's experiment for verify or backup.
	if _, err, _ := runHosted(t, b, kindEvidenceVerify, `{"exp":"e_smoke_sdoj"}`); err == nil || !strings.Contains(err.Error(), "not in this organization") {
		t.Fatalf("org_b verify of org_a's experiment: %v", err)
	}
	if _, err, _ := runHosted(t, b, kindEvidenceBackup, `{}`); err == nil {
		t.Fatal("org_b backed up evidence it does not have")
	}
	// org_a's backup lands in org_a's exports and holds only its experiment.
	res, err, log := runHosted(t, a, kindEvidenceBackup, `{}`)
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, log.text())
	}
	br := res.(hostedBackupResult)
	if !strings.HasPrefix(br.Path, "exports/evidence/evidence-org_a-") || br.Files == 0 {
		t.Fatalf("backup %+v", br)
	}
	zr, err := zip.OpenReader(filepath.Join(a.ProjectRoot, br.Path))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "e_smoke_sdoj/") {
			t.Errorf("zip entry outside the experiment: %s", f.Name)
		}
	}
	zr.Close()
	if list, _ := handlers.ListOperateBackups(b.ProjectRoot); len(list) != 0 {
		t.Fatalf("org_b sees backups %v", list)
	}
	// Mirror org_a's experiment; org_b can not restore it (its mirror keys
	// are its own org id), org_a can.
	m, err := hostedMirror(a)
	if err != nil || m == nil {
		t.Fatalf("mirror: %v", err)
	}
	orgA, _ := hostedEvidenceOrg(a)
	if _, err := m.Experiment(context.Background(), orgA, "e_smoke_sdoj"); err != nil {
		t.Fatal(err)
	}
	if _, err, _ := runHosted(t, b, kindEvidenceRestore, `{"exp":"e_smoke_sdoj"}`); err == nil {
		t.Fatal("org_b restored org_a's experiment")
	}
	if _, err := os.Stat(filepath.Join(b.App.store.Root(), "e_smoke_sdoj")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("org_b folder got org_a's experiment: %v", err)
	}
	res, err, log = runHosted(t, a, kindEvidenceVerify, `{"exp":"e_smoke_sdoj"}`)
	if err != nil {
		t.Fatalf("verify with mirror: %v\n%s", err, log.text())
	}
	it := res.(map[string]any)["experiments"].([]hostedVerifyItem)[0]
	if it.Mirror == nil || !it.Mirror.OK() || it.Mirror.Mirror != "" {
		t.Fatalf("mirror report %+v", it.Mirror)
	}
	// Server owner flags are refused.
	if _, err, _ := runHosted(t, a, kindEvidenceVerify, `{"no_db":true}`); !errors.Is(err, jobs.ErrInvalidParams) {
		t.Fatalf("no_db: %v", err)
	}
}

func TestHostedEvidenceNoMirror(t *testing.T) {
	a, _ := hostedEvidenceSandbox(t, "")
	for _, k := range []string{kindEvidenceArchive, kindEvidenceRestore} {
		if _, err, _ := runHosted(t, a, k, `{"exp":"e_smoke_sdoj"}`); !errors.Is(err, errHostedNoMirror) {
			t.Errorf("%s: %v", k, err)
		}
	}
}

func TestHostedBackupPrune(t *testing.T) {
	a, _ := hostedEvidenceSandbox(t, "")
	org, _ := hostedEvidenceOrg(a)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < hostedBackupKeep+2; i++ {
		if _, err := hostedEvidenceBackup(context.Background(), a, org, operateParams{}, &opLog{}, start.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
		// distinct mod times for the newest-first order
		p := filepath.Join(handlers.OperateBackupDir(a.ProjectRoot), handlers.OperateBackupName("org_a", start.Add(time.Duration(i)*time.Hour)))
		_ = os.Chtimes(p, start.Add(time.Duration(i)*time.Hour), start.Add(time.Duration(i)*time.Hour))
	}
	list, _ := handlers.ListOperateBackups(a.ProjectRoot)
	if len(list) > hostedBackupKeep+1 { // the last prune ran before the last Chtimes
		t.Fatalf("kept %d backups", len(list))
	}
}

func TestRunnerDoctorRows(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	env := jobEnv{OrgID: "org_a", Services: handlers.Services{Quota: quota.NewStub()}}
	rows := runnerDoctorRows(context.Background(), env, nil, true, now)
	if len(rows) != 3 || rows[0].ID != "runners.online" || rows[0].Status != schemas.DoctorWarn || !strings.Contains(rows[0].Detail, "Runners page") {
		t.Fatalf("no runners: %+v", rows)
	}
	workers := []schemas.WorkerInfo{
		{ID: "w1", State: schemas.WorkerIdle, LastSeen: now.Add(-10 * time.Second)},
		{ID: "w2", State: schemas.WorkerRunning, Pool: "shared", LastSeen: now.Add(-5 * time.Second)},
		{ID: "w3", State: schemas.WorkerLost, LastSeen: now.Add(-time.Hour)},
	}
	rows = runnerDoctorRows(context.Background(), env, workers, true, now)
	if rows[0].Status != schemas.DoctorPass || !strings.Contains(rows[0].Detail, "2 runner(s) online: 1 of your own, 1 shared") {
		t.Fatalf("online row %+v", rows[0])
	}
	if rows[1].ID != "runners.heartbeat" || rows[1].Status != schemas.DoctorPass || !strings.Contains(rows[1].Detail, "5s ago") {
		t.Fatalf("heartbeat row %+v", rows[1])
	}
	if rows[2].ID != "runners.shared" {
		t.Fatalf("shared row %+v", rows[2])
	}
	if rows := runnerDoctorRows(context.Background(), env, nil, false, now); rows[0].Status != schemas.DoctorFail {
		t.Fatalf("no coordinator %+v", rows)
	}
}

func TestWithJobConfig(t *testing.T) {
	cases := map[string]string{
		"k8s check":                             "k8s check -config {{config}}",
		"voice check -suite s -json":            "voice check -config {{config}} -suite s -json",
		"export csv -config {{config}} -exp e1": "export csv -config {{config}} -exp e1",
	}
	for in, want := range cases {
		if got := strings.Join(withJobConfig(strings.Fields(in)), " "); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// Every operate and govern CLI job runs with -config set to the job's copy
// of the server config, never the project's deploy/bench.yaml.
func TestCLIJobsUseServerConfig(t *testing.T) {
	prev := cliExecutable
	cliExecutable = func() (string, error) { return "/bin/echo", nil }
	t.Cleanup(func() { cliExecutable = prev })
	project := t.TempDir()
	env := jobEnv{App: &app{cfg: &lib.Config{Root: project, RunsRoot: "runs"}}, ProjectRoot: project}
	cases := map[string]string{
		kindVoiceCheck:      `{"suite":"voice-core"}`,
		kindVisualCompare:   `{"suite":"s","task":"t","image":"uploads/shot.png"}`,
		kindVisualCapture:   `{"browser":"pw"}`,
		kindSimulatorSpread: `{"a":"e_1","b":"e_2"}`,
		kindK8sStatus:       `{}`,
		"monitor.certify":   `{"monitor":"m.yaml","exp_ids":["e_1"]}`,
		"scan.skill":        `{"path":"agents/a/skills/s"}`,
		"export.run":        `{"format":"csv","exp_id":"exp_1"}`,
	}
	for kind, raw := range cases {
		s := evidenceLocalSpec(t, kind)
		log := &opLog{}
		if _, err := s.Run(context.Background(), env, &jobs.Job{Kind: kind, Params: json.RawMessage(raw)}, log, func(any) {}); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		out := log.text()
		if strings.Count(out, " -config ") != 1 || !strings.Contains(out, filepath.Join(project, jobConfigDir)) || strings.Contains(out, "deploy/bench.yaml") {
			t.Errorf("%s: argv %q does not use the job config", kind, out)
		}
	}
}

func evidenceLocalSpec(t *testing.T, kind string) jobKindSpec {
	t.Helper()
	jobKindsMu.Lock()
	defer jobKindsMu.Unlock()
	for _, s := range jobKinds {
		if s.Kind == kind && s.Local {
			return s
		}
	}
	t.Fatalf("no local variant of %s", kind)
	return jobKindSpec{}
}
