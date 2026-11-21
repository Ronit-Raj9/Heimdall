package release

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

const testExp = "e_rel"

// fixture is a run store with one experiment of four counted, sealed runs
// and one analysis, plus a release log beside it.
type fixture struct {
	t        *testing.T
	st       *store.FSStore
	m        *schemas.Manifest
	runsRoot string
	log      *Log
	runs     []*schemas.Run
}

// newFixture writes the experiment and an analysis "a_one" with action and,
// when policy is not "", a policy result with that decision.
func newFixture(t *testing.T, action schemas.Action, policy schemas.PolicyDecision, opts ...func(*schemas.Manifest)) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{t: t, runsRoot: filepath.Join(base, "runs")}
	t.Cleanup(func() { makeWritable(t, f.runsRoot) })
	st, err := store.NewFSStore(f.runsRoot, schemas.NoopLogger{})
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}
	f.st = st
	f.m = &schemas.Manifest{
		ExpID: testExp,
		Arms: map[string]schemas.ArmManifest{
			"A": {Label: "baseline", Config: schemas.AgentConfig{ID: "agent_a"}, ConfigHash: "sha256:cfg_a"},
			"B": {Label: "candidate", Config: schemas.AgentConfig{ID: "agent_b"}, ConfigHash: "sha256:cfg_b"},
		},
	}
	for _, task := range []string{"t01", "t02"} {
		for _, arm := range []string{"A", "B"} {
			f.m.Slots = append(f.m.Slots, schemas.Slot{SlotID: "s_" + task + "_" + strings.ToLower(arm), PairID: "p_" + task,
				Task: task, Arm: arm, Condition: "clean", Trial: 1, Counted: true})
		}
	}
	f.m.Slots = append(f.m.Slots, schemas.Slot{SlotID: "s_compat", PairID: "p_c", Task: "t01", Arm: "C", Condition: "clean", Trial: 1})
	for _, o := range opts {
		o(f.m)
	}
	// The real hash: release creation recomputes it (audit 1-A-02).
	if f.m.Hash, err = utils.ManifestHash(f.m); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.CreateExperiment(ctx, f.m); err != nil {
		t.Fatalf("CreateExperiment: %v", err)
	}
	for i := range f.m.Slots {
		sl := f.m.Slots[i]
		if !sl.Counted {
			continue
		}
		sl.ExpID = testExp
		r := &schemas.Run{RunID: "r_" + strings.TrimPrefix(sl.SlotID, "s_"), ExpID: testExp, SlotID: sl.SlotID, PairID: sl.PairID,
			Attempt: 1, Stage: schemas.StageSeal,
			ManifestHash: f.m.Hash, Outcome: schemas.OutcomePass}
		if _, err := st.NewRun(ctx, &sl, r); err != nil {
			t.Fatalf("NewRun: %v", err)
		}
		for name, data := range map[string]string{
			"agent.stderr.txt":   "stderr",
			"agent.stream.jsonl": "x",
			"cost.json":          `{"total":1}`,
			"events.jsonl":       "{}\n",
			"gateway.raw.jsonl":  `{"raw_available":true}` + "\n",
			"gates.json":         `[]`,
			"grade.json":         `{"passed":true}`,
			"metrics.json":       `{"steps":{"state":"observed","v":3,"evidence":"native"}}`,
			"tools.jsonl":        "{}\n",
		} {
			if err := st.WriteRunFile(ctx, r, name, []byte(data)); err != nil {
				t.Fatalf("WriteRunFile: %v", err)
			}
		}
		if _, err := st.Seal(ctx, r); err != nil {
			t.Fatalf("Seal: %v", err)
		}
		f.runs = append(f.runs, r)
	}
	f.writeAnalysis("a_one", time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC), action, policy)
	log, err := New(Deps{Store: st, Root: filepath.Join(base, "releases"),
		Now: func() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.log = log
	return f
}

func (f *fixture) writeAnalysis(id string, at time.Time, action schemas.Action, policy schemas.PolicyDecision) {
	f.t.Helper()
	decision := schemas.DecisionImproved
	if action != schemas.ActionShip {
		decision = schemas.DecisionInconclusive
	}
	a := &schemas.Analysis{AnalysisID: id, ExpID: testExp, CreatedAt: at, GradeSource: schemas.GradeSourceOriginal,
		Verdict: schemas.Verdict{Decision: decision, Action: action, ManifestHash: f.m.Hash,
			Effect: &schemas.Effect{ArmA: "A", ArmB: "B"}}}
	if policy != "" {
		a.Policy = &schemas.PolicyResult{PolicyName: "team", PolicyHash: "sha256:pol", Decision: policy}
	}
	if err := f.st.WriteAnalysis(context.Background(), a, map[string][]byte{"report.md": []byte("# report\n")}); err != nil {
		f.t.Fatalf("WriteAnalysis: %v", err)
	}
}

// tamper rewrites one file of a sealed run.
func (f *fixture) tamper(runID, name, data string) {
	f.t.Helper()
	dir := filepath.Join(f.runsRoot, testExp, "slots", "s_"+strings.TrimPrefix(runID, "r_"), "runs", runID)
	makeWritable(f.t, dir)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
		f.t.Fatalf("tamper: %v", err)
	}
}

