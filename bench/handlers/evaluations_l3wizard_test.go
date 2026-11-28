package handlers

import (
	"os"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// TestWizardModelSwitchHeldOutBody posts the body the evaluate wizard sends
// for "Find the best model" with "Held-out tasks only" (the same fixture
// ui/app/evaluate/views/wizard.ufl3.test.ts checks draftToSpec against), and
// the refusal of a repeated model the wizard also blocks.
func TestWizardModelSwitchHeldOutBody(t *testing.T) {
	body, err := os.ReadFile("testdata/l3_wizard_model_switch.json")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := evalServer(t, Services{Launcher: &evalLauncher{}, Suites: evalSuites()})
	got := evalCall(r, "POST", APIPrefix+"/evaluations", string(body))
	if c := got.Response.StatusCode(); c < 200 || c > 299 {
		t.Fatalf("wizard model switch body: %d %s", c, got.Response.Body())
	}
	ev := decodeEval(t, got)
	if ev.Spec.Mode != schemas.EvaluationModeModelSwitch || len(ev.Spec.Arms) != 3 || ev.Spec.Split != schemas.SplitHeldOut {
		t.Fatalf("saved spec %+v", ev.Spec)
	}
	dup := strings.Replace(string(body), `"m3"`, `"m2"`, 1)
	got = evalCall(r, "POST", APIPrefix+"/evaluations", dup)
	if got.Response.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("repeated model accepted: %d %s", got.Response.StatusCode(), got.Response.Body())
	}
}
