package importers_test

// Lane Y07: the judged wrong-answer rule (suite.lintJudgedWrong) is an
// error only for tasks written from the author's own rows. The library
// benchmarks below must import and lint with no error under it, so their
// admissions keep working.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/maximhq/bifrost/bench/graders" // sets suite.JudgeSkip, as in the server
	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/importers/agentvista"
	"github.com/maximhq/bifrost/bench/importers/bigfinancebench"
	"github.com/maximhq/bifrost/bench/importers/locomo"
	"github.com/maximhq/bifrost/bench/importers/longmemeval"
	"github.com/maximhq/bifrost/bench/importers/msibench"
	"github.com/maximhq/bifrost/bench/suite"
)

const judgedWrongMsg = "no wrong answer reaches the judge"

// lintLibrary loads suite id under benchmarksDir and fails on any lint error
// (and on the judged wrong-answer rule at any severity, so library packs stay
// quiet). Other errors are reported with the importer name.
func lintLibrary(t *testing.T, name, benchmarksDir, id string) {
	t.Helper()
	if suite.JudgeSkip == nil {
		t.Fatal("graders did not set suite.JudgeSkip")
	}
	s, err := suite.Builtin(benchmarksDir)[0].Load(context.Background(), id)
	if err != nil {
		t.Fatalf("%s: load %s: %v", name, id, err)
	}
	for _, p := range suite.Lint(s) {
		if strings.Contains(p.Message, judgedWrongMsg) {
			t.Errorf("%s: %s %s: %s", name, p.Severity, p.Path, p.Message)
		} else if p.Severity == suite.SeverityError && !strings.Contains(p.Message, "pin-me") {
			t.Errorf("%s: lint error %s: %s", name, p.Path, p.Message)
		}
	}
}

func TestLibrarySuitesLintCleanUnderJudgedWrongRule(t *testing.T) {
	for _, c := range []struct {
		name, from, suite string
	}{
		{msibench.Name, "msibench/testdata/release", "msi-bench-v2"},
		{bigfinancebench.Name, "bigfinancebench/testdata", "bigfinancebench"},
		{agentvista.Name, "agentvista/testdata/fixture", "agentvista"},
		{longmemeval.Name, "longmemeval/testdata", "longmemeval"},
		{locomo.Name, "locomo/testdata", "locomo"},
	} {
		t.Run(c.suite, func(t *testing.T) {
			im, ok := importers.Get(c.name)
			if !ok {
				t.Fatalf("importer %s not registered", c.name)
			}
			from, err := filepath.Abs(c.from)
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(t.TempDir(), "datasets", "project")
			dir := filepath.Join(root, "benchmarks")
			if _, err := im.Import(context.Background(), importers.Options{BenchRoot: root, BenchmarksDir: dir, From: from, SuiteID: c.suite}); err != nil {
				t.Fatalf("import: %v", err)
			}
			lintLibrary(t, c.name, dir, c.suite)
		})
	}
}
