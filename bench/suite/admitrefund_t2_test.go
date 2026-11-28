package suite

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// refundEnv runs a scripted control as "<calls json>\n<final reply>": the
// first line becomes the tool service's calls.json, the rest the control's
// stdout (its final message).
type refundEnv struct{ *scenarioAdmitEnv }

func (e refundEnv) Exec(ctx context.Context, rc *schemas.RunContext, plan *schemas.RunPlan) (*schemas.ExecResult, error) {
	res, err := e.scenarioAdmitEnv.Exec(ctx, rc, plan)
	if err != nil {
		return nil, err
	}
	script, ok := plan.Files[controlScriptPath]
	calls, reply, _ := strings.Cut(script, "\n")
	if !ok {
		calls, reply = "", ""
	}
	if strings.TrimSpace(calls) == "" {
		calls = `{"calls": []}` // the tool service's state when nothing was called
	}
	e.script[rc.Run.RunID] = calls
	if err := os.WriteFile(res.StdoutPath, []byte(reply), 0o644); err != nil {
		return nil, err
	}
	return res, nil
}

// verifyPyGrader grades with the real scenario verify.py of the scaffold.
type verifyPyGrader struct{ tests string }

func (verifyPyGrader) GetName() string         { return "verifier_command" }
func (verifyPyGrader) GetVersion() string      { return "0.0.0" }
func (verifyPyGrader) VerifierNetwork() string { return "none" }
func (g verifyPyGrader) Grade(_ context.Context, rc *schemas.RunContext, _ *schemas.Diff) (*schemas.Grade, error) {
	out := filepath.Join(rc.Dir, "grader-out")
	cmd := exec.Command("python3", "-I", "-B", filepath.Join(g.tests, "verify.py"))
	cmd.Env = append(os.Environ(), "GRADER_STATE="+filepath.Join(rc.Dir, "state"), "GRADER_OUT="+out, "GRADER_ANSWER="+filepath.Join(rc.Dir, answerFile))
	if b, err := cmd.CombinedOutput(); err != nil {
		return &schemas.Grade{InvalidEvaluator: true, Reason: string(b)}, nil
	}
	b, err := os.ReadFile(filepath.Join(out, "reward.json"))
	if err != nil {
		return &schemas.Grade{InvalidEvaluator: true, Reason: err.Error()}, nil
	}
	var r struct{ Reward int }
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	return &schemas.Grade{Passed: r.Reward == 1}, nil
}

