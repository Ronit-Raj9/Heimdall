package calibration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// item runs: pos/neg human items, the judge right on okPos and okNeg of
// them and wrong on the rest, one vote each, identical repeat, no flips.
func itemRuns(pos, okPos, neg, okNeg int) []ItemRun {
	var out []ItemRun
	add := func(lab, verdict string, k int) {
		ans := lab + "-" + verdict + "-" + string(rune('a'+k%26)) + strings.Repeat("x", k)
		it := schemas.CalibrationItem{ID: ItemID("t1", ans), Task: "t1", Answer: ans, Label: lab, Labeller: "ana", Source: schemas.CalibrationSourceLabel}
		out = append(out, ItemRun{
			Item: Labelled{Item: it, Label: lab, Labellers: []string{"ana"}}, Category: "single-hop", Template: "qa-v1-answer",
			Votes: []string{verdict}, Repeat: []string{verdict},
			Perturbed: map[string][]string{PerturbWhitespace: {verdict}, PerturbPreamble: {verdict}},
		})
	}
	for k := 0; k < pos; k++ {
		v := c
		if k >= okPos {
			v = i
		}
		add(lc, v, k)
	}
	for k := 0; k < neg; k++ {
		v := i
		if k >= okNeg {
			v = c
		}
		add(li, v, k)
	}
	return out
}

func testRecord(pos, okPos, neg, okNeg int, at time.Time) *schemas.CalibrationRecord {
	rec := &schemas.CalibrationRecord{
		Suite: "qa", SuiteVersion: "1.0.0", Provider: "openai", Model: "judge-model", PromptVersion: "qa-v1",
		Templates: map[string]string{"qa-v1-answer": "aaa", "qa-v1-rubric": "bbb"}, Votes: 1,
		ServedModels: []string{"judge-model-2026-01-01"}, SetSHA256: "set1", CreatedAt: at,
	}
	rec.PromptSHA256 = PromptHash(rec.Templates)
	Build(rec, itemRuns(pos, okPos, neg, okNeg), 0)
	return rec
}

func TestBuildCounts(t *testing.T) {
	runs := itemRuns(10, 8, 6, 5)
	runs[0].Repeat = []string{i}                     // one repeat disagrees
	runs[1].Perturbed[PerturbPreamble] = []string{i} // the preamble flips one
	seedRun := itemRuns(1, 1, 0, 0)[0]               // a seed item, reported apart
	seedRun.Item.Seed, seedRun.Item.Item.Source = true, schemas.CalibrationSourceControl
	rec := &schemas.CalibrationRecord{Votes: 1}
	Build(rec, append(runs, seedRun), 2)
	h := rec.Human
	if rec.HumanLabels != 16 || rec.SeedItems != 1 || rec.Disputed != 2 || h.TP != 8 || h.FN != 2 || h.TN != 5 || h.FP != 1 {
		t.Fatalf("record counts %+v human %+v", rec, h)
	}
	if rec.Seed == nil || rec.Seed.Items != 1 || rec.Items[16].Source != schemas.CalibrationSourceControl {
		t.Fatalf("seed stats %+v", rec.Seed)
	}
	if rec.Consistency.RepeatAgreement.K != 16 || rec.Consistency.RepeatAgreement.N != 17 {
		t.Fatalf("repeat %+v", rec.Consistency)
	}
	var pre schemas.CalibrationPerturbation
	for _, p := range rec.Perturbations {
		if p.Name == PerturbPreamble {
			pre = p
		}
	}
	if pre.Flips.K != 1 || pre.Flips.N != 17 {
		t.Fatalf("preamble flips %+v", rec.Perturbations)
	}
	if len(rec.Categories) != 1 || rec.Labellers[0] != "ana" {
		t.Fatalf("categories %+v labellers %v", rec.Categories, rec.Labellers)
	}
}

func TestPerturb(t *testing.T) {
	if got := Perturb(PerturbWhitespace, "Max is  the\ndog"); got != "\n\nMax  is  the\n\ndog\n\n" {
		t.Fatalf("whitespace %q", got)
	}
	if got := Perturb(PerturbPreamble, "Max"); !strings.HasPrefix(got, "Sure!") || !strings.HasSuffix(got, "\n\nMax") {
		t.Fatalf("preamble %q", got)
	}
}

func TestRecordChain(t *testing.T) {
	root := t.TempDir()
	r1 := testRecord(20, 20, 20, 20, t0)
	r2 := testRecord(20, 19, 20, 20, t0.Add(time.Hour))
	if err := WriteRecord(root, r1); err != nil {
		t.Fatal(err)
	}
	if err := WriteRecord(root, r2); err != nil {
		t.Fatal(err)
	}
	if r1.PrevHash != "" || r2.PrevHash != r1.Hash || r1.Hash == "" {
		t.Fatalf("chain r1 %s/%s r2 %s", r1.PrevHash, r1.Hash, r2.PrevHash)
	}
	set, err := ReadRecords(root)
	if err != nil || !set.Verified || len(set.Valid) != 2 {
		t.Fatalf("records %+v %v", set, err)
	}
	if got := set.ForPrompt("judge-model", r1.PromptSHA256); got == nil || got.RecordID != r2.RecordID {
		t.Fatalf("ForPrompt = %+v, want the newest", got)
	}
	if cov, _ := set.Covering(JudgeID{Model: "judge-model"}, []string{"aaa"}); cov == nil || cov.RecordID != r2.RecordID {
		t.Fatalf("Covering = %+v", cov)
	}
	if cov, newest := set.Covering(JudgeID{Model: "judge-model"}, []string{"zzz"}); cov != nil || newest == nil {
		t.Fatalf("an uncovered template: %v %v", cov, newest)
	}
	if cov, newest := set.Covering(JudgeID{Model: "other"}, []string{"aaa"}); cov != nil || newest != nil {
		t.Fatal("another model is not covered")
	}
	if Ref(r2) != r2.RecordID+"@"+r2.Hash {
		t.Fatalf("ref %s", Ref(r2))
	}

	// A changed record is left out alone; the record after it still chains
	// to its log line.
	p := filepath.Join(root, Dir, RecordsDir, r1.RecordID, recordFile)
	b, _ := os.ReadFile(p)
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), `"human_labels": 40`, `"human_labels": 400`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	set, _ = ReadRecords(root)
	if set.Verified || len(set.Valid) != 1 || set.Valid[0].RecordID != r2.RecordID || !strings.Contains(set.Problems[0], "was changed") {
		t.Fatalf("tampered: %+v", set.Problems)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}

	// An unlogged record folder and a malformed line are reported.
	if err := os.MkdirAll(filepath.Join(root, Dir, RecordsDir, "cr_orphan0001"), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, Dir, RecordsLog)
	raw, _ := os.ReadFile(logPath)
	if err := os.WriteFile(logPath, append([]byte("garbage\n"), raw...), 0o644); err != nil {
		t.Fatal(err)
	}
	set, _ = ReadRecords(root)
	if len(set.Valid) != 0 || len(set.Problems) < 3 {
		t.Fatalf("broken chain must leave every later record out: %+v", set)
	}
}
