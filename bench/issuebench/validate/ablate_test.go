package validate

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// ablRunner answers a gold run: "t::fixed" fails when the ablation lacks
// the hunk that adds "+needed", "t::old" breaks when it lacks "+keep".
type ablRunner struct{ runs int }

func (r *ablRunner) Run(_ context.Context, _ *issuebench.EnvSpec, c *issuebench.Candidate, run Run) ([]byte, error) {
	r.runs++
	if run.State != StateGold {
		return nil, fmt.Errorf("unexpected state %s", run.State)
	}
	fixed, old := "pass", "pass"
	if !strings.Contains(c.FixPatch, "+needed") {
		fixed = "fail"
	}
	if !strings.Contains(c.FixPatch, "+keep") {
		old = "fail"
	}
	return []byte("t::fixed " + fixed + "\nt::old " + old + "\n"), nil
}

func ablPatch(hunks ...string) string {
	var b strings.Builder
	b.WriteString("diff --git a/src/m.py b/src/m.py\n--- a/src/m.py\n+++ b/src/m.py\n")
	for i, h := range hunks {
		fmt.Fprintf(&b, "@@ -%d,1 +%d,1 @@\n-x\n%s\n", i*10+1, i*10+1, h)
	}
	b.WriteString("diff --git a/CHANGELOG.rst b/CHANGELOG.rst\n--- a/CHANGELOG.rst\n+++ b/CHANGELOG.rst\n@@ -1,1 +1,2 @@\n x\n+fixed it\n")
	return b.String()
}

// The worker keeps an ablation only when the tests see it is wrong: one
// without the needed hunk (a FAIL_TO_PASS test fails) or the kept hunk (a
// PASS_TO_PASS test breaks) is kept; one without a refactor hunk passes and
// is dropped. The changelog hunk is never ablated.
func TestVerifyAblationsKeptAndDropped(t *testing.T) {
	c := issuebench.Candidate{TestFiles: []string{"tests/test_m.py"}, FixPatch: ablPatch("+needed", "+keep", "+refactor\n+refactor2")}
	v := &issuebench.Validation{FailToPass: []string{"t::fixed"}, PassToPass: []string{"t::old"}}
	r := &ablRunner{}
	rep, err := VerifyAblations(context.Background(), r, &issuebench.EnvSpec{}, c, v, fakeLang{}, MaxVerifiedAblations)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Tried != 3 || rep.Kept != 2 || rep.Dropped != 1 || len(rep.Patches) != 2 || r.runs != 3 {
		t.Fatalf("report %+v, runs %d", rep, r.runs)
	}
	for _, p := range rep.Patches {
		if !strings.Contains(p, "+refactor") || !strings.Contains(p, "CHANGELOG.rst") {
			t.Fatalf("a kept ablation drops the refactor or the changelog:\n%s", p)
		}
	}
}

// At most the cap runs, the hunks with the most changed lines first.
func TestVerifyAblationsCapped(t *testing.T) {
	c := issuebench.Candidate{TestFiles: []string{"tests/test_m.py"}, FixPatch: ablPatch("+a", "+b\n+b2\n+b3", "+c\n+c2", "+d", "+e", "+needed\n+n2\n+n3\n+n4")}
	v := &issuebench.Validation{FailToPass: []string{"t::fixed"}}
	r := &ablRunner{}
	rep, err := VerifyAblations(context.Background(), r, &issuebench.EnvSpec{}, c, v, fakeLang{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Tried != 2 || r.runs != 2 {
		t.Fatalf("tried %d runs %d, want 2", rep.Tried, r.runs)
	}
	// The largest hunk ("needed", 5 changed lines) is dropped first: that
	// ablation fails the tests and is kept; the next ("b", 4) passes.
	if rep.Kept != 1 || rep.Dropped != 1 || strings.Contains(rep.Patches[0], "+needed") {
		t.Fatalf("report %+v", rep)
	}
}

// No FAIL_TO_PASS (or nothing to ablate): nothing runs.
func TestVerifyAblationsNothingToRun(t *testing.T) {
	r := &ablRunner{}
	rep, _ := VerifyAblations(context.Background(), r, &issuebench.EnvSpec{}, issuebench.Candidate{FixPatch: ablPatch("+needed")}, &issuebench.Validation{FailToPass: []string{"t::fixed"}}, fakeLang{}, 4)
	if rep.Tried != 0 || r.runs != 0 {
		t.Fatalf("report %+v runs %d", rep, r.runs)
	}
}

// Hosted: the check adds no unverified ablation; only the model's near
// misses come from it (the worker's verified ones are added on the server).
func TestCheckerNoAblations(t *testing.T) {
	m := &scriptModel{replies: []string{
		"{\"leak\": false, \"leak_evidence\": \"\", \"clarity\": 0, \"clarity_reason\": \"clear\"}",
		wrongJSON(wrongDiffA, wrongDiffB),
	}}
	v, _ := validate(t, standard, Options{Checks: &Checker{Model: m, NoAblations: true}})
	if v.Stop != "" || len(v.Checks.WrongPatches) != 2 {
		t.Fatalf("stop %q wrong %d, want the 2 model patches only", v.Stop, len(v.Checks.WrongPatches))
	}
}