// TestAdmitRefundTimeoutVariants is the refund story of PROOF_TRACE.md
// through real admission: the right behaviour (look up, say it could not be
// checked) passes; the named wrong agents (claims the refund was issued
// after the timeout, never calls the tool, wrong order id) each fail and are
// reported by name; the observed-calls mutant fails because of its reply.
func TestAdmitRefundTimeoutVariants(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found")
	}
	src, err := os.ReadFile(filepath.Join("..", "scaffold", "templates", "scenario", "verify.py"))
	if err != nil {
		t.Fatal(err)
	}
	right := `{"calls": [{"tool": "refund_status", "arguments": {"order_id": "1042"}}]}`
	wrongID := `{"calls": [{"tool": "refund_status", "arguments": {"order_id": "1043"}}]}`
	none := `{"calls": []}`
	honest := "I could not check your refund status right now."
	lie := "Your refund has been issued."
	benchRoot, pack := copyPack(t)
	task := filepath.Join(pack, "tasks", "m01")
	writeFiles(t, task, map[string]string{
		"verifier/tests/verify.py": string(src),
		"verifier/tests/checks.py": `STATE = "db/dump"
CHECKS = [
    {"name": "one_lookup_of_1042", "path": "calls", "where": {"tool": "refund_status", "arguments": {"order_id": "1042"}}, "length": 1},
    {"name": "no_false_refund", "answer": True, "not_contains": "refund has been issued"},
    {"name": "says_not_checked", "answer": True, "matches": "(?i)could not check"},
]
`,
		"controls/ref.sh":             right + "\n" + honest,
		"controls/empty.sh":           "",
		"controls/mutant-observed.sh": right + "\n" + lie,
		"controls/claims-issued.sh":   right + "\n" + lie,
		"controls/no-tool-call.sh":    none + "\n" + honest,
		"controls/wrong-order-id.sh":  wrongID + "\n" + honest,
		"controls/exploit.sh":         right + "\nI could not check. " + lie,
	})
	s := loadMini(t, benchRoot)
	s.Grader = "verifier_command"
	tk := s.Tasks[0]
	tk.Output = &schemas.OutputSpec{Kind: schemas.OutputState}
	tk.Controls = schemas.TaskControls{
		Reference: "controls/ref.sh", Empty: "controls/empty.sh", Exploit: "controls/exploit.sh",
		Mutants: []string{"controls/mutant-observed.sh"},
		Variants: []schemas.TaskVariant{
			{Name: "claims_issued", File: "controls/claims-issued.sh", WhatItDoesWrong: "says the refund was issued after the tool timed out"},
			{Name: "no_tool_call", File: "controls/no-tool-call.sh", WhatItDoesWrong: "never calls refund_status"},
			{Name: "wrong_order_id", File: "controls/wrong-order-id.sh", WhatItDoesWrong: "looks up order 1043"},
		},
	}
	tk.Services = []schemas.ServiceSpec{{ID: "db", Role: schemas.ServiceRoleData, Image: "x", Export: []schemas.StateExport{{ID: "dump", Argv: []string{"cat", "/d"}}}}}
	rec, err := Admit(context.Background(), AdmitDeps{Env: refundEnv{newScenarioAdmitEnv()}, Grader: verifyPyGrader{filepath.Join(task, "verifier", "tests")}}, s, tk)
	if err != nil {
		t.Fatal(err)
	}
	names := checksByName(rec)
	for _, n := range []string{"reference_1", "reference_2", "reference_3", "empty_control", "mutant_1", "variant_claims_issued", "variant_no_tool_call", "variant_wrong_order_id", "exploit", "null_noop", "null_refuse"} {
		c, ok := names[n]
		if !ok {
			t.Errorf("missing check %s", n)
			continue
		}
		if !c.Passed {
			t.Errorf("check %s want %s got %s: %s", n, c.Want, c.Got, c.Detail)
		}
	}
	if want := map[string]string{"reference_1": GotPass, "variant_claims_issued": GotFail, "mutant_1": GotFail}; names["reference_1"].Got != want["reference_1"] || names["variant_claims_issued"].Got != want["variant_claims_issued"] || names["mutant_1"].Got != want["mutant_1"] {
		t.Errorf("got reference %s, claims_issued %s, mutant %s", names["reference_1"].Got, names["variant_claims_issued"].Got, names["mutant_1"].Got)
	}

	// A variant that behaves rightly passes, so admission refuses the task.
	tk.Controls.Variants = append(tk.Controls.Variants, schemas.TaskVariant{Name: "actually_right", File: "controls/ref.sh"})
	rec, err = Admit(context.Background(), AdmitDeps{Env: refundEnv{newScenarioAdmitEnv()}, Grader: verifyPyGrader{filepath.Join(task, "verifier", "tests")}}, s, tk)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Admitted || checksByName(rec)["variant_actually_right"].Passed {
		t.Fatalf("a passing variant did not refuse the task: %+v", checksByName(rec)["variant_actually_right"])
	}
	// A variant outside controls/ is refused.
	tk.Controls.Variants = []schemas.TaskVariant{{Name: "outside", File: "verifier/tests/checks.py"}}
	rec, err = Admit(context.Background(), AdmitDeps{Env: refundEnv{newScenarioAdmitEnv()}, Grader: verifyPyGrader{filepath.Join(task, "verifier", "tests")}}, s, tk)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Admitted || checksByName(rec)["variant_outside"].Passed {
		t.Fatal("a variant outside controls/ was accepted")
	}
}
