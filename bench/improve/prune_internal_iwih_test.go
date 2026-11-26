package improve

import (
	"slices"
	"strings"
	"testing"
)

// The opened tracker judges only the incumbent's own practice runs.
func TestTrackOpened(t *testing.T) {
	sk, other := "/a/skills/x/SKILL.md", "/a/skills/y/SKILL.md"
	rec := &Record{}
	rd := &Round{N: 1, Parent: Version{Number: 4, Bundle: Bundle{sk: "x", other: "y", "/a/AGENTS.md": "n"}}}
	trackOpened(rec, rd, nil)
	trackOpened(rec, rd, &EvidenceDoc{Items: []EvidenceItem{{Version: 3, Opened: []string{sk}}}})
	if rec.SkillsOpened != nil {
		t.Fatalf("judged without the incumbent's runs: %v", rec.SkillsOpened)
	}
	trackOpened(rec, rd, &EvidenceDoc{Items: []EvidenceItem{{Version: 4}}})
	if rec.SkillsOpened[sk] != 1 || rec.SkillsOpened[other] != 1 || len(rec.SkillsOpened) != 2 {
		t.Fatalf("first seen %v", rec.SkillsOpened)
	}
	rd.N = 3
	trackOpened(rec, rd, &EvidenceDoc{Items: []EvidenceItem{{Version: 4, Opened: []string{sk}}}})
	if rec.SkillsOpened[sk] != 3 || rec.SkillsOpened[other] != 1 {
		t.Fatalf("after round 3 %v", rec.SkillsOpened)
	}
	delete(rd.Parent.Bundle, other)
	trackOpened(rec, rd, &EvidenceDoc{Items: []EvidenceItem{{Version: 4}}})
	if _, ok := rec.SkillsOpened[other]; ok {
		t.Errorf("a removed skill is still tracked: %v", rec.SkillsOpened)
	}
}

// Prune adds delete_item for a harmful rule and retire_skill for an unused
// skill to slot 1's applied edits, skips a hint on what the improver's own
// edits touched, and never applies alone.
func TestPruneAddsHintsToAppliedEdits(t *testing.T) {
	notes, sk := "/home/agent/AGENTS.md", "/home/agent/skills/old/SKILL.md"
	ed := Editable{Files: []string{notes}, Dirs: []string{"/home/agent/skills/"}}
	pb := PlaybookObject{Next: 3, Items: []PlaybookItem{
		{ID: "pb-0001", File: notes, Section: "Checks", Text: "Bad rule.", Harmful: 3},
		{ID: "pb-0002", File: notes, Section: "Checks", Text: "Edited rule.", Harmful: 4},
	}}
	nb := BundleObject{Files: map[string]string{notes: "notes\n", sk: "---\nname: old\ndescription: d\n---\nold\n"}, Raw: []string{}}
	applied := []AppliedEdit{{Edit: Edit{Op: "edit_item", ID: "pb-0002", Text: "Edited rule."}, Result: "applied"}}
	r := &Runner{}
	rec := &Record{ID: "imp_p", SkillsOpened: map[string]int{sk: 1}, Spec: Spec{Editable: ed}}
	gotB, gotPB, gotA := r.prune(rec, &Round{N: 3}, nb, pb, applied)
	ops := map[string]string{}
	for _, a := range gotA {
		ops[a.Op+" "+a.ID+a.Path] = a.Result
	}
	if ops["delete_item pb-0001"] != "applied" || ops["retire_skill "+sk] != "applied" {
		t.Fatalf("applied %+v", gotA)
	}
	if _, ok := ops["delete_item pb-0002"]; ok {
		t.Errorf("a hint on the improver's own edit was added")
	}
	if _, ok := gotB.Files[sk]; ok {
		t.Errorf("skill not retired")
	}
	for _, it := range gotPB.Items {
		if it.ID == "pb-0001" {
			t.Errorf("harmful rule kept")
		}
	}
	// Round 2: the skill had only one round without an opening.
	_, _, gotA = r.prune(rec, &Round{N: 2}, nb, PlaybookObject{Items: []PlaybookItem{}}, applied)
	if len(gotA) != 1 {
		t.Errorf("hints in round 2: %+v", gotA)
	}
}

// After a screen, a kept idea's playbook counters take the flips against
// the incumbent and its version points at the updated playbook.
func TestUpdateCountersAfterScreen(t *testing.T) {
	r := &Runner{Objects: Objects{Root: t.TempDir()}}
	pb := PlaybookObject{Next: 2, Items: []PlaybookItem{{ID: "pb-0001", File: "/n", Text: "rule"}}}
	h, err := r.Objects.Put(ObjPlaybook, pb)
	if err != nil {
		t.Fatal(err)
	}
	c := &CandidateRun{ID: "c1", Version: &Version{PlaybookObj: h},
		Edits: []AppliedEdit{{Edit: Edit{Op: "add_item", ID: "pb-0001", PredictsFixed: []string{"t1", "t2", "t3"}}, Result: "applied"}}}
	dev := map[string]map[string]float64{"c1": {"t1": 1, "t2": 0, "t3": 1}}
	inc := map[string]float64{"t1": 0, "t2": 1, "t3": 1}
	if err := r.updateCounters([]string{"c1"}, map[string]*CandidateRun{"c1": c}, dev, inc); err != nil {
		t.Fatal(err)
	}
	if c.Version.PlaybookObj == h {
		t.Fatal("playbook not updated")
	}
	var got PlaybookObject
	if err := r.Objects.Get(c.Version.PlaybookObj, ObjPlaybook, &got); err != nil {
		t.Fatal(err)
	}
	if got.Items[0].Helpful != 1 || got.Items[0].Harmful != 1 {
		t.Errorf("counters %+v", got.Items[0])
	}
}

