package gates

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRegrade(t *testing.T) {
	suite := &schemas.Suite{ID: "coding-core", Verifier: schemas.SuiteVerifier{
		Restore: []string{"tests", "conftest.py", "pytest.ini", "pyproject.toml", "setup.cfg", "tox.ini", "sitecustomize.py"},
	}}
	cases := []struct {
		name  string
		files []schemas.DiffFile
		fired bool
	}{
		{"edited test fires", []schemas.DiffFile{{Path: "tests/test_a.py", Status: "modified"}}, true},
		{"deleted root conftest fires", []schemas.DiffFile{{Path: "conftest.py", Status: "deleted"}}, true},
		{"edited pyproject fires", []schemas.DiffFile{{Path: "pyproject.toml", Status: "modified"}}, true},
		{"new test file ignored", []schemas.DiffFile{{Path: "tests/test_new.py", Status: "added"}}, false},
		{"new root conftest ignored", []schemas.DiffFile{{Path: "conftest.py", Status: "added"}}, false},
		{"nested conftest not under restore", []schemas.DiffFile{{Path: "pkg/conftest.py", Status: "modified"}}, false},
		{"lookalike dir ignored", []schemas.DiffFile{{Path: "tests_extra/a.py", Status: "modified"}}, false},
		{"source edit ignored", []schemas.DiffFile{{Path: "ledger/money.py", Status: "modified"}}, false},
	}
	g := newRegradeGate()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := g.Check(context.Background(), &schemas.RunContext{Suite: suite}, diffOf(c.files...))
			if err != nil {
				t.Fatal(err)
			}
			if res.Fired != c.fired {
				t.Fatalf("fired=%v detail=%q, want %v", res.Fired, res.Detail, c.fired)
			}
		})
	}
}

func TestRegradeErrors(t *testing.T) {
	g := newRegradeGate()
	if _, err := g.Check(context.Background(), &schemas.RunContext{}, diffOf()); err == nil {
		t.Fatal("want error without a suite")
	}
	if _, err := g.Check(context.Background(), &schemas.RunContext{Suite: &schemas.Suite{}}, &schemas.Evidence{}); err == nil {
		t.Fatal("want error without a diff")
	}
}
