package scaffold

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

func mapRows(t *testing.T, format, data string, m *RowMapping) ([]Row, []RowProblem, []map[string]any) {
	t.Helper()
	recs, lines, err := ReadRecords(format, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	rows, problems := MapRows(recs, lines, format == RowFormatTickets, m)
	return rows, problems, recs
}

func TestRowMapperLangSmithNested(t *testing.T) {
	data := `{"inputs":{"question":"What is the refund window?"},"outputs":{"answer":"30 days"},"metadata":{"Tier":"enterprise","language":"en"}}
{"inputs":{"question":"Do you ship to Canada?","context":"ignored"},"outputs":{"answer":"Yes"},"metadata":{"tier":"free","language":"fr"}}
`
	rows, problems, recs := mapRows(t, RowFormatJSONL, data, nil)
	if len(problems) != 0 || len(rows) != 2 {
		t.Fatalf("rows %+v problems %+v", rows, problems)
	}
	if rows[0].Question != "What is the refund window?" || rows[0].Expected[0] != "30 days" {
		t.Fatalf("row 0 = %+v", rows[0])
	}
	if rows[0].Metadata["tier"] != "enterprise" || rows[1].Metadata["language"] != "fr" {
		t.Fatalf("metadata = %v %v", rows[0].Metadata, rows[1].Metadata)
	}
	m := DetectMapping(recs)
	if m.Question != "inputs.question" || m.Expected != "outputs.answer" {
		t.Fatalf("detected = %+v", m)
	}
	cols := Columns(recs)
	var paths []string
	for _, c := range cols {
		paths = append(paths, c.Path)
	}
	if !strings.Contains(strings.Join(paths, ","), "inputs.context") || !strings.Contains(strings.Join(paths, ","), "metadata.tier") {
		t.Fatalf("columns = %v", paths)
	}
	// An explicit mapping wins over detection.
	rows, problems, _ = mapRows(t, RowFormatJSONL, data, &RowMapping{Question: "inputs.context", Expected: "outputs.answer"})
	if len(rows) != 1 || rows[0].Question != "ignored" || len(problems) != 1 {
		t.Fatalf("mapped rows %+v problems %+v", rows, problems)
	}
}

func TestRowMapperOpenAIEvalsChat(t *testing.T) {
	data := `{"input":[{"role":"system","content":"You are Acme support."},{"role":"user","content":"Hi"},{"role":"assistant","content":"Hello, how can I help?"},{"role":"user","content":[{"type":"text","text":"Is this cracked?"},{"type":"image_url","image_url":{"url":"https://cdn.example.com/p/1.png"}}]}],"ideal":"Yes, we will replace it.","turns":["Thanks, how long will it take?"]}
{"messages":[{"role":"human","content":"What is 2+2?"},{"role":"ai","content":"4"}]}
{"messages":[{"role":"wizard","content":"x"}],"ideal":"y"}
`
	rows, problems, _ := mapRows(t, RowFormatJSONL, data, nil)
	if len(rows) != 2 || len(problems) != 1 || !strings.Contains(problems[0].Message, "wizard") {
		t.Fatalf("rows %+v problems %+v", rows, problems)
	}
	r := rows[0]
	if len(r.Messages) != 4 || r.Messages[0].Role != "system" || r.Question != "Is this cracked?" || r.Expected[0] != "Yes, we will replace it." {
		t.Fatalf("chat row = %+v", r)
	}
	if len(r.UserTurns) != 1 || len(r.Attachments) != 1 || r.Attachments[0].URL != "https://cdn.example.com/p/1.png" {
		t.Fatalf("turns %v attachments %+v", r.UserTurns, r.Attachments)
	}
	// A conversation ending with the assistant's reply carries its answer;
	// one user message left is a plain question.
	if rows[1].Question != "What is 2+2?" || rows[1].Expected[0] != "4" || rows[1].Messages != nil {
		t.Fatalf("fine-tune row = %+v", rows[1])
	}
}

func TestRowMapperCSVMetadataColumns(t *testing.T) {
	data := "question,answer,metadata:tier,metadata.intent,locale\nWhere is my order?,In transit,enterprise,order_status,de\n"
	rows, problems, _ := mapRows(t, RowFormatCSV, data, &RowMapping{Metadata: []string{"locale"}})
	if len(problems) != 0 || len(rows) != 1 {
		t.Fatalf("rows %+v problems %+v", rows, problems)
	}
	md := rows[0].Metadata
	if md["tier"] != "enterprise" || md["intent"] != "order_status" || md["locale"] != "de" {
		t.Fatalf("metadata = %v", md)
	}
	if err := (&RowMapping{Metadata: []string{"-"}}).Validate(); err == nil {
		t.Fatal("bad metadata column accepted")
	}
}

func TestImportRowsChatTask(t *testing.T) {
	rows := []Row{{Line: 1, Question: "Is this cracked?", Expected: []string{"replace"}, Match: "contains_all",
		Messages:    []schemas.ChatMessage{{Role: "system", Content: "Be kind."}, {Role: "user", Content: "Is this cracked?"}},
		UserTurns:   []string{"How long?"},
		Attachments: []RowAttachment{{Name: "photo.png"}, {Name: "manual", URL: "https://example.com/manual.pdf"}},
		Metadata:    map[string]string{"tier": "pro"}}}
	if _, problems := ImportRows(rows, RowTaskOptions{}); len(problems) != 1 || !strings.Contains(problems[0].Message, "not uploaded") {
		t.Fatalf("missing upload: %+v", problems)
	}
	tasks, problems := ImportRows(rows, RowTaskOptions{Uploads: map[string]Upload{"photo.png": {Data: []byte("\x89PNG")}}})
	if len(problems) != 0 || len(tasks) != 1 {
		t.Fatalf("tasks %+v problems %+v", tasks, problems)
	}
	tk := tasks[0]
	if tk.Turns != 2 || tk.Attachments != 2 || tk.GradeOn != "final" || tk.Metadata["tier"] != "pro" {
		t.Fatalf("preview = %+v", tk)
	}
	var cf schemas.ChatFile
	if err := json.Unmarshal([]byte(fileOf(t, tk.Files, "chat.json")), &cf); err != nil {
		t.Fatal(err)
	}
	if err := cf.Validate(); err != nil || len(cf.Messages) != 2 || cf.UserTurns[0] != "How long?" || cf.Attachments[0].Path != "attachments/photo.png" || cf.Attachments[0].MediaType != "image/png" {
		t.Fatalf("chat file = %+v (%v)", cf, err)
	}
	if fileOf(t, tk.Files, "repo/attachments/photo.png") != "\x89PNG" {
		t.Fatal("uploaded attachment not stored with the task")
	}
	var ty struct {
		Metadata map[string]string  `yaml:"metadata"`
		Chat     *schemas.ChatInput `yaml:"chat"`
	}
	if err := yaml.Unmarshal([]byte(fileOf(t, tk.Files, "task.yaml")), &ty); err != nil {
		t.Fatal(err)
	}
	if ty.Metadata["tier"] != "pro" || ty.Chat == nil || ty.Chat.File != "chat.json" || ty.Chat.UserTurns != 1 {
		t.Fatalf("task.yaml = %+v", ty)
	}
	if got := fileOf(t, tk.Files, "instruction.md"); got != "system: Be kind.\n\nuser: Is this cracked?\n" {
		t.Fatalf("instruction = %q", got)
	}
	// An exact answer over the whole conversation can never match.
	rows[0].GradeOn, rows[0].Match = schemas.ChatGradeTranscript, ""
	if _, problems := ImportRows(rows, RowTaskOptions{Uploads: map[string]Upload{"photo.png": {Data: []byte("x")}}}); len(problems) != 1 || !strings.Contains(problems[0].Message, "whole conversation") {
		t.Fatalf("transcript + exact: %+v", problems)
	}
}

func TestAttachmentName(t *testing.T) {
	for in, want := range map[string]string{"../../etc/passwd": "passwd", "my photo (1).PNG": "my_photo_1_.PNG", "..": "", "C:\\x\\y.pdf": "y.pdf"} {
		if got := AttachmentName(in); got != want {
			t.Errorf("AttachmentName(%q) = %q, want %q", in, got, want)
		}
	}
}
