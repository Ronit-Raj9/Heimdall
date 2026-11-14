package suite

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

var fixedNow = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func loadMini(t *testing.T, benchRoot string) *schemas.Suite {
	t.Helper()
	s, err := newLocalLoader(benchRoot).Load(context.Background(), "mini")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLockStable(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	s := loadMini(t, benchRoot)
	a, err := Lock(s, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Lock(loadMini(t, benchRoot), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	ja, err := WriteLock(filepath.Join(t.TempDir(), "a.lock"), a)
	if err != nil {
		t.Fatal(err)
	}
	jb, err := WriteLock(filepath.Join(t.TempDir(), "b.lock"), b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ja, jb) {
		t.Fatalf("lock not stable:\n%s\n%s", ja, jb)
	}
	if !bytes.HasSuffix(ja, []byte("}\n")) || !bytes.Contains(ja, []byte("\n  \"suite\": \"mini\"")) {
		t.Fatalf("lock is not indented JSON with a trailing newline:\n%s", ja)
	}
	tl := a.Tasks["m01"]
	repoHash, err := utils.TreeHash(filepath.Join(packRoot, "tasks", "m01", "repo"))
	if err != nil {
		t.Fatal(err)
	}
	if tl.RepoTreeSHA256 != repoHash || tl.Status != schemas.TaskDraft || tl.Version != "1.0.0" {
		t.Fatalf("task lock: %+v", tl)
	}
	if a.Probes["stale-notes"].SHA256 == "" || a.BaseImage.Tag != "bench/mini-base:1" {
		t.Fatalf("lock: %+v", a)
	}
}

func TestLockTaskWithoutNotes(t *testing.T) {
	benchRoot, _ := copyPack(t)
	s := loadMini(t, benchRoot)
	s.Tasks[0].Notes = schemas.TaskNotes{}
	if err := os.RemoveAll(filepath.Join(s.Tasks[0].Dir, "fixtures")); err != nil {
		t.Fatal(err)
	}
	l, err := Lock(s, fixedNow)
	if err != nil {
		t.Fatalf("lock a task without notes: %v", err)
	}
	empty, err := setHash(s.Tasks[0].Dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.Tasks["m01"].FixturesSHA256; got != empty {
		t.Fatalf("fixtures hash = %s, want the empty set %s", got, empty)
	}
}

func TestSetHashMatchesTreeHash(t *testing.T) {
	_, packRoot := copyPack(t)
	dir := filepath.Join(packRoot, "tasks", "m01")
	want, err := utils.TreeHash(filepath.Join(dir, "controls"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := setHash(filepath.Join(dir, "controls"), []string{"mutant1.diff", "empty.diff", "exploit.sh", "empty.diff"})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("setHash = %s, want TreeHash %s", got, want)
	}
}

func TestLockChanges(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		old, new  string
		field     func(schemas.TaskLock) string
		wantLevel ChangeLevel
	}{
		{"verifier test", "tasks/m01/verifier/tests/test_hidden.py", "== -3", "== -3  # exact", func(l schemas.TaskLock) string { return l.VerifierSHA256 }, LevelMinor},
		{"mutant", "tasks/m01/controls/mutant1.diff", "abs(a + b)", "abs(a) - abs(b)", func(l schemas.TaskLock) string { return l.ControlsSHA256 }, LevelMinor},
		{"instruction", "tasks/m01/instruction.md", "too.", "too, and zero.", func(l schemas.TaskLock) string { return l.InstructionSHA256 }, LevelMajor},
		{"repo", "tasks/m01/repo/calc/core.py", "def add", "def add  ", func(l schemas.TaskLock) string { return l.RepoTreeSHA256 }, LevelMajor},
		{"stale notes", "tasks/m01/fixtures/notes.stale.md", "by design", "always", func(l schemas.TaskLock) string { return l.FixturesSHA256 }, LevelMajor},
		{"version only", "tasks/m01/task.yaml", "version: 1.0.0", "version: 1.0.1", func(l schemas.TaskLock) string { return l.Version }, LevelPatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			benchRoot, packRoot := copyPack(t)
			s := loadMini(t, benchRoot)
			before, err := Lock(s, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			if err := RecordAdmission(before, "m01", &schemas.AdmissionRecord{Admitted: true, At: fixedNow}); err != nil {
				t.Fatal(err)
			}
			if _, err := WriteLock(filepath.Join(packRoot, LockFile), before); err != nil {
				t.Fatal(err)
			}
			editFile(t, filepath.Join(packRoot, tc.file), tc.old, tc.new)
			s2 := loadMini(t, benchRoot)
			after, err := Lock(s2, fixedNow.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			if tc.field(before.Tasks["m01"]) == tc.field(after.Tasks["m01"]) {
				t.Fatalf("%s did not change the lock", tc.file)
			}
			changes := Diff(before, after)
			if len(changes) != 1 || changes[0].ID != "m01" || changes[0].Level != tc.wantLevel {
				t.Fatalf("Diff = %+v, want one m01 change at %s", changes, tc.wantLevel)
			}
			got := after.Tasks["m01"]
			if tc.wantLevel == LevelPatch {
				if got.Status != schemas.TaskAdmitted || got.Admission == nil {
					t.Fatalf("metadata-only change dropped the admission: %+v", got)
				}
			} else if got.Status != schemas.TaskDraft || got.Admission != nil {
				t.Fatalf("content change kept the admission: %+v", got)
			}
		})
	}
}

func TestLockKeepsCreatedAtWhenUnchanged(t *testing.T) {
	benchRoot, packRoot := copyPack(t)
	first, err := Lock(loadMini(t, benchRoot), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	data1, err := WriteLock(filepath.Join(packRoot, LockFile), first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Lock(loadMini(t, benchRoot), fixedNow.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	data2, err := WriteLock(filepath.Join(packRoot, LockFile), second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data1, data2) {
		t.Fatalf("re-locking an unchanged pack changed the lock:\n%s\n%s", data1, data2)
	}
	var back schemas.SuiteLock
	if err := json.Unmarshal(data2, &back); err != nil {
		t.Fatal(err)
	}
	if !back.CreatedAt.Equal(fixedNow) {
		t.Fatalf("created_at = %s", back.CreatedAt)
	}
	if _, err := os.Stat(filepath.Join(packRoot, LockFile)); err != nil {
		t.Fatal(err)
	}
}

func TestDiff(t *testing.T) {
	task := schemas.TaskLock{Version: "1.0.0", InstructionSHA256: "i", RepoTreeSHA256: "r", VerifierSHA256: "v", ControlsSHA256: "c", FixturesSHA256: "f", Status: schemas.TaskDraft}
	with := func(f func(*schemas.TaskLock)) schemas.TaskLock {
		c := task
		f(&c)
		return c
	}
	base := &schemas.SuiteLock{
		Suite:     "mini",
		BaseImage: schemas.ImageSpec{Tag: "img:1"},
		Tasks:     map[string]schemas.TaskLock{"t1": task, "t2": task},
		Probes:    map[string]schemas.ProbeLock{"p1": {Version: "1.0.0", SHA256: "x"}},
	}
	tests := []struct {
		name string
		b    *schemas.SuiteLock
		want []Change
	}{
		{"identical", base, nil},
		{"version only", &schemas.SuiteLock{Suite: "mini", BaseImage: base.BaseImage,
			Tasks:  map[string]schemas.TaskLock{"t1": with(func(l *schemas.TaskLock) { l.Version = "1.0.1" }), "t2": task},
			Probes: base.Probes},
			[]Change{{Scope: ScopeTask, ID: "t1", Level: LevelPatch, Action: ActionReuse, Fields: []string{"version"}}}},
		{"verifier and controls", &schemas.SuiteLock{Suite: "mini", BaseImage: base.BaseImage,
			Tasks:  map[string]schemas.TaskLock{"t1": with(func(l *schemas.TaskLock) { l.VerifierSHA256, l.ControlsSHA256 = "v2", "c2" }), "t2": task},
			Probes: base.Probes},
			[]Change{{Scope: ScopeTask, ID: "t1", Level: LevelMinor, Action: ActionRegrade, Fields: []string{"verifier_sha256", "controls_sha256"}}}},
		{"repo beats verifier", &schemas.SuiteLock{Suite: "mini", BaseImage: base.BaseImage,
			Tasks:  map[string]schemas.TaskLock{"t1": with(func(l *schemas.TaskLock) { l.VerifierSHA256, l.RepoTreeSHA256 = "v2", "r2" }), "t2": task},
			Probes: base.Probes},
			[]Change{{Scope: ScopeTask, ID: "t1", Level: LevelMajor, Action: ActionRerun, Fields: []string{"repo_tree_sha256", "verifier_sha256"}}}},
		{"added removed and probe", &schemas.SuiteLock{Suite: "mini", BaseImage: base.BaseImage,
			Tasks:  map[string]schemas.TaskLock{"t1": task, "t3": task},
			Probes: map[string]schemas.ProbeLock{"p1": {Version: "1.0.0", SHA256: "y"}}},
			[]Change{
				{Scope: ScopeTask, ID: "t2", Level: LevelMinor, Action: ActionRemoved, Fields: []string{"removed"}},
				{Scope: ScopeTask, ID: "t3", Level: LevelMinor, Action: ActionAdded, Fields: []string{"added"}},
				{Scope: ScopeProbe, ID: "p1", Level: LevelMajor, Action: ActionRerun, Fields: []string{"sha256"}},
			}},
		{"base image", &schemas.SuiteLock{Suite: "mini", BaseImage: schemas.ImageSpec{Tag: "img:2"}, Tasks: base.Tasks, Probes: base.Probes},
			[]Change{{Scope: ScopeSuite, ID: "mini", Level: LevelMajor, Action: ActionRerun, Fields: []string{"base_image"}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Diff(base, tc.b)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Diff =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
	if got := Diff(nil, nil); len(got) != 0 {
		t.Fatalf("Diff(nil, nil) = %+v", got)
	}
}
