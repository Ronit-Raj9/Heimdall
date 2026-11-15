package release

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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
				for _, n := range []string{"seal.json", "run.json", "metrics.json", "gates.json", "grade.json"} {
					want = append(want, "runs/"+id+"/"+n)
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
	if err := f.st.WriteGateRegrade(ctx, testExp, "rg_x", "r_t01_a", info, regate); err != nil {
		t.Fatalf("WriteGateRegrade: %v", err)
	}
	a := &schemas.Analysis{AnalysisID: "a_rg", ExpID: testExp, GradeSource: "rg_x", CreatedAt: time.Date(2026, 11, 14, 11, 0, 0, 0, time.UTC),
		Verdict: schemas.Verdict{Decision: schemas.DecisionImproved, Action: schemas.ActionShip, ManifestHash: f.m.Hash,
			Effect: &schemas.Effect{ArmA: "A", ArmB: "B"}}}
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
