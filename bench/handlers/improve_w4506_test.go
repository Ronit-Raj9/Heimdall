package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
)

// w4Source is runnerSource with resume and continue that only check, so a
// test sees the answer of each state without running a loop.
type w4Source struct{ runnerSource }

func (s w4Source) Resume(_ context.Context, id, _ string) (*improve.Summary, error) {
	rec, err := s.r.CanResume(id)
	if err != nil {
		return nil, err
	}
	sum := improve.Summarize(rec, true)
	return &sum, nil
}

func (s w4Source) Continue(_ context.Context, id, _ string, _ improve.ContinueRequest) (*improve.Summary, error) {
	rec, err := s.r.CheckContinue(id)
	if err != nil {
		return nil, err
	}
	sum := improve.Summarize(rec, true)
	return &sum, nil
}

func (s w4Source) ContinueEstimate(_ context.Context, id string, in improve.ContinueRequest) (*improve.ContinueOffer, error) {
	rec, err := s.r.Get(id)
	if err != nil {
		return nil, err
	}
	return improve.ContinueOfferFor(rec, in, 10, 0.1), nil
}

func writeImproveRecord(t *testing.T, root string, rec *improve.Record) {
	t.Helper()
	dir := filepath.Join(root, rec.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(dir, improve.RecordFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestResumeRouteStates: planned, failed and interrupted runs resume (202);
// a done one answers 409 with the plain text; a source without resume 501.
func TestResumeRouteStates(t *testing.T) {
	root := t.TempDir()
	for id, st := range map[string]string{"imp_p": improve.StatusPlanned, "imp_f": improve.StatusFailed, "imp_i": improve.StatusInterrupted, "imp_d": improve.StatusDone} {
		writeImproveRecord(t, root, &improve.Record{ID: id, Status: st})
	}
	c := newImproveClient(t, w4Source{runnerSource{r: &improve.Runner{Root: root}}})
	for _, id := range []string{"imp_p", "imp_f", "imp_i"} {
		if resp := c.do(t, "POST", APIPrefix+"/improvements/"+id+"/resume", nil, authed("")); resp.StatusCode() != 202 {
			t.Errorf("%s: %d %s", id, resp.StatusCode(), resp.Body())
		}
	}
	resp := c.do(t, "POST", APIPrefix+"/improvements/imp_d/resume", nil, authed(""))
	if resp.StatusCode() != 409 || !strings.Contains(string(resp.Body()), "This run is done; it cannot be resumed.") || strings.Contains(string(resp.Body()), "imp_d") {
		t.Errorf("done: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := c.do(t, "POST", APIPrefix+"/improvements/imp_p/resume", nil, nil); resp.StatusCode() != 401 {
		t.Errorf("no token: %d", resp.StatusCode())
	}
	old := newImproveClient(t, runnerSource{r: &improve.Runner{Root: root}})
	resp = old.do(t, "POST", APIPrefix+"/improvements/imp_p/resume", nil, authed(""))
	if resp.StatusCode() != 501 || !strings.Contains(string(resp.Body()), "This server cannot resume or continue improvement runs.") {
		t.Errorf("old source: %d %s", resp.StatusCode(), resp.Body())
	}
}

// TestContinueRoutes: estimate answers the offer; continue without the
// runner's second look answers 501; with it, 202; a second continue 409.
func TestContinueRoutes(t *testing.T) {
	root := t.TempDir()
	rec := &improve.Record{ID: "imp_c", Status: improve.StatusDone, Verdict: &improve.Verdict{State: improve.VerdictLikelyBetter},
		Spec: improve.Spec{Claim: improve.ClaimSpec{Extension: true, Alpha: 0.05, ExtensionAlpha: 0.02}}, Final: &improve.FinalClaim{N: 20, CI80: [2]float64{0.01, 0.1}}}
	writeImproveRecord(t, root, rec)
	twice := *rec
	twice.ID, twice.Revision, twice.Extension = "imp_t", 1, &improve.FinalClaim{N: 30}
	writeImproveRecord(t, root, &twice)
	c := newImproveClient(t, w4Source{runnerSource{r: &improve.Runner{Root: root}}})
	body := []byte(`{"extra_trials": 2, "new_tasks": true}`)
	resp := c.do(t, "POST", APIPrefix+"/improvements/imp_c/continue/estimate", body, nil)
	var o improve.ContinueOffer
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &o) != nil || o.Runs == 0 || o.Text == "" {
		t.Fatalf("estimate: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := c.do(t, "POST", APIPrefix+"/improvements/imp_c/continue/estimate", []byte(`{"extra_trials": -1}`), nil); resp.StatusCode() != 422 {
		t.Errorf("bad body: %d", resp.StatusCode())
	}
	saved := improve.RunExtend
	t.Cleanup(func() { improve.RunExtend = saved })
	improve.RunExtend = nil
	resp = c.do(t, "POST", APIPrefix+"/improvements/imp_c/continue", body, authed(""))
	if resp.StatusCode() != 501 || !strings.Contains(string(resp.Body()), "This build cannot continue improvement runs.") {
		t.Errorf("no extend: %d %s", resp.StatusCode(), resp.Body())
	}
	improve.RunExtend = func(*improve.Runner, context.Context, string, int, bool, float64) (*improve.Record, error) { return nil, nil }
	if resp := c.do(t, "POST", APIPrefix+"/improvements/imp_c/continue", body, authed("")); resp.StatusCode() != 202 {
		t.Errorf("continue: %d %s", resp.StatusCode(), resp.Body())
	}
	resp = c.do(t, "POST", APIPrefix+"/improvements/imp_t/continue", body, authed(""))
	if resp.StatusCode() != 409 || !strings.Contains(string(resp.Body()), "second look") {
		t.Errorf("twice: %d %s", resp.StatusCode(), resp.Body())
	}
}
