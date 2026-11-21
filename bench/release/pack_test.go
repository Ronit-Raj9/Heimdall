package release

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

func readZip(t *testing.T, b []byte) ([]string, map[string][]byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	var names []string
	files := map[string][]byte{}
	for _, zf := range zr.File {
		if !zf.Modified.Equal(packTime) {
			t.Errorf("%s modified %v, want %v", zf.Name, zf.Modified, packTime)
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		if err := rc.Close(); err != nil {
			t.Fatal(err)
		}
		names = append(names, zf.Name)
		files[zf.Name] = data
	}
	return names, files
}

func TestEvidencePackContents(t *testing.T) {
	tests := []struct {
		name   string
		policy schemas.PolicyDecision
	}{
		{"with policy", schemas.PolicyAllow},
		{"without policy", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, schemas.ActionShip, tt.policy)
			rec, err := f.log.Create(context.Background(), request("ana", ""))
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			b, err := f.log.Evidence(context.Background(), rec.ReleaseID)
			if err != nil {
				t.Fatalf("Evidence: %v", err)
			}
			names, files := readZip(t, b)
			want := []string{"README.txt", "release.json", "manifest.json", "analysis/verdict.json", "analysis/report.md"}
			if tt.policy != "" {
				want = append(want, "analysis/policy.json")
			}
			want = append(want, "seals.log")
			for _, id := range []string{"r_t01_a", "r_t01_b", "r_t02_a", "r_t02_b"} {
				sealPath := filepath.Join(f.runsRoot, testExp, "slots", "s_"+strings.TrimPrefix(id, "r_"), "runs", id, "seal.json")
				sealBytes, err := os.ReadFile(sealPath)
				if err != nil {
					t.Fatal(err)
				}
				var seal schemas.SealRecord
				if err := json.Unmarshal(sealBytes, &seal); err != nil {
					t.Fatal(err)
				}
				want = append(want, "runs/"+id+"/seal.json")
				sealedNames := make([]string, 0, len(seal.Files))
				for name := range seal.Files {
					sealedNames = append(sealedNames, name)
				}
				sort.Strings(sealedNames)
				for _, name := range sealedNames {
					want = append(want, "runs/"+id+"/"+name)
				}
			}
			if strings.Join(names, ",") != strings.Join(want, ",") {
				t.Fatalf("files\n got %v\nwant %v", names, want)
			}
			checkPackFiles(t, f, rec, files)
		})
	}
}

func checkPackFiles(t *testing.T, f *fixture, rec *schemas.ReleaseRecord, files map[string][]byte) {
	t.Helper()
	var inPack schemas.ReleaseRecord
	if err := json.Unmarshal(files["release.json"], &inPack); err != nil {
		t.Fatal(err)
	}
	if inPack.EvidenceSHA256 != "" || inPack.Hash != "" {
		t.Errorf("release.json carries evidence_sha256 %q hash %q, want both empty", inPack.EvidenceSHA256, inPack.Hash)
	}
	inPack.EvidenceSHA256, inPack.Hash = rec.EvidenceSHA256, rec.Hash
	if inPack != *rec {
		t.Errorf("release.json = %+v\nwant %+v", inPack, *rec)
	}
	expDir := filepath.Join(f.runsRoot, testExp)
	for name, src := range map[string]string{
		"manifest.json":             filepath.Join(expDir, "manifest.json"),
		"seals.log":                 filepath.Join(expDir, "seals.log"),
		"analysis/verdict.json":     filepath.Join(expDir, "analysis", "a_one", "verdict.json"),
		"runs/r_t01_b/seal.json":    filepath.Join(expDir, "slots", "s_t01_b", "runs", "r_t01_b", "seal.json"),
		"runs/r_t01_b/grade.json":   filepath.Join(expDir, "slots", "s_t01_b", "runs", "r_t01_b", "grade.json"),
		"runs/r_t02_a/metrics.json": filepath.Join(expDir, "slots", "s_t02_a", "runs", "r_t02_a", "metrics.json"),
	} {
		disk, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(disk, files[name]) {
			t.Errorf("%s differs from %s", name, src)
		}
	}
	for _, run := range f.runs {
		sealName := "runs/" + run.RunID + "/seal.json"
		var seal schemas.SealRecord
		if err := json.Unmarshal(files[sealName], &seal); err != nil {
			t.Fatalf("parse %s: %v", sealName, err)
		}
		dir := filepath.Join(expDir, "slots", run.SlotID, "runs", run.RunID)
		for name := range seal.Files {
			disk, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
			if err != nil {
				t.Fatalf("read sealed file %s: %v", name, err)
			}
			packedName := "runs/" + run.RunID + "/" + name
			if !bytes.Equal(disk, files[packedName]) {
				t.Errorf("%s differs from sealed file %s", packedName, name)
			}
		}
	}
	// The digest in the record recomputes from the packed seals alone.
	var seals []string
	for name, data := range files {
		if strings.HasSuffix(name, "/seal.json") {
			seals = append(seals, utils.SHA256Bytes(data))
			if !bytes.Contains(files["seals.log"], []byte(utils.SHA256Bytes(data))) {
				t.Errorf("%s hash not in seals.log", name)
			}
		}
	}
	if SealsDigest(seals) != rec.SealsDigest {
		t.Errorf("seals digest from pack %s, record %s", SealsDigest(seals), rec.SealsDigest)
	}
	readme := string(files["README.txt"])
	for _, want := range []string{rec.ReleaseID, "not signed", "not a", "compliance certification", "seals_digest", "release verify"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README.txt lacks %q", want)
		}
	}
}

