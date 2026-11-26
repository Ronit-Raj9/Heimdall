package handlers

import (
	"encoding/json"
	"strings"
	"testing"
)

// p01-5: the agent description is the file's head comment. Saving twice
// keeps exactly one comment, and the form always gets the field.
func TestApplyFieldsDescriptionRoundTrip(t *testing.T) {
	doc := []byte("# zz-test-p01 old\nid: zz-test-p01\nadapter: command\nimage: x:1\nargv: [run]\nbudgets:\n  wall_s: 600\n  cpus: 1\n  mem_gb: 2\n")
	for _, desc := range []string{"B1", "B2"} {
		cfg, err := decodeAgent(doc, "agents/zz-test-p01.yaml")
		if err != nil {
			t.Fatal(err)
		}
		f := abFieldsOf(cfg, doc)
		f.Description = desc
		out, err := abApplyFields(doc, f)
		if err != nil {
			t.Fatal(err)
		}
		doc = out
	}
	s := string(doc)
	if strings.Count(s, "#") != 1 || !strings.Contains(s, "# B2\n") {
		t.Fatalf("want exactly one comment line \"# B2\":\n%s", s)
	}
	cfg, err := decodeAgent(doc, "agents/zz-test-p01.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f := abFieldsOf(cfg, doc)
	if f.Description != "B2" {
		t.Fatalf("description read back %q, want B2", f.Description)
	}
	f.Description = ""
	b, _ := json.Marshal(f)
	if !strings.Contains(string(b), `"description":""`) {
		t.Fatalf("an empty description must still be sent: %s", b)
	}
}

func TestFieldsReadKeyLevelComment(t *testing.T) {
	doc := []byte("# hi\nid: a\nadapter: command\nimage: x:1\nargv: [run]\nbudgets:\n  wall_s: 600\n  cpus: 1\n  mem_gb: 2\n")
	cfg, err := decodeAgent(doc, "agents/a.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := abFieldsOf(cfg, doc).Description; got != "hi" {
		t.Fatalf("description %q, want hi", got)
	}
}
