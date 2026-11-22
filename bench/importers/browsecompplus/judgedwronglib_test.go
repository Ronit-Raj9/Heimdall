package browsecompplus

// Lane Y07: browsecomp-plus judged imports lint with no judged
// wrong-answer problem (see importers/judgedwronglib_test.go).

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/maximhq/bifrost/bench/graders" // sets suite.JudgeSkip
	"github.com/maximhq/bifrost/bench/suite"
)

func TestJudgedImportLintCleanUnderJudgedWrongRule(t *testing.T) {
	if suite.JudgeSkip == nil {
		t.Fatal("graders did not set suite.JudgeSkip")
	}
	from := writeFixture(t, t.TempDir(), true)
	root := filepath.Join(t.TempDir(), "datasets", "project")
	mustImport(t, root, from, map[string]string{setGrading: gradingJudged}, false)
	s, err := suite.Builtin(filepath.Join(root, "benchmarks"))[0].Load(context.Background(), "bcp-fx")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range suite.Lint(s) {
		if strings.Contains(p.Message, "no wrong answer reaches the judge") || (p.Severity == suite.SeverityError && !strings.Contains(p.Message, "pin-me")) {
			t.Errorf("%s %s: %s", p.Severity, p.Path, p.Message)
		}
	}
}