func TestEvidencePackRegate(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, schemas.ActionShip, "")
	info := store.RegradeInfo{GateVersions: map[string]string{"g1_protected_paths": "1.1.0"}}
	regate := &store.GateRegrade{Outcome: schemas.OutcomePass, SealedOutcome: schemas.OutcomeIntegrityViolation,
		Y: schemas.Observed(1, schemas.EvidenceOutcome), Gates: []schemas.GateResult{}}
	if err := f.st.WriteGateRegrades(ctx, testExp, "rg_x", info, map[string]*store.GateRegrade{"r_t01_a": regate}); err != nil {
		t.Fatalf("WriteGateRegrades: %v", err)
	}
	a := &schemas.Analysis{AnalysisID: "a_rg", ExpID: testExp, GradeSource: "rg_x", CreatedAt: time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC),
		Verdict: schemas.Verdict{Decision: schemas.DecisionImproved, Action: schemas.ActionShip, ManifestHash: f.m.Hash,
			Effect: &schemas.Effect{ArmA: "A", ArmB: "B"}}}
	f.digest(a)
	if err := f.st.WriteAnalysis(ctx, a, nil); err != nil {
		t.Fatalf("WriteAnalysis: %v", err)
	}
	m, err := f.st.LoadManifest(ctx, testExp)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := f.log.loadEvidence(ctx, m, "a_rg")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := newRecord(m, ev, *request("ana", ""), f.log.d.Now())
	if err != nil {
		t.Fatal(err)
	}
	files, err := packFiles(f.runsRoot, rec, ev)
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	for _, pf := range files {
		if pf.name == "runs/r_t01_b/regrade.regate.json" {
			t.Fatal("a run without a regate got one in the pack")
		}
		if pf.name == "runs/r_t01_a/regrade.regate.json" {
			got = pf.data
		}
	}
	disk, err := os.ReadFile(filepath.Join(f.runsRoot, testExp, "regrades", "rg_x", "runs", "r_t01_a", "regate.json"))
	if err != nil || !bytes.Equal(got, disk) {
		t.Fatalf("regate in pack differs from disk (err %v):\n%s", err, got)
	}
}

