package dataset

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/pii"
)

func TestStringsAcceptsOneOrMany(t *testing.T) {
	var r Row
	if err := json.Unmarshal([]byte(`{"input":"q","expected":"30","wrong_answers":["14","7"]}`), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Expected) != 1 || r.Expected[0] != "30" || len(r.WrongAnswers) != 2 {
		t.Fatalf("%+v", r)
	}
	if err := json.Unmarshal([]byte(`{"expected":{"a":1}}`), &r); err == nil {
		t.Fatal("object accepted")
	}
}

func TestCleanChecksAndRedacts(t *testing.T) {
	bad := []Row{
		{},
		{Input: "a", Messages: []Message{{Role: "user", Content: "b"}}},
		{Messages: []Message{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "hello"}}},
		{Input: "a", Metadata: map[string]any{"tier": map[string]any{"x": 1}}},
		{Input: "a", Attachments: []Attachment{{Name: "../x", Text: "t"}}},
		{Input: "a", Attachments: []Attachment{{Name: "x.bin", ContentBase64: "!!"}}},
	}
	for i, r := range bad {
		if _, msg := Clean(r, i+1, pii.Redactor{}, nil); msg == "" {
			t.Errorf("row %d accepted: %+v", i, r)
		}
	}
	rep := NewPIIReport(pii.ModeRedact)
	r := Row{ID: "t1", Messages: []Message{{Role: "user", Content: "I'm ann@example.com, card 4242 4242 4242 4242"}},
		Expected: Strings{"Refund issued"}, Metadata: map[string]any{"tier": "enterprise", "owner": "bob@example.com", "n": 3.0},
		Attachments: []Attachment{{Name: "log.txt", Text: "call +1 415 555 0132"}, {Name: "img.png", ContentBase64: base64.StdEncoding.EncodeToString([]byte{1, 2})}}}
	out, msg := Clean(r, 1, pii.Redactor{Policy: pii.Policy{Mode: pii.ModeRedact}, Key: []byte("k")}, rep)
	rep.Finish()
	if msg != "" {
		t.Fatal(msg)
	}
	if strings.Contains(out.Messages[0].Content, "ann@") || strings.Contains(out.Metadata["owner"].(string), "bob") || strings.Contains(out.Attachments[0].Text, "555") {
		t.Fatalf("not redacted: %+v", out)
	}
	if rep.Rows != 1 || rep.Kinds["email"] != 2 || rep.Kinds["card"] != 1 || rep.Kinds["phone"] != 1 || len(rep.Unscanned) != 1 || !strings.HasPrefix(rep.Summary, "Redacted") {
		t.Fatalf("report %+v", rep)
	}
	if _, msg := Clean(Row{Input: "mail me at a@b.io"}, 1, pii.Redactor{Policy: pii.Policy{Mode: pii.ModeBlock}}, NewPIIReport(pii.ModeBlock)); !strings.Contains(msg, "block") {
		t.Fatalf("block: %q", msg)
	}
}

func TestQuestionAndExtraFiles(t *testing.T) {
	r := Row{Messages: []Message{{Role: "user", Content: "Order late"}, {Role: "assistant", Content: "Which order?"}, {Role: "user", Content: "A-17"}},
		Metadata: map[string]any{"category": "shipping"}, Attachments: []Attachment{{Name: "a.txt", Text: "x"}}}
	q := Question(r)
	if !strings.Contains(q, "**assistant:** Which order?") || !strings.HasSuffix(q, "A-17") {
		t.Fatalf("question %q", q)
	}
	if ScaffoldRow(r, 2).Category != "shipping" {
		t.Fatal("category from metadata")
	}
	files, err := ExtraFiles(r)
	if err != nil || len(files) != 3 || files[2].Path != "repo/a.txt" {
		t.Fatalf("files %+v %v", files, err)
	}
}

func TestVersionLog(t *testing.T) {
	s := Open(t.TempDir())
	now := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	sha, b, _ := RowSHA(Row{Input: "a"})
	if err := s.PutRow("b", sha, b); err != nil {
		t.Fatal(err)
	}
	v1, err := s.Record("b", map[string]string{"t1": sha, "t2": sha}, []string{"t2", "t1"}, nil, nil, "ci", "api", now)
	if err != nil || v1.N != 1 || v1.Tasks != 2 || v1.Added[0] != "t1" {
		t.Fatalf("v1 %+v %v", v1, err)
	}
	if v, _ := s.Record("b", nil, nil, nil, nil, "", "", now); v != nil {
		t.Fatal("empty change recorded")
	}
	v2, _ := s.Record("b", nil, nil, nil, []string{"t2"}, "ci", "api", now)
	if v2.N != 2 || v2.Tasks != 1 || v2.SHA256 == v1.SHA256 {
		t.Fatalf("v2 %+v", v2)
	}
	snap, _ := s.Snapshot("b", 1)
	cur, _ := s.Snapshot("b", 0)
	if len(snap.Tasks) != 2 || len(cur.Tasks) != 1 {
		t.Fatalf("snapshots %v %v", snap, cur)
	}
	row, err := s.Row("b", sha)
	if err != nil || row.Input != "a" {
		t.Fatalf("row %v %v", row, err)
	}
	if _, err := s.Row("b", "../../x"); err == nil {
		t.Fatal("bad sha read")
	}
	vs, _ := s.Versions("b")
	if len(vs) != 2 {
		t.Fatalf("versions %v", vs)
	}
}
