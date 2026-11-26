package handlers

// Group H integration tests of the improvement routes against the real
// runner (scripted executor).

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/improve/improvetest"
)

// extendSource is the server's source with the second look run in the
// request, so the test can compare the offer with what ran.
type extendSource struct{ runnerSource }

func (s extendSource) Resume(_ context.Context, id, _ string) (*improve.Summary, error) {
	rec, err := s.r.CanResume(id)
	if err != nil {
		return nil, err
	}
	sum := improve.Summarize(rec, false)
	return &sum, nil
}

func (s extendSource) Continue(ctx context.Context, id, _ string, in improve.ContinueRequest) (*improve.Summary, error) {
	if _, err := s.r.CheckContinue(id); err != nil {
		return nil, err
	}
	rec, err := improve.RunExtend(s.r, ctx, id, in.ExtraTrials, in.NewTasks, in.ExtraBudgetUSD)
	if err != nil {
		return nil, err
	}
	sum := improve.Summarize(rec, false)
	return &sum, nil
}

func (s extendSource) ContinueEstimate(ctx context.Context, id string, in improve.ContinueRequest) (*improve.ContinueOffer, error) {
	return s.r.OfferContinue(ctx, id, in)
}

// Item 5 of the group H merge: the continue estimate prices exactly the
// second look the runner then runs (same runs, the same new tasks, money
// around the runs' real cost), the second look is tested at the reserved
// extension level, and a second continue is refused.
func TestContinueOfferMatchesTheSecondLook(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120}
	root := filepath.Join(t.TempDir(), improve.DefaultDir)
	r := &improve.Runner{Root: root, Exec: x}
	pool := []string{}
	for i := 1; i <= 100; i++ {
		pool = append(pool, improvetest.TaskID(i))
	}
	spec := &improve.Spec{Name: "continue-loop", Agent: "agents/scripted-improver.yaml", Suite: "scripted@1.0.0", Seed: 7, Tasks: pool,
		Editable: improve.Editable{Files: []string{improvetest.NotesPath}},
		Splits:   improve.SplitSpec{Dev: 0.48, Audit: 0.48, Sealed: 0.04},
		Commit:   improve.CommitSpec{Alpha: 0.10, Trials: 2}, Sealed: improve.PhaseSpec{Trials: 2},
		Claim: improve.ClaimSpec{Extension: true}, Budget: improve.BudgetSpec{TotalUSD: 50}}
	rec, err := r.Create(context.Background(), spec, "ana")
	if err != nil {
		t.Fatal(err)
	}
	if rec, err = r.Run(context.Background(), rec.ID); err != nil {
		t.Fatal(err)
	}
	if rec.Verdict == nil || rec.Verdict.State != improve.VerdictInsufficientSample {
		t.Fatalf("verdict %+v", rec.Verdict)
	}
	c := newImproveClient(t, extendSource{runnerSource{r: r}})

	// The detail offers it, and the page's field names decode.
	resp := c.do(t, "GET", APIPrefix+"/improvements/"+rec.ID, nil, nil)
	var detail struct {
		CanContinue bool `json:"can_continue"`
		Verdict     struct {
			Continue *struct {
				Runs int    `json:"runs"`
				Text string `json:"text"`
			} `json:"continue"`
		} `json:"verdict_detail"`
	}
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &detail) != nil || !detail.CanContinue || detail.Verdict.Continue == nil {
		t.Fatalf("detail: %d %s", resp.StatusCode(), resp.Body())
	}

	body := []byte(`{"extra_trials": 2, "new_tasks": true, "extra_budget_usd": 5}`)
	resp = c.do(t, "POST", APIPrefix+"/improvements/"+rec.ID+"/continue/estimate", body, nil)
	var offer struct {
		ExtraTrials int        `json:"extra_trials"`
		NewTasks    int        `json:"new_tasks"`
		Runs        int        `json:"runs"`
		USD         [2]float64 `json:"usd"`
		CanConfirm  float64    `json:"can_confirm"`
		Text        string     `json:"text"`
	}
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &offer) != nil {
		t.Fatalf("estimate: %d %s", resp.StatusCode(), resp.Body())
	}
	pageShape(t, readTSInterfaces(t), "ContinueOffer", resp.Body())
	if offer.ExtraTrials != 2 || offer.NewTasks != 20 || offer.Runs != 2*(len(rec.Splits.Sealed)*2+20*4) || offer.Text == "" || offer.CanConfirm <= 0 {
		t.Fatalf("offer %+v", offer)
	}
	spentBefore := rec.Controls.CostUSD

	resp = c.do(t, "POST", APIPrefix+"/improvements/"+rec.ID+"/continue", body, authed(""))
	if resp.StatusCode() != 202 {
		t.Fatalf("continue: %d %s", resp.StatusCode(), resp.Body())
	}
	got, err := r.Get(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, rec.ID, improve.ExtensionRunsFile))
	if err != nil {
		t.Fatal(err)
	}
	var runs []improve.RunScore
	if err := json.Unmarshal(b, &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != offer.Runs {
		t.Errorf("the second look ran %d runs, the offer said %d", len(runs), offer.Runs)
	}
	cost := got.Controls.CostUSD - spentBefore
	if cost < offer.USD[0]-1e-9 || cost > offer.USD[1]+1e-9 {
		t.Errorf("the second look cost $%.2f, the offer said $%.2f to $%.2f", cost, offer.USD[0], offer.USD[1])
	}
	// The first look used the main share, the second the reserve; together
	// they stay within the claim's alpha (0.03 + 0.02 = 0.05).
	if got.Extension == nil || math.Abs(got.Extension.Level-got.Spec.Claim.ExtensionAlpha) > 1e-12 ||
		rec.Verdict.Level+got.Extension.Level > got.Spec.Claim.Alpha+1e-12 {
		t.Errorf("second look level %+v, first %v, claim alpha %v", got.Extension, rec.Verdict.Level, got.Spec.Claim.Alpha)
	}

	resp = c.do(t, "POST", APIPrefix+"/improvements/"+rec.ID+"/continue", body, authed(""))
	if resp.StatusCode() != 409 || !strings.Contains(string(resp.Body()), "already had its second look") {
		t.Errorf("twice: %d %s", resp.StatusCode(), resp.Body())
	}
	resp = c.do(t, "POST", APIPrefix+"/improvements/"+rec.ID+"/continue/estimate", body, nil)
	if resp.StatusCode() != 409 {
		t.Errorf("estimate after the second look: %d %s", resp.StatusCode(), resp.Body())
	}
}