func makeWritable(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(p, 0o755)
		}
		return os.Chmod(p, 0o644)
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Logf("make writable %s: %v", root, err)
	}
}

func request(approver, override string) *schemas.ReleaseRequest {
	return &schemas.ReleaseRequest{ExpID: testExp, Approver: approver, Note: "weekly", Override: override}
}

func TestCreate(t *testing.T) {
	tests := []struct {
		name     string
		action   schemas.Action
		policy   schemas.PolicyDecision
		req      *schemas.ReleaseRequest
		tamper   bool
		refused  string // substring of the refusal; "" means released
		sentinel error
	}{
		{name: "ship without policy", action: schemas.ActionShip, req: request("ana", "")},
		{name: "ship policy allow", action: schemas.ActionShip, policy: schemas.PolicyAllow, req: request("ana", "")},
		{name: "ship policy warn", action: schemas.ActionShip, policy: schemas.PolicyWarn, req: request("ana", "")},
		{name: "not ship", action: schemas.ActionHold, req: request("ana", ""), refused: "action is hold"},
		{name: "policy block", action: schemas.ActionShip, policy: schemas.PolicyBlock, req: request("ana", ""), refused: "decision is block"},
		{name: "missing approver", action: schemas.ActionShip, req: request("  ", ""), refused: "approver is required"},
		{name: "override of not ship", action: schemas.ActionNoDecision, req: request("ana", "hotfix, risk accepted")},
		{name: "override of block", action: schemas.ActionShip, policy: schemas.PolicyBlock, req: request("ana", "cost check waived")},
		{name: "override with nothing to override", action: schemas.ActionShip, req: request("ana", "just because"), refused: "nothing to override"},
		{name: "seal mismatch", action: schemas.ActionShip, req: request("ana", ""), tamper: true, refused: "does not verify", sentinel: schemas.ErrSealMismatch},
		{name: "seal mismatch not overridable", action: schemas.ActionHold, req: request("ana", "ship it"), tamper: true, refused: "does not verify", sentinel: schemas.ErrSealMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, tt.action, tt.policy)
			if tt.tamper {
				f.tamper("r_t02_b", "metrics.json", `{"steps":{"state":"observed","v":1,"evidence":"native"}}`)
			}
			rec, err := f.log.Create(context.Background(), tt.req)
			if tt.refused != "" {
				var refused *RefusedError
				if !errors.As(err, &refused) || !strings.Contains(err.Error(), tt.refused) {
					t.Fatalf("err = %v, want refusal %q", err, tt.refused)
				}
				if tt.sentinel != nil && !errors.Is(err, tt.sentinel) {
					t.Errorf("err = %v, want %v", err, tt.sentinel)
				}
				list, lerr := f.log.List(context.Background())
				if lerr != nil || len(list.Releases) != 0 {
					t.Errorf("refusal wrote a record: %v %v", list, lerr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			checkRecord(t, f, rec, tt.req, tt.policy)
		})
	}
}

func checkRecord(t *testing.T, f *fixture, rec *schemas.ReleaseRecord, req *schemas.ReleaseRequest, policy schemas.PolicyDecision) {
	t.Helper()
	var seals []string
	for _, r := range f.runs {
		h, err := utils.SHA256File(filepath.Join(f.runsRoot, testExp, "slots", "s_"+strings.TrimPrefix(r.RunID, "r_"), "runs", r.RunID, "seal.json"))
		if err != nil {
			t.Fatal(err)
		}
		seals = append(seals, h)
	}
	want := map[string]string{
		"exp_id": testExp, "analysis_id": "a_one", "arm": "B", "agent_config_id": "agent_b", "config_hash": "sha256:cfg_b",
		"manifest_hash": f.m.Hash, "seals_digest": SealsDigest(seals), "approver": "ana", "note": "weekly",
		"override": strings.TrimSpace(req.Override), "policy_decision": string(policy), "prev_hash": "",
	}
	got := map[string]string{
		"exp_id": rec.ExpID, "analysis_id": rec.AnalysisID, "arm": rec.Arm, "agent_config_id": rec.AgentConfigID,
		"config_hash": rec.ConfigHash, "manifest_hash": rec.ManifestHash, "seals_digest": rec.SealsDigest,
		"approver": rec.Approver, "note": rec.Note, "override": rec.Override, "policy_decision": string(rec.PolicyDecision),
		"prev_hash": rec.PrevHash,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %q, want %q", k, got[k], w)
		}
	}
	if !strings.HasPrefix(rec.ReleaseID, "rel_") || rec.EvidencePath != rec.ReleaseID+"/"+EvidenceFile {
		t.Errorf("release id %q, evidence path %q", rec.ReleaseID, rec.EvidencePath)
	}
	if h, err := recordHash(rec); err != nil || h != rec.Hash {
		t.Errorf("hash %s, recomputed %s (%v)", rec.Hash, h, err)
	}
	p, err := f.log.Export(context.Background(), rec.ReleaseID)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if sum, _ := utils.SHA256File(p); sum != rec.EvidenceSHA256 {
		t.Errorf("evidence sha %s, record %s", sum, rec.EvidenceSHA256)
	}
	stored, err := f.log.Get(context.Background(), rec.ReleaseID)
	if err != nil || *stored != *rec {
		t.Errorf("Get = %+v, %v; want %+v", stored, err, rec)
	}
}

