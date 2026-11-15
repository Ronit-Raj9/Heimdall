package mcpmark

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

const fixtureRef = "cd45b7f57923b9b3985467f5139927575f83141c"

func fixtureImport(t *testing.T, extra map[string]string, ids []string) (*importers.Result, string) {
	t.Helper()
	root := t.TempDir()
	if extra == nil {
		extra = map[string]string{}
	}
	if _, ok := extra[SetRef]; !ok {
		extra[SetRef] = fixtureRef
	}
	im := &importer{now: func() time.Time { return time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC) }}
	res, err := im.Import(context.Background(), importers.Options{
		BenchRoot: root, From: filepath.Join("testdata", "upstream"), SuiteID: "mcpmark-fs", IDs: ids, Extra: extra,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res, root
}

func TestImportFixture(t *testing.T) {
	res, root := fixtureImport(t, nil, nil)
	wantTasks := []string{"fs-easy-file-context-count-demo", "fs-standard-file-context-uppercase-demo"}
	if !slices.Equal(res.Tasks, wantTasks) {
		t.Fatalf("tasks = %v, want %v", res.Tasks, wantTasks)
	}
	skipped := map[string]string{}
	for _, s := range res.Skipped {
		skipped[s.ID] = s.Reason
	}
	for id, want := range map[string]string{
		"fs-standard-papers-missing-state": "test environment papers not found",
		"postgres/*":                       "1 task(s): needs a PostgreSQL service",
		"github/*":                         "1 task(s): needs a GitHub account",
	} {
		if !strings.Contains(skipped[id], want) {
			t.Errorf("skip %s = %q, want it to contain %q", id, skipped[id], want)
		}
	}
	if len(res.Notes) == 0 || !strings.Contains(strings.Join(res.Notes, "\n"), "2 of 2 task(s) have controls/reference.sh that refuses") {
		t.Errorf("notes = %v", res.Notes)
	}

	s, task, err := suite.LoadTask(filepath.Join(root, "benchmarks"), "mcpmark-fs", "fs-standard-file-context-uppercase-demo")
	if err != nil {
		t.Fatalf("generated suite does not load: %v", err)
	}
	if s.Track != schemas.TrackMCP || s.Grading != schemas.GradingVerified || s.Imported == nil || s.Imported.License != "Apache-2.0" || s.Imported.Revision != fixtureRef {
		t.Errorf("suite = %+v", s)
	}
	if s.Verifier.Result != schemas.VerifierResultReward || s.Verifier.ResultPath != "/grader/out/reward.json" {
		t.Errorf("verifier = %+v", s.Verifier)
	}
	if len(task.Services) != 1 {
		t.Fatalf("services = %+v", task.Services)
	}
	svc := task.Services[0]
	if svc.ID != "fs" || svc.Role != schemas.ServiceRoleMCP || svc.Port != 8080 || svc.Build == nil || svc.MCP == nil || svc.MCP.Path != "/mcp" {
		t.Errorf("service = %+v", svc)
	}
	if len(svc.Export) != 1 || svc.Export[0].Path != "/data" || svc.Export[0].MaxBytes < minExportBytes {
		t.Errorf("export = %+v", svc.Export)
	}
	if task.Output == nil || task.Output.Kind != schemas.OutputState || task.Environment == nil || !task.Environment.BuildNetwork {
		t.Errorf("output/environment = %+v / %+v", task.Output, task.Environment)
	}
	if task.Provenance == nil || task.Provenance.ImportedFrom != "mcpmark filesystem/standard/file_context/uppercase_demo" || task.Difficulty != "hard" {
		t.Errorf("provenance %+v difficulty %q", task.Provenance, task.Difficulty)
	}
	for _, p := range suite.LintTask(s, task) {
		if p.Severity == suite.SeverityError {
			t.Errorf("lint error: %+v", p)
		}
	}

	dir := filepath.Join(root, "benchmarks", "mcpmark-fs", "tasks", "fs-standard-file-context-uppercase-demo")
	for _, f := range []string{"instruction.md", "service/Dockerfile", "service/state/file_01.txt", "service/state/sub/note.txt", "verifier/tests/verify.py", "verifier/tests/runverify.py", "controls/reference.sh", "controls/empty.sh", "controls/exploit.sh"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	df, _ := os.ReadFile(filepath.Join(dir, "service", "Dockerfile"))
	if !strings.Contains(string(df), "server-filesystem@"+fsServerVersion) || strings.Contains(string(df), "__FS_SERVER_VERSION__") || !strings.Contains(string(df), "toolproxy\", \"bridge\"") {
		t.Errorf("Dockerfile:\n%s", df)
	}
	ins, _ := os.ReadFile(filepath.Join(dir, "instruction.md"))
	if !strings.Contains(string(ins), "`/data`") || !strings.Contains(string(ins), "Write the uppercase of file_01.txt") {
		t.Errorf("instruction:\n%s", ins)
	}
	src, _ := os.Stat(filepath.Join("testdata", "upstream", "test_environments", "file_context", "file_01.txt"))
	dst, _ := os.Stat(filepath.Join(dir, "service", "state", "file_01.txt"))
	if src == nil || dst == nil || !src.ModTime().Equal(dst.ModTime()) {
		t.Errorf("state file time not kept: %v vs %v", src, dst)
	}
	if gi, err := os.ReadFile(filepath.Join(root, "benchmarks", "mcpmark-fs", ".gitignore")); err != nil || !strings.Contains(string(gi), "*") {
		t.Errorf(".gitignore = %q, %v", gi, err)
	}
}

func TestImportSelection(t *testing.T) {
	refs := t.TempDir()
	if err := os.WriteFile(filepath.Join(refs, "fs-standard-file-context-uppercase-demo.sh"), []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		extra map[string]string
		ids   []string
		want  []string
	}{
		{name: "easy only", extra: map[string]string{SetDifficulty: "easy"}, want: []string{"fs-easy-file-context-count-demo"}},
		{name: "by upstream id", ids: []string{"filesystem/standard/file_context/uppercase_demo"}, want: []string{"fs-standard-file-context-uppercase-demo"}},
		{name: "by bench id", ids: []string{"fs-easy-file-context-count-demo"}, want: []string{"fs-easy-file-context-count-demo"}},
		{name: "own references", extra: map[string]string{SetReferences: refs}, ids: []string{"fs-standard-file-context-uppercase-demo"}, want: []string{"fs-standard-file-context-uppercase-demo"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, root := fixtureImport(t, tt.extra, tt.ids)
			if !slices.Equal(res.Tasks, tt.want) {
				t.Fatalf("tasks = %v, want %v", res.Tasks, tt.want)
			}
			if tt.extra[SetReferences] != "" {
				b, _ := os.ReadFile(filepath.Join(root, "benchmarks", "mcpmark-fs", "tasks", tt.want[0], "controls", "reference.sh"))
				if !strings.Contains(string(b), "echo mine") {
					t.Fatalf("reference = %q", b)
				}
			}
		})
	}
}

func TestImportExistingAndForce(t *testing.T) {
	root := t.TempDir()
	im := &importer{now: time.Now}
	opts := importers.Options{BenchRoot: root, From: filepath.Join("testdata", "upstream"), SuiteID: "s", Extra: map[string]string{SetRef: fixtureRef}, IDs: []string{"fs-easy-file-context-count-demo"}}
	if _, err := im.Import(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	res, err := im.Import(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	forced := false
	for _, sk := range res.Skipped {
		forced = forced || strings.Contains(sk.Reason, "-force")
	}
	if len(res.Tasks) != 0 || !forced {
		t.Fatalf("second import = %+v", res)
	}
	opts.Force = true
	opts.IDs = nil
	opts.Limit = 1
	res, err = im.Import(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tasks) != 1 {
		t.Fatalf("forced import with limit = %+v", res.Tasks)
	}
	all, err := existingTasks(importers.SuiteDir(root, "s"))
	if err != nil || len(all) != 1 {
		t.Fatalf("suite tasks = %v, %v", all, err)
	}
}

func TestParseSettings(t *testing.T) {
	tests := []struct {
		name    string
		extra   map[string]string
		wantErr string
	}{
		{name: "defaults", extra: map[string]string{}},
		{name: "filesystem family", extra: map[string]string{SetFamilies: "filesystem"}},
		{name: "postgres refused with reason", extra: map[string]string{SetFamilies: "filesystem,postgres"}, wantErr: "PostgreSQL"},
		{name: "unknown family", extra: map[string]string{SetFamilies: "slack"}, wantErr: "unknown family"},
		{name: "bad difficulty", extra: map[string]string{SetDifficulty: "hard"}, wantErr: "want all"},
		{name: "unknown key", extra: map[string]string{"x": "1"}, wantErr: "unknown setting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := parseSettings("/up", tt.extra)
			if tt.wantErr == "" {
				if err != nil || s.stateDir != "/up/test_environments" {
					t.Fatalf("got %+v, %v", s, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestImportErrors(t *testing.T) {
	im := &importer{now: time.Now}
	tests := []struct {
		name string
		opts importers.Options
		want string
	}{
		{name: "missing options", opts: importers.Options{}, want: "required"},
		{name: "not a clone", opts: importers.Options{BenchRoot: t.TempDir(), From: t.TempDir(), SuiteID: "s"}, want: "no tasks/ folder"},
		{name: "no revision", opts: importers.Options{BenchRoot: t.TempDir(), From: filepath.Join("testdata", "upstream"), SuiteID: "s"}, want: "-set ref"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := im.Import(context.Background(), tt.opts); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestInfoAndRegistry(t *testing.T) {
	im, ok := importers.Get(Name)
	if !ok {
		t.Fatal("mcpmark not registered")
	}
	in := im.Info()
	if in.Track != schemas.TrackMCP || in.License != "Apache-2.0" || in.Grading != schemas.GradingVerified || len(in.Needs) == 0 {
		t.Fatalf("info = %+v", in)
	}
}

func TestTaskID(t *testing.T) {
	tests := []struct {
		task upTask
		want string
	}{
		{task: upTask{Difficulty: "standard", Category: "file_context", Name: "uppercase"}, want: "fs-standard-file-context-uppercase"},
		{task: upTask{Difficulty: "easy", Category: "Papers", Name: "papers_counting"}, want: "fs-easy-papers-papers-counting"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := taskID(tt.task); got != tt.want {
				t.Fatalf("got %s", got)
			}
		})
	}
}

// TestImportRealClone imports a real MCPMark clone when MCPMARK_DIR names
// one (with test_environments unzipped in it); it is skipped otherwise.
func TestImportRealClone(t *testing.T) {
	from := os.Getenv("MCPMARK_DIR")
	if from == "" {
		t.Skip("MCPMARK_DIR not set")
	}
	root := t.TempDir()
	res, err := (&importer{now: time.Now}).Import(context.Background(), importers.Options{BenchRoot: root, From: from, SuiteID: "mcpmark-fs"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d tasks, %d skipped", len(res.Tasks), len(res.Skipped))
	for _, s := range res.Skipped {
		t.Logf("skip %s: %s", s.ID, s.Reason)
	}
	for _, id := range res.Tasks {
		s, task, err := suite.LoadTask(filepath.Join(root, "benchmarks"), "mcpmark-fs", id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		for _, p := range suite.LintTask(s, task) {
			if p.Severity == suite.SeverityError {
				t.Errorf("%s: lint error: %+v", id, p)
			}
		}
	}
}
