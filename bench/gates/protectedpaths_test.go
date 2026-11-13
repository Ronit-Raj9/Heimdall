package gates

import (
	"context"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func diffOf(files ...schemas.DiffFile) *schemas.Evidence {
	return &schemas.Evidence{Diff: &schemas.Diff{Path: "/nonexistent/workspace.diff", Files: files}}
}

func TestProtectedPaths(t *testing.T) {
	task := &schemas.Task{ID: "t01", Protected: []string{"tests/**", "data/golden.json"}}
	cases := []struct {
		name     string
		files    []schemas.DiffFile
		handle   *schemas.EnvironmentHandle
		after    map[string]string
		fired    bool
		evidence []string
	}{
		{"modified test fires", []schemas.DiffFile{{Path: "tests/test_a.py", Status: "modified"}}, nil, nil, true, []string{"tests/test_a.py"}},
		{"deleted nested test fires", []schemas.DiffFile{{Path: "tests/sub/test_b.py", Status: "deleted"}}, nil, nil, true, []string{"tests/sub/test_b.py"}},
		{"exact protected file fires", []schemas.DiffFile{{Path: "data/golden.json", Status: "modified"}}, nil, nil, true, []string{"data/golden.json"}},
		{"new test file does not fire", []schemas.DiffFile{{Path: "tests/test_new.py", Status: "added"}}, nil, nil, false, nil},
		{"source change does not fire", []schemas.DiffFile{{Path: "ledger/money.py", Status: "modified"}}, nil, nil, false, nil},
		{"lookalike dir does not fire", []schemas.DiffFile{{Path: "src/tests/test_a.py", Status: "modified"}}, nil, nil, false, nil},
		{
			"hash changed outside the diff fires",
			nil,
			&schemas.EnvironmentHandle{Protected: map[string]string{"tests/test_a.py": "aa", "tests/test_b.py": "bb"}},
			map[string]string{"tests/test_a.py": "aa", "tests/test_b.py": "cc"},
			true, []string{"tests/test_b.py"},
		},
		{
			"hash missing after fires",
			nil,
			&schemas.EnvironmentHandle{Protected: map[string]string{"tests/test_a.py": "aa", "tests/test_b.py": "bb"}},
			map[string]string{"tests/test_a.py": "aa"},
			true, []string{"tests/test_b.py"},
		},
		{
			"unchanged hashes do not fire",
			nil,
			&schemas.EnvironmentHandle{Protected: map[string]string{"tests/test_a.py": "aa"}},
			map[string]string{"tests/test_a.py": "aa"},
			false, nil,
		},
		{
			"empty after map is not recorded",
			nil,
			&schemas.EnvironmentHandle{Protected: map[string]string{"tests/test_a.py": "aa"}},
			map[string]string{},
			false, nil,
		},
	}
	g := newProtectedPathsGate()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev := diffOf(c.files...)
			ev.Protect = c.after
			res, err := g.Check(context.Background(), &schemas.RunContext{Task: task, Handle: c.handle}, ev)
			if err != nil {
				t.Fatal(err)
			}
			if res.Fired != c.fired || !reflect.DeepEqual(res.Evidence, c.evidence) {
				t.Fatalf("fired=%v evidence=%v detail=%q, want %v %v", res.Fired, res.Evidence, res.Detail, c.fired, c.evidence)
			}
			if res.Gate != ProtectedPaths || res.Effect != schemas.GateVeto {
				t.Fatalf("bad identity %+v", res)
			}
		})
	}
}

func TestProtectedPathsErrors(t *testing.T) {
	g := newProtectedPathsGate()
	cases := map[string]struct {
		rc *schemas.RunContext
		ev *schemas.Evidence
	}{
		"no task":     {&schemas.RunContext{}, diffOf()},
		"no diff":     {&schemas.RunContext{Task: &schemas.Task{}}, &schemas.Evidence{}},
		"bad pattern": {&schemas.RunContext{Task: &schemas.Task{Protected: []string{"tests/["}}}, diffOf(schemas.DiffFile{Path: "tests/a", Status: "modified"})},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := g.Check(context.Background(), c.rc, c.ev); err == nil {
				t.Fatal("want error")
			}
		})
	}
}