// TestEvidencePackReview checks that an analysis on grade source reviewed
// packs the review each reviewed run's outcome came from.
func TestEvidencePackReview(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, schemas.ActionShip, "")
	rv, err := f.st.AddReview(ctx, testExp, &schemas.ReviewRequest{RunID: "r_t01_a", Decision: schemas.ReviewFail, Reason: "empty diff", Reviewer: "ana"})
	if err != nil {
		t.Fatalf("AddReview: %v", err)
	}
	a := &schemas.Analysis{AnalysisID: "a_rv", ExpID: testExp, GradeSource: schemas.GradeSourceReviewed, CreatedAt: time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC),
		Verdict: schemas.Verdict{Decision: schemas.DecisionImproved, Action: schemas.ActionShip, ManifestHash: f.m.Hash,
			Effect: &schemas.Effect{ArmA: "A", ArmB: "B"}}}
	f.digest(a)
	if err := f.st.WriteAnalysis(ctx, a, nil); err != nil {
		t.Fatalf("WriteAnalysis: %v", err)
	}
	m, err := f.st.LoadManifest(ctx, testExp)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := f.log.loadEvidence(ctx, m, "a_rv")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := newRecord(m, ev, *request("ana", ""), f.log.d.Now())
	if err != nil {
		t.Fatal(err)
	}
	files, err := packFiles(f.runsRoot, rec, ev)
	if err != nil {
		t.Fatal(err)
	}
	var got []byte
	for _, pf := range files {
		if pf.name == "runs/r_t01_b/review.json" {
			t.Fatal("a run without a review got one in the pack")
		}
		if pf.name == "runs/r_t01_a/review.json" {
			got = pf.data
		}
	}
	disk, err := os.ReadFile(filepath.Join(f.runsRoot, testExp, "reviews", rv.ReviewID, "review.json"))
	if err != nil || !bytes.Equal(got, disk) {
		t.Fatalf("review in pack differs from disk (err %v):\n%s", err, got)
	}
}

func TestEvidencePackDeterministic(t *testing.T) {
	f := newFixture(t, schemas.ActionShip, schemas.PolicyWarn)
	m, err := f.st.LoadManifest(context.Background(), testExp)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := f.log.loadEvidence(context.Background(), m, "a_one")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := newRecord(m, ev, *request("ana", ""), f.log.d.Now())
	if err != nil {
		t.Fatal(err)
	}
	rec.ReleaseID, rec.EvidencePath = "rel_fixed", "rel_fixed/evidence.zip"
	build := func() []byte {
		files, err := packFiles(f.runsRoot, rec, ev)
		if err != nil {
			t.Fatal(err)
		}
		b, err := buildPack(files)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	first := build()
	// Reversed run order in, same bytes out.
	for i, j := 0, len(ev.runs)-1; i < j; i, j = i+1, j-1 {
		ev.runs[i], ev.runs[j] = ev.runs[j], ev.runs[i]
	}
	if second := build(); !bytes.Equal(first, second) {
		t.Fatal("same inputs gave different zip bytes")
	}
	rec.Approver = "bob"
	if third := build(); bytes.Equal(first, third) {
		t.Fatal("a different record gave the same zip bytes")
	}
}

// TestCreateRefusesChangedReviewSnapshot is the regression test for audit
// finding 1-A-03: a reviewed analysis records the digest of its inputs, and
// a superseding review written after it makes release creation refuse that
// analysis instead of packaging its ship action with the newer review.
func TestCreateRefusesChangedReviewSnapshot(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, schemas.ActionShip, "")
	if _, err := f.st.AddReview(ctx, testExp, &schemas.ReviewRequest{RunID: "r_t01_b", Decision: schemas.ReviewPass, Reason: "looks right", Reviewer: "ana"}); err != nil {
		t.Fatalf("AddReview: %v", err)
	}
	runs, err := f.st.SealedRuns(ctx, testExp, schemas.GradeSourceReviewed)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := utils.AnalysisInputsDigest(runs)
	if err != nil {
		t.Fatal(err)
	}
	a := &schemas.Analysis{AnalysisID: "a_rv", ExpID: testExp, GradeSource: schemas.GradeSourceReviewed, CreatedAt: time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC),
		Verdict: schemas.Verdict{Decision: schemas.DecisionImproved, Action: schemas.ActionShip, ManifestHash: f.m.Hash,
			Effect: &schemas.Effect{ArmA: "A", ArmB: "B"}}, InputsDigest: digest}
	f.digest(a)
	if err := f.st.WriteAnalysis(ctx, a, nil); err != nil {
		t.Fatalf("WriteAnalysis: %v", err)
	}
	m, err := f.st.LoadManifest(ctx, testExp)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := f.log.loadEvidence(ctx, m, "a_rv")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkRelease(m, ev, ""); err != nil {
		t.Fatalf("unchanged inputs refused: %v", err)
	}
	if _, err := f.st.AddReview(ctx, testExp, &schemas.ReviewRequest{RunID: "r_t01_b", Decision: schemas.ReviewFail, Reason: "on second look the diff is empty", Reviewer: "bo"}); err != nil {
		t.Fatalf("superseding AddReview: %v", err)
	}
	_, err = f.log.Create(ctx, &schemas.ReleaseRequest{ExpID: testExp, AnalysisID: "a_rv", Approver: "ana"})
	var refused *RefusedError
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "changed after analysis a_rv") {
		t.Fatalf("err = %v, want a refusal naming the changed inputs", err)
	}
}

