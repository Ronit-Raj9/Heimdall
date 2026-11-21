package release

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func evalRequest(override string, blocked ...string) *schemas.ReleaseRequest {
	return &schemas.ReleaseRequest{
		EvaluationID: "ev_20261120_101010_abc", Approver: "ana", Override: override,
		Evaluation: &schemas.ReleaseEvaluation{
			EvaluationID: "ev_20261120_101010_abc", AgentID: "codex", Version: 4, ReplacesVersion: 3,
			CandidateArm: "B", Children: []schemas.ReleaseChildRef{{Benchmark: "hd-harbor-tb2", ExpID: testExp}},
			Decision: schemas.DecisionImproved, Blocked: blocked, Gate: schemas.GatePass,
			Summary: &schemas.ReleaseSummary{Candidate: schemas.ReleaseSide{Label: "B", Agent: "codex", Version: 4, Score: 0.6}},
		},
	}
}

func TestCreateEvaluationRelease(t *testing.T) {
	// A hold verdict on one benchmark does not block a release of the
	// whole evaluation: only a regression, a policy block or the gate does.
	f := newFixture(t, schemas.ActionHold, "")
	rec, err := f.log.Create(context.Background(), evalRequest(""))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rec.AgentID != "codex" || rec.Version != 4 || rec.ReplacesVersion != 3 || rec.EvaluationID != "ev_20261120_101010_abc" {
		t.Errorf("version fields = %+v", rec)
	}
	if rec.Action != schemas.ActionShip || rec.Arm != "B" || rec.AgentConfigID != "agent_b" || rec.ExpID != testExp {
		t.Errorf("record = %+v", rec)
	}
	if len(rec.Children) != 1 || rec.Children[0].Runs != 4 || rec.Children[0].Benchmark != "hd-harbor-tb2" || rec.Children[0].Action != schemas.ActionHold {
		t.Errorf("children = %+v", rec.Children)
	}
	if rec.Summary == nil || rec.Summary.Candidate.Score != 0.6 {
		t.Errorf("summary = %+v", rec.Summary)
	}
	if err := f.log.VerifyChain(); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	b, err := f.log.Evidence(context.Background(), rec.ReleaseID)
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, zf := range zr.File {
		names[zf.Name] = true
	}
	for _, want := range []string{"README.txt", "release.json", "experiments/e_rel/manifest.json", "experiments/e_rel/analysis/verdict.json",
		"experiments/e_rel/seals.log", "experiments/e_rel/runs/r_t01_b/seal.json"} {
		if !names[want] {
			t.Errorf("pack lacks %s", want)
		}
	}
	got, err := f.log.Get(context.Background(), rec.ReleaseID)
	if err != nil || got.Version != 4 || len(got.Children) != 1 {
		t.Errorf("Get = %+v, %v", got, err)
	}
}

func TestCreateEvaluationReleaseBlocks(t *testing.T) {
	tests := []struct {
		name     string
		policy   schemas.PolicyDecision
		arm      string
		blocked  []string
		override string
		refused  string
	}{
		{name: "gate failed", blocked: []string{"the gate failed: coding fell 9 points"}, refused: "gate failed"},
		{name: "gate failed with override", blocked: []string{"the gate failed"}, override: "accepted for the hotfix"},
		{name: "policy blocks", policy: schemas.PolicyBlock, refused: "hd-harbor-tb2: policy team decision is block"},
		{name: "candidate is the worse arm", arm: "A", refused: "the verdict says A got worse"},
		{name: "override with nothing to override", override: "because", refused: "nothing to override"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, schemas.ActionShip, tt.policy)
			req := evalRequest(tt.override, tt.blocked...)
			if tt.arm != "" {
				req.Evaluation.CandidateArm = tt.arm
			}
			rec, err := f.log.Create(context.Background(), req)
			if tt.refused == "" {
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
				if rec.Override == "" || rec.Action != schemas.ActionHold {
					t.Errorf("record = %+v, want an override and action hold", rec)
				}
				return
			}
			var re *RefusedError
			if err == nil || !strings.Contains(err.Error(), tt.refused) {
				t.Fatalf("err = %v, want %q", err, tt.refused)
			}
			if !asRefused(err, &re) {
				t.Errorf("err %T is not a refusal", err)
			}
		})
	}
}

func TestCreateEvaluationIDNeedsTheEvaluationRoute(t *testing.T) {
	f := newFixture(t, schemas.ActionShip, "")
	_, err := f.log.Create(context.Background(), &schemas.ReleaseRequest{EvaluationID: "ev_x", Approver: "ana"})
	if err == nil || !strings.Contains(err.Error(), "report card") {
		t.Fatalf("err = %v", err)
	}
}

func asRefused(err error, re **RefusedError) bool {
	r, ok := err.(*RefusedError)
	*re = r
	return ok
}
