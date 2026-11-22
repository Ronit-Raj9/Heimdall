package handlers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func newQASuite(t *testing.T, c *authoringClient, id string) {
	t.Helper()
	if code, body := c.do(t, "POST", "/suites", []byte(`{"id":"`+id+`","template":"qa","blank":true}`), nil); code != 201 {
		t.Fatalf("create %s: %d %s", id, code, body)
	}
}

func TestSuiteBuildRowMapperChatMetadata(t *testing.T) {
	c := newBuildClient(t)
	newQASuite(t, c, "support-chat")
	data := `{"inputs":{"messages":[{"role":"system","content":"You are Acme support."},{"role":"user","content":"My grinder arrived cracked."}]},"outputs":{"answer":"replacement"},"metadata":{"tier":"enterprise","intent":"damaged"},"follow_ups":["How long will it take?"],"files":["photo.png"]}
{"inputs":{"question":"Where is order A-1?"},"outputs":{"answer":"in transit"},"metadata":{"tier":"free","intent":"order_status"},"files":["https://cdn.example.com/a1.pdf"]}
`
	req := RowImportRequest{Format: "jsonl", Data: data, DryRun: true, Match: "contains_all"}
	code, body := c.do(t, "POST", "/suites/support-chat/rows", jsonBody(t, req), nil)
	var res RowImportResult
	if code != 200 || json.Unmarshal(body, &res) != nil {
		t.Fatalf("dry run: %d %s", code, body)
	}
	if res.Rows != 2 || res.Mapping.Messages != "inputs.messages" || res.Mapping.UserTurns != "follow_ups" || res.Mapping.Attachments != "files" || len(res.Columns) == 0 {
		t.Fatalf("detected mapping %+v columns %d rows %d", res.Mapping, len(res.Columns), res.Rows)
	}
	// photo.png is named but not uploaded.
	if len(res.Tasks) != 1 || len(res.Problems) != 1 || !strings.Contains(res.Problems[0].Message, "photo.png") {
		t.Fatalf("missing upload: %+v %+v", res.Tasks, res.Problems)
	}

	req.DryRun, req.SaveMapping = false, true
	req.Mapping = &res.Mapping
	req.Attachments = map[string]RowAttachmentUpload{"photo.png": {ContentBase64: base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n")), MediaType: "image/png"}}
	code, body = c.do(t, "POST", "/suites/support-chat/rows", jsonBody(t, req), nil)
	res = RowImportResult{}
	if code != 201 || json.Unmarshal(body, &res) != nil || len(res.Tasks) != 2 || !res.MappingSaved {
		t.Fatalf("import: %d %s", code, body)
	}
	if errs := lintErrors(t, c, "support-chat"); len(errs) > 0 {
		t.Fatalf("chat tasks do not lint clean:\n%s", strings.Join(errs, "\n"))
	}
	if res.Tasks[0].Turns != 2 || res.Tasks[0].Metadata["tier"] != "enterprise" {
		t.Fatalf("preview %+v", res.Tasks[0])
	}
	s, err := diskSuites{root: filepath.Join(c.root, "benchmarks")}.LoadSuite(t.Context(), "support-chat")
	if err != nil {
		t.Fatal(err)
	}
	chat := s.TaskByID(res.Tasks[0].ID)
	if chat == nil || chat.Chat == nil || chat.Chat.UserTurns != 1 || chat.Metadata["intent"] != "damaged" {
		t.Fatalf("loaded chat task %+v", chat)
	}
	cf, err := schemas.ReadChatFile(chat)
	if err != nil || len(cf.Messages) != 2 || cf.Attachments[0].Path != "attachments/photo.png" {
		t.Fatalf("chat file %+v %v", cf, err)
	}
	if b, err := os.ReadFile(filepath.Join(chat.Dir, "repo", "attachments", "photo.png")); err != nil || string(b) != "\x89PNG\r\n" {
		t.Fatalf("attachment stored: %q %v", b, err)
	}
	instr, _ := os.ReadFile(filepath.Join(chat.Dir, "instruction.md"))
	if strings.Contains(string(instr), "answer.txt") || !strings.Contains(string(instr), "My grinder arrived cracked.") {
		t.Fatalf("instruction is not the customer's text: %q", instr)
	}
	plain := s.TaskByID(res.Tasks[1].ID)
	if b, _ := os.ReadFile(filepath.Join(plain.Dir, "instruction.md")); string(b) != "Where is order A-1?\n" {
		t.Fatalf("plain instruction %q", b)
	}
	if _, err := os.Stat(filepath.Join(c.root, "benchmarks", "support-chat", "import-map.yaml")); err != nil {
		t.Fatal("mapping not saved")
	}

	// The saved mapping is reused: a question column renamed by the
	// mapping still maps without one in the request.
	code, body = c.do(t, "POST", "/suites/support-chat/rows", []byte(`{"format":"jsonl","data":"{\"inputs\":{\"question\":\"Q?\"},\"outputs\":{\"answer\":\"A\"}}\n","dry_run":true}`), nil)
	if code != 200 || !strings.Contains(string(body), `"messages":"inputs.messages"`) {
		t.Fatalf("saved mapping: %d %s", code, body)
	}

	// Bad uploads and mappings.
	bad := RowImportRequest{Format: "jsonl", Data: data, Attachments: map[string]RowAttachmentUpload{"x.png": {ContentBase64: "%%%"}}}
	if code, _ := c.do(t, "POST", "/suites/support-chat/rows", jsonBody(t, bad), nil); code != 400 {
		t.Fatalf("bad base64: %d", code)
	}
	bad = RowImportRequest{Format: "jsonl", Data: data, GradeOn: "middle"}
	if code, _ := c.do(t, "POST", "/suites/support-chat/rows", jsonBody(t, bad), nil); code != 400 {
		t.Fatalf("bad grade_on: %d", code)
	}
}

func TestSuiteBuildRowsAsync(t *testing.T) {
	c := newBuildClient(t)
	newQASuite(t, c, "big-faq")
	var b strings.Builder
	b.WriteString("question,answer,metadata:tier\n")
	for i := range 620 {
		fmt.Fprintf(&b, "Question number %d?,%d,t%d\n", i, i, i%3)
	}
	b.WriteString(",\n,,\nno answer here,,\n")
	// The preview lists the first 500 and counts them all.
	code, body := c.do(t, "POST", "/suites/big-faq/rows", jsonBody(t, RowImportRequest{Format: "csv", Data: b.String(), DryRun: true}), nil)
	var res RowImportResult
	if code != 200 || json.Unmarshal(body, &res) != nil || len(res.Tasks) != 500 || res.TasksTotal != 620 || len(res.Problems) != 1 {
		t.Fatalf("dry run: %d tasks %d total %d problems %d", code, len(res.Tasks), res.TasksTotal, len(res.Problems))
	}
	code, body = c.do(t, "POST", "/suites/big-faq/rows", jsonBody(t, RowImportRequest{Format: "csv", Data: b.String(), Source: "big.csv"}), nil)
	res = RowImportResult{}
	if code != 202 || json.Unmarshal(body, &res) != nil || res.Import == nil || res.Import.Total != 620 {
		t.Fatalf("async: %d %s", code, body)
	}
	var st RowImportStatus
	deadline := time.Now().Add(60 * time.Second)
	for {
		code, body = c.do(t, "GET", "/suites/big-faq/rows/imports/"+res.Import.ID, nil, nil)
		if code != 200 || json.Unmarshal(body, &st) != nil {
			t.Fatalf("status: %d %s", code, body)
		}
		if st.Status != ImportRunning || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st.Status != ImportDone || st.Written != 620 || st.ProblemCount != 1 || st.FinishedAt == nil {
		t.Fatalf("finished: %+v", st)
	}
	s, err := diskSuites{root: filepath.Join(c.root, "benchmarks")}.LoadSuite(t.Context(), "big-faq")
	if err != nil || len(s.Tasks) != 620 {
		t.Fatalf("loaded %d tasks: %v", len(s.Tasks), err)
	}
	if errs := lintErrors(t, c, "big-faq"); len(errs) > 0 {
		t.Fatalf("lint:\n%s", strings.Join(errs[:min(5, len(errs))], "\n"))
	}
	// The status survives the process: read back from disk.
	rowImports.mu.Lock()
	delete(rowImports.live, c.root+"\x00"+st.ID)
	rowImports.mu.Unlock()
	if code, body := c.do(t, "GET", "/suites/big-faq/rows/imports/"+st.ID, nil, nil); code != 200 || !strings.Contains(string(body), `"status":"done"`) {
		t.Fatalf("status from disk: %d %s", code, body)
	}
	// An import the server was running when it stopped reads as interrupted.
	st.Status, st.ID = ImportRunning, "imp-00000000beef"
	saveStatus(c.root, st)
	if code, body := c.do(t, "GET", "/suites/big-faq/rows/imports/"+st.ID, nil, nil); code != 200 || !strings.Contains(string(body), `"status":"interrupted"`) {
		t.Fatalf("interrupted: %d %s", code, body)
	}
	for _, p := range []string{"/suites/big-faq/rows/imports/imp-zz", "/suites/big-faq/rows/imports/imp-000000000000", "/suites/nope/rows/imports/" + st.ID} {
		if code, _ := c.do(t, "GET", p, nil, nil); code != 404 {
			t.Fatalf("%s: %d", p, code)
		}
	}
}