// Audit 1-E-01: a sealed aborted_infra attempt stays in seals.log while only
// counted runs are packed. The pack carries that attempt's seal under chain/
// so the whole prev_seal chain verifies from the zip alone.
func TestEvidencePackCarriesSupersededSeals(t *testing.T) {
	f := newFixture(t, schemas.ActionShip, schemas.PolicyAllow)
	ctx := context.Background()
	sl := f.m.Slots[0]
	sl.ExpID = testExp
	aborted := &schemas.Run{RunID: "r_t01_a_retry", ExpID: testExp, SlotID: sl.SlotID, PairID: sl.PairID,
		Attempt: 2, Stage: schemas.StageSeal, ManifestHash: f.m.Hash, Outcome: schemas.OutcomeAbortedInfra}
	if _, err := f.st.NewRun(ctx, &sl, aborted); err != nil {
		t.Fatalf("NewRun: %v", err)
	}
	if _, err := f.st.Seal(ctx, aborted); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	rec, err := f.log.Create(ctx, request("ana", ""))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	b, err := f.log.Evidence(ctx, rec.ReleaseID)
	if err != nil {
		t.Fatalf("Evidence: %v", err)
	}
	_, files := readZip(t, b)
	if _, ok := files["runs/r_t01_a_retry/seal.json"]; ok {
		t.Fatal("superseded attempt packed as a counted run")
	}
	prev := ""
	for _, line := range strings.Split(strings.TrimSpace(string(files["seals.log"])), "\n") {
		id, hash, _ := strings.Cut(line, " ")
		seal, ok := files["runs/"+id+"/seal.json"]
		if !ok {
			seal, ok = files["chain/"+id+".seal.json"]
		}
		if !ok {
			t.Fatalf("pack has no seal for chain link %s", id)
		}
		if got := utils.SHA256Bytes(seal); got != hash {
			t.Fatalf("seal of %s hashes to %s, log %s", id, got, hash)
		}
		var sr schemas.SealRecord
		if err := json.Unmarshal(seal, &sr); err != nil {
			t.Fatal(err)
		}
		if sr.PrevSeal != prev {
			t.Fatalf("run %s prev_seal %q, want %q", id, sr.PrevSeal, prev)
		}
		prev = hash
	}
	if !strings.Contains(string(files["README.txt"]), "chain/<run_id>.seal.json") {
		t.Fatal("README does not describe chain seals")
	}
}

// Audit qc2-01-03: an analysis saved before inputs_digest existed has no
// snapshot to tie its action to today's reviews, so it is never released.
func TestCreateRefusesLegacyAnalysisWithoutDigest(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, schemas.ActionShip, "")
	a := &schemas.Analysis{AnalysisID: "a_legacy", ExpID: testExp, GradeSource: schemas.GradeSourceReviewed, CreatedAt: time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC),
		Verdict: schemas.Verdict{Decision: schemas.DecisionImproved, Action: schemas.ActionShip, ManifestHash: f.m.Hash,
			Effect: &schemas.Effect{ArmA: "A", ArmB: "B"}}}
	if err := f.st.WriteAnalysis(ctx, a, nil); err != nil {
		t.Fatalf("WriteAnalysis: %v", err)
	}
	_, err := f.log.Create(ctx, &schemas.ReleaseRequest{ExpID: testExp, AnalysisID: "a_legacy", Approver: "ana"})
	var refused *RefusedError
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "no inputs_digest") {
		t.Fatalf("err = %v, want a refusal naming the missing snapshot", err)
	}
}