// A second look that failed leaves the run done with its first verdict and
// cannot be started again: the reserve is used once.
func TestFailedSecondLookIsNotRetried(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120, FailOn: improve.PhaseExtension}
	root := filepath.Join(t.TempDir(), improve.DefaultDir)
	r := &improve.Runner{Root: root, Exec: x}
	pool := []string{}
	for i := 1; i <= 100; i++ {
		pool = append(pool, improvetest.TaskID(i))
	}
	spec := &improve.Spec{Name: "continue-fail", Agent: "agents/scripted-improver.yaml", Suite: "scripted@1.0.0", Seed: 7, Tasks: pool,
		Editable: improve.Editable{Files: []string{improvetest.NotesPath}},
		Splits:   improve.SplitSpec{Dev: 0.48, Audit: 0.48, Sealed: 0.04},
		Commit:   improve.CommitSpec{Alpha: 0.10, Trials: 2}, Sealed: improve.PhaseSpec{Trials: 2},
		Claim: improve.ClaimSpec{Extension: true}, Budget: improve.BudgetSpec{TotalUSD: 50}}
	rec, err := r.Create(context.Background(), spec, "ana")
	if err != nil {
		t.Fatal(err)
	}
	if rec, err = r.Run(context.Background(), rec.ID); err != nil {
		t.Fatal(err)
	}
	first := rec.Verdict.Headline
	got, err := improve.RunExtend(r, context.Background(), rec.ID, 2, false, 0)
	if err == nil || got.Status != improve.StatusDone || got.Verdict.Headline != first {
		t.Fatalf("failed look: %v %s %q", err, got.Status, got.Verdict.Headline)
	}
	if _, err := improve.RunExtend(r, context.Background(), rec.ID, 2, false, 0); !errors.Is(err, improve.ErrState) {
		t.Errorf("retry after a failed look: %v", err)
	}
}

// refusingExec is the scripted executor whose improver cannot run.
type refusingExec struct{ *improvetest.Exec }

func (refusingExec) CheckImprover(context.Context, *improve.Spec) error {
	return errors.New("The improver benchmark heimdall-improver has not been admitted on this server yet, so improvement runs cannot start.")
}

// Security review: a run started from the web whose improver cannot run is
// refused with 422 bad_improver and the plain sentence, before any scoring
// or improver call and with no record left behind.
func TestCreateRefusesBadImproverBeforeSpend(t *testing.T) {
	x := refusingExec{&improvetest.Exec{Behavior: improvetest.Honest, Base: 1, NTasks: 40}}
	r := &improve.Runner{Root: filepath.Join(t.TempDir(), improve.DefaultDir), Exec: x}
	c := newImproveClient(t, runnerSource{r: r})
	resp := c.do(t, "POST", APIPrefix+"/improvements", []byte(webFormBody), authed("application/json"))
	if resp.StatusCode() != 422 || !strings.Contains(string(resp.Body()), `"bad_improver"`) ||
		!strings.Contains(string(resp.Body()), "has not been admitted on this server yet") {
		t.Fatalf("create: %d %s", resp.StatusCode(), resp.Body())
	}
	if len(x.Calls) != 0 {
		t.Errorf("calls before the refusal: %v", x.Calls)
	}
	if recs, _ := r.List(); len(recs) != 0 {
		t.Errorf("records after the refusal: %d", len(recs))
	}
}
