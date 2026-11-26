package improve

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type bundleTree struct {
	Files        map[string]string `yaml:"files"`
	FilesRaw     map[string]string `yaml:"files_raw"`
	Instructions string            `yaml:"instructions"`
}

func decodeBundleTree(t *testing.T, doc []byte) bundleTree {
	t.Helper()
	var tr bundleTree
	if err := yaml.Unmarshal(doc, &tr); err != nil {
		t.Fatal(err)
	}
	return tr
}

const rawDoc = "id: a\nfiles:\n  /n.md: \"model {{model}}\"\n  /fixed: keep\nfiles_raw:\n  /skills/old/SKILL.md: \"raw {{x}}\"\n  /frozen-raw: stay\ninstructions: Run the tests.\n"

func TestBundleRawPathsKeptRaw(t *testing.T) {
	ed := Editable{Files: []string{"/n.md"}, Dirs: []string{"/skills/"}, Instructions: true}
	b, err := ExtractBundle([]byte(rawDoc), ed)
	if err != nil {
		t.Fatal(err)
	}
	if b["/n.md"] != "model {{model}}" || b["/skills/old/SKILL.md"] != "raw {{x}}" || b[InstructionsKey] != "Run the tests." || len(b) != 3 {
		t.Fatalf("extract %v", b)
	}
	nb := Bundle{"/n.md": "model {{model}} here", "/skills/old/SKILL.md": "raw {{x}} {{y}}", "/skills/new/SKILL.md": "new {{model}}", InstructionsKey: "Run the tests twice."}
	doc, err := ApplyBundle([]byte(rawDoc), ed, nb)
	if err != nil {
		t.Fatal(err)
	}
	tr := decodeBundleTree(t, doc)
	if tr.Files["/n.md"] != "model {{model}} here" || tr.Files["/fixed"] != "keep" || len(tr.Files) != 2 {
		t.Errorf("files %v", tr.Files)
	}
	if tr.FilesRaw["/skills/old/SKILL.md"] != "raw {{x}} {{y}}" || tr.FilesRaw["/skills/new/SKILL.md"] != "new {{model}}" || tr.FilesRaw["/frozen-raw"] != "stay" || len(tr.FilesRaw) != 3 {
		t.Errorf("files_raw %v", tr.FilesRaw)
	}
	if tr.Instructions != "Run the tests twice." {
		t.Errorf("instructions %q", tr.Instructions)
	}
	back, err := ExtractBundle(doc, ed)
	if err != nil || back.Hash() != nb.Hash() {
		t.Errorf("round trip %v %v", back, err)
	}
	// Removing editable entries of either map, and the instructions.
	doc, err = ApplyBundle([]byte(rawDoc), ed, Bundle{"/n.md": "x"})
	if err != nil {
		t.Fatal(err)
	}
	tr = decodeBundleTree(t, doc)
	if _, ok := tr.FilesRaw["/skills/old/SKILL.md"]; ok || tr.FilesRaw["/frozen-raw"] != "stay" || tr.Instructions != "" {
		t.Errorf("removal %+v", tr)
	}
}

func TestBundleInstructionsRoundTripWithoutFiles(t *testing.T) {
	ed := Editable{Instructions: true}
	b, err := ExtractBundle([]byte("id: lib\nmodel: m\n"), ed)
	if err != nil || len(b) != 1 || b[InstructionsKey] != "" {
		t.Fatalf("extract %v %v", b, err)
	}
	doc, err := ApplyBundle([]byte("id: lib\nmodel: m\n"), ed, Bundle{InstructionsKey: "Be careful."})
	if err != nil {
		t.Fatal(err)
	}
	if tr := decodeBundleTree(t, doc); tr.Instructions != "Be careful." || tr.Files != nil || tr.FilesRaw != nil {
		t.Errorf("tree %+v", tr)
	}
	if _, err := ApplyBundle([]byte("id: lib\n"), Editable{}, Bundle{InstructionsKey: "x"}); err == nil {
		t.Error("instructions applied while not editable")
	}
}

func TestBundleRefusesNewPlaceholderInTemplatedFile(t *testing.T) {
	ed := Editable{Files: []string{"/n.md"}}
	_, err := ApplyBundle([]byte(rawDoc), ed, Bundle{"/n.md": "model {{model}} and {{ secret_x }}"})
	want := "/n.md would gain the template placeholder {{secret_x}}; write it in a new file instead"
	if err == nil || err.Error() != want {
		t.Fatalf("err %v, want %q", err, want)
	}
	// The same placeholder twice is a rise in its count.
	if _, err := ApplyBundle([]byte(rawDoc), ed, Bundle{"/n.md": "{{model}} {{model}}"}); err == nil || !strings.Contains(err.Error(), "{{model}}") {
		t.Errorf("second {{model}}: %v", err)
	}
}

func TestFrozenGateRawAndInstructions(t *testing.T) {
	spec := &Spec{Editable: Editable{Files: []string{"/n.md"}}}
	parent := []byte(rawDoc)
	cand := []byte(strings.Replace(strings.Replace(rawDoc, "stay", "moved", 1), "Run the tests.", "Skip the tests.", 1))
	g := frozenGate(spec, Bundle{}, parent, cand)
	joined := strings.Join(g.Evidence, "\n")
	if !g.Fired || !strings.Contains(joined, "non-editable file changed: /frozen-raw") || !strings.Contains(joined, "instructions changed") {
		t.Errorf("gate %+v", g)
	}
	spec.Editable.Instructions = true
	spec.Editable.Files = append(spec.Editable.Files, "/frozen-raw")
	if g := frozenGate(spec, Bundle{}, parent, cand); g.Fired {
		t.Errorf("editable changes fired %+v", g)
	}
}