// TestCreateNeedsFrozenPolicy: an analysis written by `check -policy none`
// or with another policy file does not stand in for the policy the
// experiment froze; releasing it needs an override reason (QC2-22-01).
func TestCreateNeedsFrozenPolicy(t *testing.T) {
	frozen := func(m *schemas.Manifest) {
		m.Experiment.Policy = "policies/team.yaml"
		m.PolicyHash = "sha256:pol"
	}
	tests := []struct {
		name     string
		hash     string // policy hash of the analysis's result; "" means no policy result
		override string
		refused  string
	}{
		{name: "frozen policy evaluated", hash: "sha256:pol"},
		{name: "policy none", refused: "did not evaluate the frozen policy"},
		{name: "other policy file", hash: "sha256:other", refused: "not the frozen policy"},
		{name: "policy none with override", override: "frozen policy waived for hotfix"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, schemas.ActionShip, "", frozen)
			a := &schemas.Analysis{AnalysisID: "a_two", ExpID: testExp, CreatedAt: time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC),
				GradeSource: schemas.GradeSourceOriginal, Verdict: schemas.Verdict{Decision: schemas.DecisionImproved,
					Action: schemas.ActionShip, ManifestHash: f.m.Hash, Effect: &schemas.Effect{ArmA: "A", ArmB: "B"}}}
			if tt.hash != "" {
				a.Policy = &schemas.PolicyResult{PolicyName: "team", PolicyHash: tt.hash, Decision: schemas.PolicyAllow}
			}
			if err := f.st.WriteAnalysis(context.Background(), a, nil); err != nil {
				t.Fatal(err)
			}
			rec, err := f.log.Create(context.Background(), request("ana", tt.override))
			if tt.refused != "" {
				var refused *RefusedError
				if !errors.As(err, &refused) || !strings.Contains(err.Error(), tt.refused) {
					t.Fatalf("err = %v, want refusal %q", err, tt.refused)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if rec.AnalysisID != "a_two" || rec.Override != tt.override {
				t.Errorf("record analysis %s override %q", rec.AnalysisID, rec.Override)
			}
		})
	}
}

func TestCreatePicksAnalysis(t *testing.T) {
	f := newFixture(t, schemas.ActionHold, "")
	f.writeAnalysis("a_two", time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC), schemas.ActionShip, "")
	f.writeAnalysis("a_old", time.Date(2026, 11, 13, 11, 0, 0, 0, time.UTC), schemas.ActionShip, "")
	// A newer analysis of another grade source (written by viewing its
	// verdict) is not the default (4-B-07).
	judge := &schemas.Analysis{AnalysisID: "a_judge", ExpID: testExp, CreatedAt: time.Date(2026, 11, 15, 11, 0, 0, 0, time.UTC),
		GradeSource: "judge_x", Verdict: schemas.Verdict{Decision: schemas.DecisionImproved, Action: schemas.ActionShip,
			ManifestHash: f.m.Hash, Effect: &schemas.Effect{ArmA: "A", ArmB: "B"}}}
	if err := f.st.WriteAnalysis(context.Background(), judge, nil); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, analysisID, want, refused string
	}{
		{name: "newest by created_at", want: "a_two"},
		{name: "named", analysisID: "a_old", want: "a_old"},
		{name: "named hold", analysisID: "a_one", refused: "action is hold"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := request("ana", "")
			req.AnalysisID = tt.analysisID
			rec, err := f.log.Create(context.Background(), req)
			if tt.refused != "" {
				if err == nil || !strings.Contains(err.Error(), tt.refused) {
					t.Fatalf("err = %v, want %q", err, tt.refused)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if rec.AnalysisID != tt.want {
				t.Errorf("analysis = %s, want %s", rec.AnalysisID, tt.want)
			}
		})
	}
}

