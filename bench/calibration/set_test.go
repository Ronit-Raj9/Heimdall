package calibration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

var t0 = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

func label(t *testing.T, task, answer, lab, who string, source string) schemas.CalibrationItem {
	t.Helper()
	it, err := NewItem(schemas.CalibrationLabelRequest{Task: task, Answer: answer, Label: lab, Labeller: who}, source, t0)
	if err != nil {
		t.Fatalf("NewItem: %v", err)
	}
	return it
}

func TestNewItemValidates(t *testing.T) {
	for _, req := range []schemas.CalibrationLabelRequest{
		{Task: "t1", Answer: "a", Label: "maybe", Labeller: "x"},
		{Task: "t1", Answer: "  ", Label: "correct", Labeller: "x"},
		{Task: "t1", Answer: "a", Label: "correct", Labeller: ""},
		{Task: "t1\nx", Answer: "a", Label: "correct", Labeller: "x"},
		{Task: "t1", Answer: "a", Label: "correct", Labeller: "a\nb"},
	} {
		if _, err := NewItem(req, schemas.CalibrationSourceLabel, t0); !errors.Is(err, ErrInput) {
			t.Fatalf("NewItem(%+v) = %v, want ErrInput", req, err)
		}
	}
	it := label(t, "t1", "Max", " Correct ", "ana", schemas.CalibrationSourceLabel)
	if it.Label != lc || it.ID != ItemID("t1", "Max") || len(it.ID) != 16 {
		t.Fatalf("item %+v", it)
	}
	if ItemID("t1", "Max") == ItemID("t2", "Max") || ItemID("t1", "Max") == ItemID("t1", "Rex") {
		t.Fatal("item ids must address task and answer")
	}
}

func TestAppendDedupesAndHashes(t *testing.T) {
	root := t.TempDir()
	s, err := LoadSet(root)
	if err != nil || s.SHA256 != "" || len(s.Items) != 0 {
		t.Fatalf("empty set %+v %v", s, err)
	}
	a := label(t, "t1", "Max", lc, "ana", schemas.CalibrationSourceLabel)
	b := label(t, "t1", "Rex", li, "ana", schemas.CalibrationSourceLabel)
	if n, err := Append(root, []schemas.CalibrationItem{a, b}); err != nil || n != 2 {
		t.Fatalf("Append = %d, %v", n, err)
	}
	if n, err := Append(root, []schemas.CalibrationItem{a}); err != nil || n != 0 {
		t.Fatalf("second Append = %d, %v; a duplicate row must be skipped", n, err)
	}
	s, err = LoadSet(root)
	if err != nil || len(s.Items) != 2 {
		t.Fatalf("set %+v %v", s, err)
	}
	want, _ := utils.SHA256File(LabelsPath(root))
	if s.SHA256 != want {
		t.Fatalf("set hash %s, file hash %s", s.SHA256, want)
	}
	rel, err := Snapshot(s)
	if err != nil || rel != filepath.Join(Dir, SetsDir, s.SHA256+".jsonl") {
		t.Fatalf("snapshot %s %v", rel, err)
	}
	if fi, err := os.Stat(filepath.Join(root, rel)); err != nil || fi.Mode().Perm()&0o222 != 0 {
		t.Fatalf("snapshot must be read-only: %v %v", fi, err)
	}
	if again, err := Snapshot(s); err != nil || again != rel {
		t.Fatalf("snapshot again %s %v", again, err)
	}
	if err := os.WriteFile(LabelsPath(root), append(s.Raw, []byte("{not json}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSet(root); err == nil || !strings.Contains(err.Error(), "line 3") {
		t.Fatalf("a bad line must be an error naming it: %v", err)
	}
}

func TestResolve(t *testing.T) {
	rows := []schemas.CalibrationItem{
		label(t, "t1", "Max", lc, "ana", schemas.CalibrationSourceLabel),
		label(t, "t1", "Max", lc, "bo", schemas.CalibrationSourceReview),
		label(t, "t1", "Rex", li, "ana", schemas.CalibrationSourceLabel),
		label(t, "t1", "Rex", lc, "bo", schemas.CalibrationSourceImport), // people disagree: disputed
		label(t, "t2", "42", lc, "author", schemas.CalibrationSourceControl),
		label(t, "t2", "41", li, "author", schemas.CalibrationSourceControl),
		label(t, "t2", "41", lc, "cy", schemas.CalibrationSourceLabel), // a person overrides the seed
	}
	human, seed, disputed := Resolve(rows)
	if disputed != 1 || len(human) != 2 || len(seed) != 1 {
		t.Fatalf("human %d seed %d disputed %d", len(human), len(seed), disputed)
	}
	if human[0].Item.Answer != "Max" || len(human[0].Labellers) != 2 || human[1].Item.Answer != "41" || human[1].Label != lc {
		t.Fatalf("human %+v", human)
	}
	if !seed[0].Seed || seed[0].Item.Answer != "42" {
		t.Fatalf("seed %+v", seed)
	}
}

func TestImportFiles(t *testing.T) {
	csv := "task,answer,label,labeller,note\nt1,\"Max, the dog\",correct,ana,ok\nt1,Rex,INCORRECT,ana,\n"
	items, err := ParseCSV([]byte(csv), "labels.csv")
	if err != nil || len(items) != 2 || items[0].Answer != "Max, the dog" || items[1].Label != li || items[0].Source != schemas.CalibrationSourceImport || items[0].Ref != "labels.csv" {
		t.Fatalf("csv %+v %v", items, err)
	}
	if _, err := ParseCSV([]byte("task,answer,label\nt1,a,correct\n"), "x.csv"); !errors.Is(err, ErrInput) {
		t.Fatalf("missing labeller column: %v", err)
	}
	jl := `{"task":"t1","answer":"Max","label":"correct","labeller":"ana"}` + "\n"
	items, err = ParseJSONL([]byte(jl), "in.jsonl", true)
	if err != nil || len(items) != 1 || items[0].ID != ItemID("t1", "Max") {
		t.Fatalf("jsonl %+v %v", items, err)
	}
	if _, err := ParseJSONL([]byte(`{"task":"t1","answer":"Max","label":"correct","labeller":"ana","extra":1}`), "in.jsonl", true); err == nil {
		t.Fatal("unknown fields must be refused")
	}
}

func TestSeedItems(t *testing.T) {
	seeds := SeedItems("t1", []string{"Max"}, []string{"Rex", ""}, t0)
	if len(seeds) != 2 || seeds[0].Label != lc || seeds[1].Label != li || seeds[0].Source != schemas.CalibrationSourceControl {
		t.Fatalf("seeds %+v", seeds)
	}
}