// The eval-cue gate has no exemption: the neutral markers pass it on a
// normal edit, and a rule that names the product still trips it.
func TestEvalCueIgnoresManagedBlockMarkers(t *testing.T) {
	block := renderBlock([]PlaybookItem{{ID: "pb-0001", Section: "Checks", Text: "Run the tests before you finish."}})
	if g := evalCueGate(block); g.Fired {
		t.Errorf("a normal edit fired the gate: %+v", g)
	}
	cue := renderBlock([]PlaybookItem{{ID: "pb-0001", Section: "Checks", Text: "This is a Heimdall run, so be careful."}})
	if g := evalCueGate(cue); !g.Fired {
		t.Error("a rule that names the product did not fire the gate")
	}
	if g := evalCueGate("<!-- heimdall:rules begin -->\n1. [pb-0001] Read the code.\n<!-- heimdall:rules end -->\n"); !g.Fired {
		t.Error("the old markers no longer fire the gate: an exemption is left")
	}
}

// cueWords are the eval-cue gate's own list plus the words the markers and
// every other text Heimdall writes into a bundle must never contain.
func cueWords() []string {
	return append(slices.Clone(evalCues), "bench", "eval", "test", "grader", "judge", "score")
}

// Text Heimdall adds to a bundle (markers, section headers, numbering, ids)
// carries no cue: after ApplyEdits, the file with the parent's text and the
// improver's own words taken out holds none of the cue words.
func TestRenderedBundleCarriesNoCue(t *testing.T) {
	notes := "/home/agent/AGENTS.md"
	parent := "# Notes\n\nBe careful.\n"
	ed := Editable{Files: []string{notes}}
	edits := EditsFile{Edits: []Edit{
		{Op: "add_item", File: notes, Section: "Strategies", Text: "Read the module before changing it.", Evidence: []string{"dev:t01#1/A"}, Reason: "r"},
		{Op: "add_item", File: notes, Section: "Pitfalls", Text: "Keep the change small.", Evidence: []string{"dev:t02#1/A"}, Reason: "r"},
	}}
	nb, _, _, err := ApplyEdits(BundleObject{Files: map[string]string{notes: parent}, Raw: []string{}}, PlaybookObject{Items: []PlaybookItem{}}, edits, ed, DefaultLimits, "mutation", 1)
	if err != nil {
		t.Fatal(err)
	}
	added := strings.Replace(nb.Files[notes], parent, "", 1)
	if !strings.Contains(added, blockBegin) || !strings.Contains(added, blockEnd) {
		t.Fatalf("no block: %q", added)
	}
	for _, e := range edits.Edits {
		added = strings.ReplaceAll(added, e.Text, "")
		added = strings.ReplaceAll(added, "## "+e.Section, "##")
	}
	low := strings.ToLower(added)
	for _, w := range cueWords() {
		if strings.Contains(low, strings.ToLower(w)) {
			t.Errorf("text Heimdall wrote holds %q: %q", w, added)
		}
	}
	if g := evalCueGate(nb.Files[notes]); g.Fired {
		t.Errorf("the gate fired on a normal edit: %+v", g)
	}
}

// A bundle written before the neutral markers is still read: its rules are
// kept and the next render writes only the new markers.
func TestLegacyMarkersAreReadAndRewritten(t *testing.T) {
	notes := "/home/agent/AGENTS.md"
	old := "# Notes\n\n<!-- heimdall:rules begin -->\n## Pitfalls\n1. [pb-0001] Keep the change small.\n<!-- heimdall:rules end -->\n"
	pb := PlaybookObject{Next: 2, Items: []PlaybookItem{{ID: "pb-0001", File: notes, Section: "Pitfalls", Text: "Keep the change small."}}}
	edits := EditsFile{Edits: []Edit{{Op: "add_item", File: notes, Section: "Strategies", Text: "Read the module first.", Evidence: []string{"dev:t01#1/A"}, Reason: "r"}}}
	nb, npb, _, err := ApplyEdits(BundleObject{Files: map[string]string{notes: old}, Raw: []string{}}, pb, edits, Editable{Files: []string{notes}}, DefaultLimits, "mutation", 2)
	if err != nil {
		t.Fatal(err)
	}
	got := nb.Files[notes]
	if strings.Contains(got, "heimdall") || strings.Count(got, blockBegin) != 1 || strings.Count(got, blockEnd) != 1 {
		t.Fatalf("file: %q", got)
	}
	if !strings.Contains(got, "[pb-0001] Keep the change small.") || len(npb.Items) != 2 {
		t.Fatalf("old rule lost: %q %+v", got, npb.Items)
	}
}