func TestCreateErrors(t *testing.T) {
	f := newFixture(t, schemas.ActionShip, "")
	tests := []struct {
		name     string
		req      *schemas.ReleaseRequest
		sentinel error
		text     string
	}{
		{name: "unknown experiment", req: &schemas.ReleaseRequest{ExpID: "e_none", Approver: "ana"}, sentinel: schemas.ErrNotFound},
		{name: "unknown analysis", req: &schemas.ReleaseRequest{ExpID: testExp, AnalysisID: "a_none", Approver: "ana"}, sentinel: schemas.ErrNotFound},
		{name: "bad exp id", req: &schemas.ReleaseRequest{ExpID: "../x", Approver: "ana"}, text: "invalid exp_id"},
		{name: "bad analysis id", req: &schemas.ReleaseRequest{ExpID: testExp, AnalysisID: "A/B", Approver: "ana"}, text: "invalid analysis_id"},
		{name: "long approver", req: &schemas.ReleaseRequest{ExpID: testExp, Approver: strings.Repeat("a", maxApprover+1)}, text: "approver is longer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.log.Create(context.Background(), tt.req)
			if err == nil {
				t.Fatal("Create succeeded")
			}
			if tt.sentinel != nil && !errors.Is(err, tt.sentinel) {
				t.Errorf("err = %v, want %v", err, tt.sentinel)
			}
			if tt.text != "" && !strings.Contains(err.Error(), tt.text) {
				t.Errorf("err = %v, want %q", err, tt.text)
			}
		})
	}
}

func TestCreateRefusesBrokenSealChain(t *testing.T) {
	tests := []struct {
		name    string
		breakIt func(f *fixture)
	}{
		{"seals.log line edited", func(f *fixture) {
			p := filepath.Join(f.runsRoot, testExp, "seals.log")
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, append(b, []byte("not a seal line\n")...), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"sealed run deleted", func(f *fixture) {
			dir := filepath.Join(f.runsRoot, testExp, "slots", "s_t02_b", "runs")
			makeWritable(t, dir)
			if err := os.RemoveAll(filepath.Join(dir, "r_t02_b")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, schemas.ActionShip, "")
			tt.breakIt(f)
			_, err := f.log.Create(context.Background(), request("ana", ""))
			var refused *RefusedError
			if !errors.As(err, &refused) || !errors.Is(err, schemas.ErrSealMismatch) || !strings.Contains(err.Error(), "seals.log chain") {
				t.Fatalf("err = %v, want a refusal naming the seals.log chain", err)
			}
		})
	}
}

func TestNewRefusesRootInsideRuns(t *testing.T) {
	f := newFixture(t, schemas.ActionShip, "")
	for _, root := range []string{f.runsRoot, filepath.Join(f.runsRoot, "releases")} {
		if _, err := New(Deps{Store: f.st, Root: root}); err == nil {
			t.Errorf("New(%s) succeeded", root)
		}
	}
	if _, err := New(Deps{Root: " "}); err == nil {
		t.Error("New with empty root succeeded")
	}
}

func TestDefaultRoot(t *testing.T) {
	t.Setenv(RootEnv, "")
	if got := DefaultRoot("/bench"); got != filepath.Join("/bench", "releases") {
		t.Errorf("DefaultRoot = %s", got)
	}
	t.Setenv(RootEnv, "/elsewhere")
	if got := DefaultRoot("/bench"); got != "/elsewhere" {
		t.Errorf("DefaultRoot with env = %s", got)
	}
}

func TestSealsDigestIgnoresOrder(t *testing.T) {
	a := SealsDigest([]string{"bb", "aa"})
	if a != SealsDigest([]string{"aa", "bb"}) || a != utils.SHA256Bytes([]byte("aa\nbb\n")) {
		t.Errorf("digest %s", a)
	}
}

// TestChainSealsReadsPackedLog covers QC2-22-04: chain documents come from
// the seals.log bytes the pack holds, never a second read of the live log.
func TestChainSealsReadsPackedLog(t *testing.T) {
	f := newFixture(t, schemas.ActionShip, "")
	expDir := filepath.Join(f.runsRoot, testExp)
	live, err := os.ReadFile(filepath.Join(expDir, "seals.log"))
	if err != nil || len(live) == 0 {
		t.Fatalf("seals.log: %v", err)
	}
	// A packed snapshot taken before any seal: the live log's lines must not
	// leak into the chain.
	if out, err := chainSeals(expDir, nil, nil); err != nil || len(out) != 0 {
		t.Fatalf("empty packed log gave %d chain files, %v", len(out), err)
	}
	if out, err := chainSeals(expDir, live, nil); err != nil || len(out) != len(f.runs) {
		t.Fatalf("packed log gave %d chain files, want %d (%v)", len(out), len(f.runs), err)
	}
}
