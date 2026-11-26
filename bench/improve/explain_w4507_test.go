package improve

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// explainRunner stores a two-round record: round 1 kept version 2 (its
// bundle in the object store, one idea with two edits), round 2 rejected
// version 3 (its bundle in the record, no ideas list).
func explainRunner(t *testing.T) *Runner {
	t.Helper()
	root := t.TempDir()
	r := &Runner{Root: root, Objects: Objects{Root: filepath.Join(root, "objects")}}
	v1 := Version{Ref: "agents/a.yaml@v1", Number: 1, Bundle: Bundle{"/home/agent/NOTES.md": "be careful\n"}}
	b2, err := r.Objects.Put(ObjBundle, BundleObject{Files: map[string]string{"/home/agent/NOTES.md": "be careful\nrun the tests\n", InstructionsKey: "check first"}})
	if err != nil {
		t.Fatal(err)
	}
	edits, err := r.Objects.Put(ObjEdits, EditsFile{Rationale: "Runs stopped before the tests."})
	if err != nil {
		t.Fatal(err)
	}
	cobj, err := r.Objects.Put(ObjCandidate, CandidateObject{Source: "mutation", Bundle: b2, Edits: edits, Model: "gpt-5-mini", Round: 1, Slot: 1})
	if err != nil {
		t.Fatal(err)
	}
	v2 := Version{Ref: "agents/a.yaml@v2", Number: 2, BundleObj: b2}
	v3 := Version{Ref: "agents/a.yaml@v3", Number: 3, Bundle: Bundle{"/home/agent/NOTES.md": "be careful\nrun the tests\nhurry\n"}}
	rec := &Record{
		ID:   "imp_x-abc123",
		Spec: Spec{Improver: ImproverSpec{Task: DefaultImproverTask}},
		Base: v1,
		Rounds: []*Round{
			{
				N: 1, Parent: v1, Candidate: &v2, Decision: "committed", Why: "Kept: better on 18 gate tasks.",
				Gates:  []GateResult{{Name: "improver_answer_key", Detail: "ok"}},
				Winner: "c1",
				Candidates: []CandidateRun{{ID: "c1", Kind: "mutation", Obj: cobj, Edits: []AppliedEdit{
					{Edit: Edit{Op: "add_item", ID: "pb-0001", File: "/home/agent/NOTES.md", Reason: "tests were skipped", Evidence: []string{"dev:t1#1", "dev:t2#1", "dev:t3#1"}, PredictsFixed: []string{"t1", "t2"}}, Result: "applied"},
					{Edit: Edit{Op: "rewrite_file", File: "/home/agent/OTHER.md", Reason: "unused"}, Result: "refused", Why: "not editable"},
				}}},
			},
			{N: 2, Parent: v2, Candidate: &v3, Decision: "rejected", Why: "Not kept."},
		},
	}
	if err := os.MkdirAll(r.dir(rec.ID), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.save(rec); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestExplainKeptVersionFromObjects(t *testing.T) {
	x, err := explainRunner(t).Explain("imp_x-abc123", 2)
	if err != nil {
		t.Fatal(err)
	}
	if x.Ref != "agents/a.yaml@v2" || x.ParentRef != "agents/a.yaml@v1" || x.Round != 1 || x.Decision != "committed" {
		t.Fatalf("header = %+v", x)
	}
	if x.Model != "gpt-5-mini" || x.Source != "mutation" || x.Rationale != "Runs stopped before the tests." || x.Improver != DefaultImproverTask {
		t.Fatalf("candidate fields = %+v", x)
	}
	paths := []string{}
	for _, f := range x.Files {
		paths = append(paths, f.Path+":"+f.Status)
	}
	if got := strings.Join(paths, ","); got != "/home/agent/NOTES.md:changed,/home/agent/OTHER.md:unchanged,@instructions:added" {
		t.Fatalf("files = %s", got)
	}
	notes := x.Files[0]
	if len(notes.Edits) != 1 || notes.Edits[0].EvidenceCount != 3 || notes.Edits[0].PredictsFixed != 2 || notes.Edits[0].Result != "applied" {
		t.Fatalf("notes edits = %+v", notes.Edits)
	}
	if x.Files[1].Edits[0].Result != "refused" || len(x.Files[1].Lines) != 0 {
		t.Fatalf("refused edit = %+v", x.Files[1])
	}
	b, _ := json.Marshal(x)
	for _, leak := range []string{"dev:t1#1", `"t1"`} {
		if strings.Contains(string(b), leak) {
			t.Fatalf("explain names evidence %s: %s", leak, b)
		}
	}
}

func TestExplainRecordBundleAndBase(t *testing.T) {
	r := explainRunner(t)
	x, err := r.Explain("imp_x-abc123", 3)
	if err != nil {
		t.Fatal(err)
	}
	if x.Decision != "rejected" || len(x.Files) != 2 {
		t.Fatalf("version 3 = %+v", x)
	}
	base, err := r.Explain("imp_x-abc123", 1)
	if err != nil || base.Source != "v0" || len(base.Files) != 0 || base.ParentRef != "" {
		t.Fatalf("base = %+v, %v", base, err)
	}
	if _, err := r.Explain("imp_x-abc123", 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown version err = %v", err)
	}
	if _, err := r.Explain("imp_nope-abc123", 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown run err = %v", err)
	}
}
