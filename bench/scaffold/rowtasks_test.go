package scaffold

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func fileOf(t *testing.T, files []File, p string) string {
	t.Helper()
	for _, f := range files {
		if f.Path == p {
			return string(f.Data)
		}
	}
	t.Fatalf("no file %s", p)
	return ""
}

func TestParseRowsCSV(t *testing.T) {
	data := "\xef\xbb\xbfQuestion,Answer,Wrong_Answers,Category,Split\n" +
		"\"What is the refund window, in days?\",30,14 | 60,refunds,\n" +
		",,,,\n" +
		"Which plan has SSO?,Enterprise,,plans,held_out\n" +
		"No check here,,,,\n"
	rows, problems, err := ParseRows(RowFormatCSV, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || len(problems) != 1 {
		t.Fatalf("rows %+v problems %+v", rows, problems)
	}
	if rows[0].Question != "What is the refund window, in days?" || rows[0].Expected[0] != "30" || len(rows[0].Wrong) != 2 || rows[0].Line != 2 {
		t.Fatalf("row 0 = %+v", rows[0])
	}
	if !rows[1].HeldOut || rows[1].Category != "plans" {
		t.Fatalf("row 1 = %+v", rows[1])
	}
	if problems[0].Line != 5 || !strings.Contains(problems[0].Message, "no check") {
		t.Fatalf("problem = %+v", problems[0])
	}
}

func TestParseRowsJSONL(t *testing.T) {
	data := `{"id":"Q-1","instruction":"Summarise the policy","rubric":"Mentions the 30 day window","reference":"Refunds within 30 days."}
{"prompt":"2+2?","expected":["4","four"],"match":"exact"}
{"prompt":"bad","answer":"x","match":"fuzzy"}
`
	rows, problems, err := ParseRows(RowFormatJSONL, []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || len(problems) != 1 || problems[0].Line != 3 {
		t.Fatalf("rows %+v problems %+v", rows, problems)
	}
	if rows[0].Rubric == "" || rows[0].Reference == "" || rows[0].ID != "Q-1" {
		t.Fatalf("row 0 = %+v", rows[0])
	}
	if len(rows[1].Expected) != 2 {
		t.Fatalf("row 1 = %+v", rows[1])
	}
	if _, _, err := ParseRows(RowFormatJSONL, []byte("{nope\n")); err == nil {
		t.Fatal("bad JSONL accepted")
	}
	if _, _, err := ParseRows("xlsx", []byte("a")); err == nil {
		t.Fatal("unknown format accepted")
	}
}

func TestParseTickets(t *testing.T) {
	js := `{"tickets":[{"key":"SUP-12","subject":"Charged twice","description":"I was billed two times in May.","resolution":"Apologise, confirm the duplicate charge and refund it within 5 days."}]}`
	rows, problems, err := ParseRows(RowFormatTickets, []byte(js))
	if err != nil || len(problems) != 0 || len(rows) != 1 {
		t.Fatalf("rows %+v problems %+v err %v", rows, problems, err)
	}
	r := rows[0]
	if !r.Ticket || r.ID != "SUP-12" || r.Rubric == "" || !strings.Contains(r.Question, "billed two times") {
		t.Fatalf("ticket = %+v", r)
	}
	text := "Cannot log in\nThe reset link expired.\nExpected: Send a new reset link and explain it lasts 1 hour.\n---\nWhere is my invoice?\nI need May's invoice.\nResolution: Point to Billing > Invoices.\n"
	rows, problems, err = ParseRows(RowFormatTickets, []byte(text))
	if err != nil || len(problems) != 0 || len(rows) != 2 {
		t.Fatalf("rows %+v problems %+v err %v", rows, problems, err)
	}
	if rows[0].Title != "Cannot log in" || rows[0].Question != "The reset link expired." || !strings.HasPrefix(rows[0].Rubric, "Send a new") {
		t.Fatalf("text ticket 0 = %+v", rows[0])
	}
	if rows[1].Line != 5 || rows[1].Rubric != "Point to Billing > Invoices." {
		t.Fatalf("text ticket 1 = %+v", rows[1])
	}
}

func TestImportRowsExact(t *testing.T) {
	rows := []Row{
		{Line: 2, Question: "How many days is the refund window?", Expected: []string{"30"}, Match: "numeric"},
		{Line: 3, Question: "How many days is the refund window?", Expected: []string{"thirty"}, HeldOut: true},
		{Line: 4, Question: "Rubric only", Rubric: "says hi"},
		{Line: 5, ID: "taken", Question: "q", Expected: []string{"a"}},
	}
	tasks, problems := ImportRows(rows, RowTaskOptions{Taken: func(id string) bool { return id == "taken" }})
	if len(tasks) != 2 || len(problems) != 2 {
		t.Fatalf("tasks %+v problems %+v", tasks, problems)
	}
	if tasks[0].ID != "how-many-days-is-the-refund-window" || tasks[1].ID != "how-many-days-is-the-refund-window-2" {
		t.Fatalf("ids %s %s", tasks[0].ID, tasks[1].ID)
	}
	var spec struct {
		Match    string   `yaml:"match"`
		Expected []string `yaml:"expected"`
		Wrong    []string `yaml:"wrong_answers"`
	}
	if err := yaml.Unmarshal([]byte(fileOf(t, tasks[0].Files, "verifier/answer.yaml")), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Match != "numeric" || spec.Expected[0] != "30" || spec.Wrong[0] != "31" {
		t.Fatalf("spec = %+v", spec)
	}
	var meta map[string]any
	if err := yaml.Unmarshal([]byte(fileOf(t, tasks[1].Files, "task.yaml")), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["split"] != "held_out" || meta["id"] != tasks[1].ID {
		t.Fatalf("task.yaml = %v", meta)
	}
	if fileOf(t, tasks[0].Files, "solution/answer.txt") != "30\n" {
		t.Fatal("reference answer")
	}
	if !strings.Contains(fileOf(t, tasks[0].Files, "instruction.md"), "answer.txt") {
		t.Fatal("instruction says where to answer")
	}
}

func TestImportRowsJudgeAndTickets(t *testing.T) {
	rows := []Row{
		{Line: 1, Title: "Charged twice", Question: "I was billed twice", Rubric: "Apologise and refund", Expected: []string{"Apologise and refund"}, Ticket: true},
		{Line: 2, Question: "Rubric only", Rubric: "says hi"},
	}
	if tasks, problems := ImportRows(rows[:1], RowTaskOptions{}); len(tasks) != 0 || len(problems) != 1 {
		t.Fatalf("a ticket under the exact grader: tasks %+v problems %+v", tasks, problems)
	}
	tasks, problems := ImportRows(rows, RowTaskOptions{Grader: RowGraderJudge})
	if len(tasks) != 1 || len(problems) != 1 || problems[0].Line != 2 {
		t.Fatalf("tasks %+v problems %+v", tasks, problems)
	}
	if !strings.Contains(fileOf(t, tasks[0].Files, "verifier/answer.yaml"), "rubric: Apologise and refund") {
		t.Fatal("rubric missing")
	}
	if !strings.Contains(fileOf(t, tasks[0].Files, "instruction.md"), "> I was billed twice") {
		t.Fatal("ticket instruction")
	}
}

func TestSetTaskSplit(t *testing.T) {
	src := []byte("# comment kept\nid: a\nversion: 1.0.0\n")
	out, err := SetTaskSplit(src, true)
	if err != nil || !strings.Contains(string(out), "split: held_out") || !strings.Contains(string(out), "# comment kept") {
		t.Fatalf("%s %v", out, err)
	}
	back, err := SetTaskSplit(out, false)
	if err != nil || strings.Contains(string(back), "split") {
		t.Fatalf("%s %v", back, err)
	}
}

func TestSetSuiteGrader(t *testing.T) {
	src := []byte("# template: qa\nid: s\ngrader: answer_match\ntasks: []\n")
	if _, err := SetSuiteGrader(src, RowGraderJudge, nil); err == nil {
		t.Fatal("judge without model accepted")
	}
	if _, err := SetSuiteGrader(src, RowGraderJudge, &JudgeConfig{Provider: "openai", Model: "m", Votes: 2}); err == nil {
		t.Fatal("even votes accepted")
	}
	if _, err := SetSuiteGrader(src, "hidden_tests", nil); err == nil {
		t.Fatal("hidden_tests accepted")
	}
	out, err := SetSuiteGrader(src, RowGraderJudge, &JudgeConfig{Provider: "openai", Model: "gpt-x", Votes: 3})
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Grader  string `yaml:"grader"`
		Grading string `yaml:"grading"`
		Judge   struct {
			Provider, Model string
			Prompt          string `yaml:"prompt_version"`
			Votes           int
		} `yaml:"judge"`
	}
	if err := yaml.Unmarshal(out, &s); err != nil {
		t.Fatal(err)
	}
	if s.Grader != "judge" || s.Grading != "judged" || s.Judge.Model != "gpt-x" || s.Judge.Prompt != DefaultJudgePrompt || s.Judge.Votes != 3 {
		t.Fatalf("%+v", s)
	}
	if !strings.HasPrefix(string(out), "# template: qa") {
		t.Fatalf("template comment lost:\n%s", out)
	}
	back, err := SetSuiteGrader(out, RowGraderExact, nil)
	if err != nil || strings.Contains(string(back), "judge:") || strings.Contains(string(back), "grading") {
		t.Fatalf("%s %v", back, err)
	}
}

func TestBlankSuite(t *testing.T) {
	files, err := Suite(SuiteOptions{ID: "faq", Template: TemplateQA})
	if err != nil {
		t.Fatal(err)
	}
	blank, err := BlankSuite(files)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range blank {
		if strings.HasPrefix(f.Path, "tasks/") {
			t.Fatalf("sample task kept: %s", f.Path)
		}
	}
	var s struct {
		Tasks []string `yaml:"tasks"`
	}
	y := fileOf(t, blank, "suite.yaml")
	if err := yaml.Unmarshal([]byte(y), &s); err != nil || len(s.Tasks) != 0 {
		t.Fatalf("%s %v", y, err)
	}
	if DetectTemplate([]byte(y)) != TemplateQA {
		t.Fatalf("template not detected:\n%s", y)
	}
	out, changed, err := AddTaskID([]byte(y), "q1")
	if err != nil || !changed || !strings.Contains(string(out), "q1") {
		t.Fatalf("add to blank: %s %v", out, err)
	}
}
