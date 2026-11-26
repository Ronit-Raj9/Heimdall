package improve

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const skillsDir = "/home/agent/.claude/skills/"

func editsBase() (BundleObject, PlaybookObject, Editable) {
	b := BundleObject{Files: map[string]string{
		"/home/agent/AGENTS.md": "# Notes\n\nHuman text stays.\n",
		InstructionsKey:         "Be careful.",
		"/home/agent/tmpl.md":   "Hello {{name}}.",
	}, Raw: []string{"/home/agent/AGENTS.md"}}
	ed := Editable{Files: []string{"/home/agent/AGENTS.md", "/home/agent/tmpl.md"}, Dirs: []string{skillsDir}, Instructions: true}
	return b, PlaybookObject{Next: 1}, ed
}

func ref(task string) []string { return []string{"dev:" + task + "#1/B"} }

func addItem(text string) Edit {
	return Edit{Op: "add_item", File: "/home/agent/AGENTS.md", Section: "Strategies", Text: text, Evidence: ref("t01"), Reason: "r"}
}

func apply(t *testing.T, b BundleObject, pb PlaybookObject, ed Editable, kind string, edits ...Edit) (BundleObject, PlaybookObject, []AppliedEdit, error) {
	t.Helper()
	return ApplyEdits(b, pb, EditsFile{Edits: edits}, ed, DefaultLimits, kind, 1)
}

func TestEditsRule1AtMostEight(t *testing.T) {
	b, pb, ed := editsBase()
	var es []Edit
	for i := range 9 {
		es = append(es, addItem(strings.Repeat("word ", 3)+string(rune('a'+i))+" unique rule number"))
	}
	_, _, ap, err := apply(t, b, pb, ed, "mutation", es...)
	if err != nil {
		t.Fatal(err)
	}
	if ap[8].Result != "refused" || ap[8].Why != "more than 8 edits" {
		t.Fatalf("ninth: %+v", ap[8])
	}
	if ap[0].Result != "applied" {
		t.Fatalf("first: %+v", ap[0])
	}
}

func TestEditsRule2EvidenceRequired(t *testing.T) {
	b, pb, ed := editsBase()
	e := addItem("Run the failing test first.")
	e.Evidence = nil
	_, _, ap, _ := apply(t, b, pb, ed, "mutation", e, addItem("Read the whole traceback."))
	if ap[0].Result != "refused" {
		t.Fatalf("no evidence: %+v", ap[0])
	}
	_, _, ap, err := apply(t, b, pb, ed, "null", e)
	if err != nil || ap[0].Result != "applied" {
		t.Fatalf("null improver is exempt: %+v %v", ap, err)
	}
}

func TestEditsRule3AddAndMerge(t *testing.T) {
	b, pb, ed := editsBase()
	long := "Before editing any file run the failing test once and read the full traceback carefully"
	nb, npb, ap, err := apply(t, b, pb, ed, "mutation", addItem(long))
	if err != nil || ap[0].Result != "applied" || ap[0].ID != "pb-0001" {
		t.Fatalf("add: %+v %v", ap, err)
	}
	if !strings.Contains(nb.Files["/home/agent/AGENTS.md"], "1. [pb-0001] "+long) {
		t.Fatalf("block: %q", nb.Files["/home/agent/AGENTS.md"])
	}
	near := "Before editing any file run the failing test once and read the full traceback carefully twice"
	_, npb2, ap, _ := apply(t, nb, npb, ed, "mutation", addItem(near))
	if ap[0].Result != "merged_into:pb-0001" || len(npb2.Items) != 1 || npb2.Items[0].Text != near {
		t.Fatalf("merge: %+v %+v", ap, npb2.Items)
	}
	_, _, ap, _ = apply(t, nb, npb, ed, "mutation", addItem(strings.Repeat("w ", 121)))
	if ap[0].Result != "refused" {
		t.Fatalf("too long: %+v", ap[0])
	}
	short1, _, _, _ := apply(t, b, pb, ed, "mutation", addItem("Run tests often."))
	_ = short1
}

func TestEditsRule4IDMustExist(t *testing.T) {
	b, pb, ed := editsBase()
	_, _, ap, _ := apply(t, b, pb, ed, "mutation", Edit{Op: "delete_item", ID: "pb-0042", Evidence: ref("t01"), Reason: "r"}, addItem("Keep one."))
	if ap[0].Result != "refused" {
		t.Fatalf("%+v", ap[0])
	}
	nb, npb, _, _ := apply(t, b, pb, ed, "mutation", addItem("Keep tests green."))
	nb2, npb2, ap, err := apply(t, nb, npb, ed, "mutation", Edit{Op: "edit_item", ID: "pb-0001", Text: "Keep all tests green.", Evidence: ref("t01"), Reason: "r"})
	if err != nil || ap[0].Result != "applied" || npb2.Items[0].Text != "Keep all tests green." || !strings.Contains(nb2.Files["/home/agent/AGENTS.md"], "Keep all tests green.") {
		t.Fatalf("edit: %+v %v", ap, err)
	}
	nb3, npb3, _, err := apply(t, nb2, npb2, ed, "mutation", Edit{Op: "delete_item", ID: "pb-0001", Evidence: ref("t01"), Reason: "r"})
	if err != nil || len(npb3.Items) != 0 || strings.Contains(nb3.Files["/home/agent/AGENTS.md"], "pb-0001") {
		t.Fatalf("delete: %v %+v %q", err, npb3, nb3.Files["/home/agent/AGENTS.md"])
	}
	if !strings.Contains(nb3.Files["/home/agent/AGENTS.md"], "Human text stays.") {
		t.Fatal("human text lost")
	}
}

