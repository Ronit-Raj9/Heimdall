package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestRunProblemFromJobProgress: the run job's last error is the
// comparison's problem only while nothing sealed (N151).
func TestRunProblemFromJobProgress(t *testing.T) {
	raw, _ := json.Marshal(expRunProgress{ExpID: "e_x", Errors: 2, LastError: "every worker refused this run 3 times: x"})
	if !strings.Contains(string(raw), `"last_error":"every worker refused this run 3 times: x"`) {
		t.Fatalf("progress %s has no last_error", raw)
	}
	problem, jobID := runProblemOf(&jobs.Job{ID: "job_1", Progress: raw})
	if problem != "every worker refused this run 3 times: x" || jobID != "job_1" {
		t.Fatalf("problem %q job %q", problem, jobID)
	}
	raw, _ = json.Marshal(expRunProgress{ExpID: "e_x", Errors: 1, Sealed: 1, LastError: "slot s1: boom"})
	if problem, jobID := runProblemOf(&jobs.Job{ID: "job_2", Progress: raw}); problem != "" || jobID != "job_2" {
		t.Fatalf("a sealed run: problem %q job %q", problem, jobID)
	}
	if problem, _ := runProblemOf(&jobs.Job{ID: "job_3"}); problem != "" {
		t.Fatalf("no progress: problem %q", problem)
	}
	if problem, jobID := (&launcher{}).RunProblem("e_x"); problem != "" || jobID != "" {
		t.Fatalf("no job runner: %q %q", problem, jobID)
	}
}

// TestReadinessFailsWithRefusalReason: when no readiness run could start,
// the job fails with the refusal reason, not "N tasks not admitted" (N151).
func TestReadinessFailsWithRefusalReason(t *testing.T) {
	dir, base, ref := initQuickRepo(t)
	id := cutMinedTask(t, dir, base, ref)
	env, l := hostedBridgeEnv(t, schemas.OutcomePass, schemas.OutcomeFail)
	bridgeSealedRuns = func(context.Context, jobEnv, string) ([]bridgeRun, error) { return nil, nil }
	l.activeFor = 3
	l.events = make(chan schemas.LiveEvent, 2)
	l.events <- schemas.LiveEvent{Kind: "error", SlotID: "s1", Message: "every worker refused this run 3 times: The worker's copy of your project does not have this benchmark yet."}
	raw, _ := json.Marshal(hostedSuiteAdmitParams{Suite: "lib", Task: id})
	log := &bridgeTestLog{}
	_, err := runHostedSuiteAdmit(context.Background(), env, &jobs.Job{Params: raw}, log, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "no readiness run could start: ") || !strings.Contains(err.Error(), "does not have this benchmark yet") {
		t.Fatalf("err = %v", err)
	}
	if lines := strings.Join(log.lines, "\n"); !strings.Contains(lines, "run in comparison e_hostedtest; open it with Open the comparison at the top of this page") {
		t.Fatalf("log does not name the comparison: %s", lines)
	}
}
