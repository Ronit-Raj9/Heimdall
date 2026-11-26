package improve

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func saveTestRecord(t *testing.T, r *Runner, rec *Record) {
	t.Helper()
	if err := os.MkdirAll(r.dir(rec.ID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.save(rec); err != nil {
		t.Fatal(err)
	}
}

// TestMarkInterrupted: only running records become interrupted, with the
// plain reason; others are left as they are.
func TestMarkInterrupted(t *testing.T) {
	root := t.TempDir()
	r := &Runner{Root: root}
	for id, st := range map[string]string{"imp_a": StatusRunning, "imp_b": StatusDone, "imp_c": StatusRunning, "imp_d": StatusFailed} {
		saveTestRecord(t, r, &Record{ID: id, Status: st})
	}
	if err := os.MkdirAll(filepath.Join(root, "studies"), 0o755); err != nil {
		t.Fatal(err)
	}
	n, err := MarkInterrupted(root)
	if err != nil || n != 2 {
		t.Fatalf("MarkInterrupted = %d, %v; want 2", n, err)
	}
	for id, want := range map[string]string{"imp_a": StatusInterrupted, "imp_b": StatusDone, "imp_c": StatusInterrupted, "imp_d": StatusFailed} {
		rec, err := r.load(id)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Status != want {
			t.Errorf("%s: status %s, want %s", id, rec.Status, want)
		}
		if want == StatusInterrupted && rec.Reason != InterruptedReason {
			t.Errorf("%s: reason %q", id, rec.Reason)
		}
	}
	if n, err := MarkInterrupted(filepath.Join(root, "missing")); n != 0 || err != nil {
		t.Errorf("missing root: %d, %v", n, err)
	}
}

// TestCanResumeStates: planned, failed and interrupted runs may resume;
// running and finished ones are refused with the plain text.
func TestCanResumeStates(t *testing.T) {
	r := &Runner{Root: t.TempDir()}
	cases := map[string]string{StatusPlanned: "", StatusFailed: "", StatusInterrupted: "",
		StatusRunning: "This run is already running.", StatusDone: "This run is done; it cannot be resumed.",
		StatusAwaiting: "This run is awaiting approval; it cannot be resumed."}
	for st, want := range cases {
		id := "imp_" + st[:4]
		saveTestRecord(t, r, &Record{ID: id, Status: st})
		_, err := r.CanResume(id)
		if want == "" && err != nil || want != "" && (err == nil || err.Error() != want) {
			t.Errorf("%s: %v, want %q", st, err, want)
		}
		if want != "" && !errors.Is(err, ErrState) && !errors.Is(err, ErrBusy) {
			t.Errorf("%s: %v is not a state error", st, err)
		}
	}
}

func continueRecord() *Record {
	return &Record{ID: "imp_cont", Status: StatusDone, Spec: Spec{Claim: ClaimSpec{Extension: true, Alpha: 0.05, ExtensionAlpha: 0.02}, Sealed: PhaseSpec{Trials: 2}},
		Verdict: &Verdict{State: VerdictLikelyBetter}, Final: &FinalClaim{N: 30, CI80: [2]float64{0.01, 0.09}}}
}

// TestContinueOfferedOnlyWhenReserved: only a done, likely better or too
// small run with the reserve kept is offered a second look.
func TestContinueOfferedOnlyWhenReserved(t *testing.T) {
	if err := CanContinue(continueRecord()); err != nil {
		t.Fatalf("offered run refused: %v", err)
	}
	rec := continueRecord()
	rec.Spec.Claim.Extension = false
	if CanContinue(rec) == nil {
		t.Error("run without a reserve was offered")
	}
	o := ContinueOfferFor(rec, ContinueRequest{ExtraTrials: 2, NewTasks: true}, 10, 0.1)
	if o.Runs != 0 || o.Text == "" || o.Text[:41] != "This run kept no reserve for a second loo" {
		t.Errorf("no reserve offer = %+v", o)
	}
	for _, st := range []string{VerdictSupported, VerdictNoEvidence, VerdictWorse, VerdictNoChange} {
		rec := continueRecord()
		rec.Verdict.State = st
		if CanContinue(rec) == nil {
			t.Errorf("%s was offered", st)
		}
	}
	rec = continueRecord()
	rec.Status = StatusRunning
	if CanContinue(rec) == nil {
		t.Error("running run was offered")
	}
	o = ContinueOfferFor(continueRecord(), ContinueRequest{ExtraTrials: 2, NewTasks: true}, 10, 0.1)
	if o.Runs != 2*(30*2+10*4) || o.NewTasks != 10 || o.USD[1] <= o.USD[0] || o.CanConfirm <= 0 {
		t.Errorf("offer = %+v", o)
	}
}

// TestContinueRefusedTwice: a run whose second look ran is not offered one
// again.
func TestContinueRefusedTwice(t *testing.T) {
	rec := continueRecord()
	rec.Extension, rec.Revision = &FinalClaim{N: 40}, 1
	err := CanContinue(rec)
	if err == nil || !errors.Is(err, ErrState) {
		t.Fatalf("second continue: %v", err)
	}
	if err.Error() != "This run already had its second look; it cannot be continued again." {
		t.Errorf("text %q", err.Error())
	}
}