const skillText = "---\nname: run-tests\ndescription: How to run the tests of this project.\n---\nRun pytest -q from the root.\n"

func TestEditsRule5AddSkill(t *testing.T) {
	b, pb, ed := editsBase()
	ok := Edit{Op: "add_skill", Path: skillsDir + "run-tests/SKILL.md", Text: skillText, Evidence: []string{"dev:t01#1/B", "dev:t02#1/B"}, Reason: "r"}
	nb, _, ap, err := apply(t, b, pb, ed, "mutation", ok)
	if err != nil || ap[0].Result != "applied" || nb.Files[ok.Path] != skillText {
		t.Fatalf("%+v %v", ap, err)
	}
	if !containsStr(nb.Raw, ok.Path) {
		t.Fatalf("new skill must be raw: %v", nb.Raw)
	}
	bad := []Edit{
		{Op: "add_skill", Path: "/etc/run-tests/SKILL.md", Text: skillText, Evidence: ok.Evidence},
		{Op: "add_skill", Path: skillsDir + "x/README.md", Text: skillText, Evidence: ok.Evidence},
		{Op: "add_skill", Path: skillsDir + "y/SKILL.md", Text: "no front matter", Evidence: ok.Evidence},
		{Op: "add_skill", Path: skillsDir + "z/SKILL.md", Text: skillText, Evidence: []string{"dev:t01#1/B", "dev:t01#2/B"}},
		{Op: "add_skill", Path: skillsDir + "w/SKILL.md", Text: skillText + strings.Repeat("word ", 600), Evidence: ok.Evidence},
	}
	for i, e := range bad {
		_, _, ap, _ := apply(t, b, pb, ed, "mutation", e, addItem("Keep one."))
		if ap[0].Result != "refused" {
			t.Fatalf("bad %d: %+v", i, ap[0])
		}
	}
	full := b
	full.Files = map[string]string{}
	for k, v := range b.Files {
		full.Files[k] = v
	}
	for i := range 12 {
		full.Files[skillsDir+string(rune('a'+i))+"/SKILL.md"] = skillText
	}
	_, _, ap, _ = apply(t, full, pb, ed, "mutation", ok, addItem("Keep one."))
	if ap[0].Result != "refused" {
		t.Fatalf("13th skill: %+v", ap[0])
	}
}

func TestEditsRule6EditAndRetireSkill(t *testing.T) {
	b, pb, ed := editsBase()
	p := skillsDir + "run-tests/SKILL.md"
	b.Files[p] = skillText
	nb, _, ap, err := apply(t, b, pb, ed, "mutation", Edit{Op: "edit_skill", Path: p, Text: strings.Replace(skillText, "-q", "-x", 1), Evidence: ref("t01"), Reason: "r"})
	if err != nil || ap[0].Result != "applied" || !strings.Contains(nb.Files[p], "-x") {
		t.Fatalf("edit: %+v %v", ap, err)
	}
	nb, _, ap, err = apply(t, b, pb, ed, "mutation", Edit{Op: "retire_skill", Path: p, Evidence: ref("t01"), Reason: "r"})
	if err != nil || ap[0].Result != "applied" {
		t.Fatalf("retire: %+v %v", ap, err)
	}
	if _, ok := nb.Files[p]; ok {
		t.Fatal("retired skill still in the bundle")
	}
	_, _, ap, _ = apply(t, b, pb, ed, "mutation", Edit{Op: "retire_skill", Path: skillsDir + "nope/SKILL.md", Evidence: ref("t01")}, addItem("Keep one."))
	if ap[0].Result != "refused" {
		t.Fatalf("missing: %+v", ap[0])
	}
}

func TestEditsRule7RewriteOnlyOnRestart(t *testing.T) {
	b, pb, ed := editsBase()
	text := "# New notes\n\n<!-- heimdall:rules begin -->\n## Pitfalls\n1. [pb-0009] Never skip the tests.\n<!-- heimdall:rules end -->\n"
	e := Edit{Op: "rewrite_file", File: "/home/agent/AGENTS.md", Text: text, Evidence: ref("t01"), Reason: "r"}
	_, _, ap, _ := apply(t, b, pb, ed, "mutation", e, addItem("Keep one."))
	if ap[0].Result != "refused" {
		t.Fatalf("mutation rewrite: %+v", ap[0])
	}
	nb, npb, ap, err := apply(t, b, pb, ed, "restart", e)
	if err != nil || ap[0].Result != "applied" {
		t.Fatalf("restart: %+v %v", ap, err)
	}
	if len(npb.Items) != 1 || npb.Items[0].ID != "pb-0009" || npb.Items[0].Section != "Pitfalls" || npb.Items[0].Text != "Never skip the tests." {
		t.Fatalf("parsed: %+v", npb.Items)
	}
	if !strings.HasPrefix(nb.Files["/home/agent/AGENTS.md"], "# New notes") {
		t.Fatalf("file: %q", nb.Files["/home/agent/AGENTS.md"])
	}
}

