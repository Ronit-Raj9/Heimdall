package improve

import (
	"strings"
	"testing"
)

const bundleDoc = "id: a\nfiles:\n  /n.md: |\n    one\n    two\n  /fixed: keep\n"

func TestBundleRoundTrip(t *testing.T) {
	ed := Editable{Files: []string{"/n.md"}, Dirs: []string{"/skills/"}}
	b, err := ExtractBundle([]byte(bundleDoc), ed)
	if err != nil || len(b) != 1 || b["/n.md"] != "one\ntwo\n" {
		t.Fatalf("extract %v %v", b, err)
	}
	nb := Bundle{"/n.md": "one\nthree\n", "/skills/s/SKILL.md": "do it\n"}
	doc, err := ApplyBundle([]byte(bundleDoc), ed, nb)
	if err != nil || !strings.Contains(string(doc), "keep") {
		t.Fatalf("apply %s %v", doc, err)
	}
	back, _ := ExtractBundle(doc, ed)
	if back.Hash() != nb.Hash() || back.Hash() == b.Hash() {
		t.Error("hash")
	}
	if _, err := ApplyBundle([]byte(bundleDoc), ed, Bundle{"/fixed": "x"}); err == nil {
		t.Error("non-editable path applied")
	}
	d := Diff(b, nb)
	if len(d) != 2 || d[0].Path != "/n.md" || d[0].Added != 1 || d[0].Gone != 1 || d[1].Status != "added" {
		t.Fatalf("diff %+v", d)
	}
	if got := AddedText(d); got != "three\ndo it\n" {
		t.Errorf("added %q", got)
	}
}