func TestEditsRule8SizeLimit(t *testing.T) {
	b, pb, ed := editsBase()
	// The block of AGENTS.md is above 80% of its 1,500 word cap, so the set
	// must remove at least as many words as it adds.
	for i := range 11 {
		pb.Items = append(pb.Items, PlaybookItem{ID: fmt.Sprintf("pb-%04d", i+1), File: "/home/agent/AGENTS.md", Section: "Strategies", Text: fmt.Sprintf("rule %d ", i) + strings.Repeat("word ", 113)})
	}
	pb.Next = 12
	b.Files["/home/agent/AGENTS.md"] += "\n" + renderBlock(pb.Items)
	nb, _, ap, err := apply(t, b, pb, ed, "mutation", addItem("Add a short new rule here."))
	if err == nil || err.Error() != "over the size limit" {
		t.Fatalf("expected over the size limit, got %v %+v", err, ap)
	}
	if nb.Files["/home/agent/AGENTS.md"] != b.Files["/home/agent/AGENTS.md"] {
		t.Fatal("a refused candidate must leave the bundle as it was")
	}
	// Removing as many words as it adds is allowed.
	_, _, ap, err = apply(t, b, pb, ed, "mutation", Edit{Op: "delete_item", ID: "pb-0002", Evidence: ref("t01")}, addItem("Add a short new rule here."))
	if err != nil || ap[1].Result != "applied" {
		t.Fatalf("delete then add: %v %+v", err, ap)
	}
}

func TestEditsRule9NoNewPlaceholder(t *testing.T) {
	b, pb, ed := editsBase()
	e := Edit{Op: "add_item", File: "/home/agent/tmpl.md", Section: "Checks", Text: "Greet {{user}} first.", Evidence: ref("t01"), Reason: "r"}
	_, _, ap, _ := apply(t, b, pb, ed, "mutation", e, addItem("Keep one."))
	if ap[0].Result != "refused" {
		t.Fatalf("%+v", ap[0])
	}
	e.File = "/home/agent/AGENTS.md" // raw file: braces are plain text
	_, _, ap, _ = apply(t, b, pb, ed, "mutation", e)
	if ap[0].Result != "applied" {
		t.Fatalf("raw: %+v", ap[0])
	}
}

func TestEditsRule10NothingApplied(t *testing.T) {
	b, pb, ed := editsBase()
	_, _, _, err := apply(t, b, pb, ed, "mutation", Edit{Op: "delete_item", ID: "pb-0099", Evidence: ref("t01")})
	if err == nil || err.Error() != "the improver changed nothing" {
		t.Fatalf("%v", err)
	}
}

func TestEditsUnknownOpAndFile(t *testing.T) {
	b, pb, ed := editsBase()
	e := addItem("Rule.")
	e.File = "/etc/passwd"
	_, _, ap, _ := apply(t, b, pb, ed, "mutation", Edit{Op: "drop_table", Evidence: ref("t01")}, e, addItem("Keep one."))
	if ap[0].Result != "refused" || ap[1].Result != "refused" {
		t.Fatalf("%+v", ap)
	}
}

func TestEditsIsTheDefault(t *testing.T) {
	if DefaultEditApplier == nil {
		t.Fatal("DefaultEditApplier is nil")
	}
}

func TestRenderBlockGolden(t *testing.T) {
	items := []PlaybookItem{
		{ID: "pb-0003", Section: "Strategies", Text: "Before editing, run the failing test once and read the full traceback."},
		{ID: "pb-0007", Section: "Pitfalls", Text: "`pytest -x` hides later failures; run the whole file before finishing."},
		{ID: "pb-0008", Section: "Strategies", Text: "Read the module before changing it."},
	}
	got := renderBlock(items)
	golden := filepath.Join("testdata", "render_block_w4505.golden")
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func containsStr(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestKeepKnownRefs(t *testing.T) {
	b, pb, ed := editsBase()
	e := EditsFile{Edits: []Edit{addItem("Run the tests."), addItem("Read the traceback.")}}
	e.Edits[1].Evidence = []string{"dev:made-up#1/B"}
	e = KeepKnownRefs(e, map[string]bool{"dev:t01#1/B": true})
	_, _, ap, err := ApplyEdits(b, pb, e, ed, DefaultLimits, "mutation", 1)
	if err != nil || ap[0].Result != "applied" || ap[1].Result != "refused" {
		t.Fatalf("%v %+v", err, ap)
	}
}
