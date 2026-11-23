package onboard

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

// rowsSource reads a spreadsheet (CSV), JSON Lines, a JSON array or
// tickets: each row becomes a question-and-answer task.
type rowsSource struct{}

func init() { Register(rowsSource{}) }

func (rowsSource) Name() string { return "rows" }
func (rowsSource) Kind() Kind   { return KindRows }

func (rowsSource) Detect(p Peek) (Match, bool) {
	if p.Dir != "" || len(p.Data) == 0 {
		return Match{}, false
	}
	if looksOpenAPI(p.Data) || looksOTLP(p.Data) {
		return Match{}, false
	}
	switch p.Ext {
	case ".csv":
		return Match{Label: "Spreadsheet (CSV)", Score: 90, CanDraft: true}, true
	case ".jsonl", ".ndjson":
		return Match{Label: "JSON Lines rows", Score: 90, CanDraft: true}, true
	case ".json":
		return Match{Label: "JSON rows", Score: 70, CanDraft: true}, true
	case ".txt", ".md":
		if ticketSplitRE(p.Data) {
			return Match{Label: "Tickets (text split by --- lines)", Score: 70, CanDraft: true}, true
		}
	}
	t := bytes.TrimSpace(p.Data)
	if len(t) > 0 && (t[0] == '{' || t[0] == '[') {
		return Match{Label: "JSON rows", Score: 50, CanDraft: true}, true
	}
	if first, _, _ := bytes.Cut(t, []byte("\n")); bytes.Count(first, []byte(",")) >= 1 {
		return Match{Label: "Spreadsheet (CSV)", Score: 40, CanDraft: true}, true
	}
	return Match{}, false
}

func ticketSplitRE(b []byte) bool {
	for _, ln := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(ln); len(t) >= 3 && strings.Trim(t, "-") == "" {
			return true
		}
	}
	return false
}

// rowFormat is the scaffold row format of a file.
func rowFormat(plan Plan, name string, data []byte) string {
	if plan.Format != "" {
		return plan.Format
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".csv":
		return scaffold.RowFormatCSV
	case ".txt", ".md":
		return scaffold.RowFormatTickets
	}
	t := bytes.TrimSpace(data)
	if len(t) > 0 && (t[0] == '{' || t[0] == '[') {
		return scaffold.RowFormatJSONL
	}
	return scaffold.RowFormatCSV
}

func (rowsSource) data(ctx context.Context, env *Env, in Input) (string, []byte, error) {
	p, err := env.Peek(ctx, in)
	if err != nil {
		return "", nil, err
	}
	if p.FetchErr != "" {
		return "", nil, errors.New(p.FetchErr)
	}
	if p.Dir != "" {
		return "", nil, errors.New("this upload is a folder; rows come as one CSV or JSONL file")
	}
	if len(p.Data) == 0 {
		return "", nil, fmt.Errorf("%s is empty or larger than %d MiB", p.Name, MaxDataBytes>>20)
	}
	return p.Name, p.Data, nil
}

// rowsPlan is the parsed rows of a file and what they suggest.
type rowsPlan struct {
	format   string
	unit     string // what a row reference counts: row, line or item (scaffold.RecordUnit)
	columns  []string
	records  int
	rows     []scaffold.Row
	problems []scaffold.RowProblem
}

func parseRowsPlan(name string, data []byte, plan Plan) (*rowsPlan, error) {
	format := rowFormat(plan, name, data)
	recs, err := records(format, data)
	if err != nil {
		return nil, err
	}
	rp := &rowsPlan{format: format, unit: scaffold.RecordUnit(format, data), columns: columnsOf(recs), records: len(recs)}
	if len(recs) > scaffold.MaxImportRows {
		recs = recs[:scaffold.MaxImportRows]
	}
	input, inFormat := data, format
	// lines maps a mapped record (numbered 1, 2, ... in the JSON written
	// below) back to where it starts in the file, so a mapping does not
	// renumber the rows.
	var lines []int
	if len(plan.Mapping) > 0 || len(recs) < rp.records {
		if _, ls, err := scaffold.ReadRecords(format, data); err == nil && len(ls) == rp.records {
			lines = ls
		}
		mapped, err := applyMapping(recs, plan.Mapping)
		if err != nil {
			return nil, err
		}
		if input, err = json.Marshal(mapped); err != nil {
			return nil, err
		}
		if inFormat != scaffold.RowFormatTickets {
			inFormat = scaffold.RowFormatJSONL
		}
	}
	rows, problems, err := scaffold.ParseRows(inFormat, input)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 && format != scaffold.RowFormatTickets && plan.Format == "" && hasTicketColumns(rp.columns) {
		// Subject and body columns but no question: read them as tickets.
		rp.format = scaffold.RowFormatTickets
		if r2, p2, err := scaffold.ParseRows(scaffold.RowFormatTickets, input); err == nil && len(r2) > 0 {
			rows, problems = r2, p2
		}
	}
	if lines != nil {
		at := func(n int) int {
			if n >= 1 && n <= len(lines) {
				return lines[n-1]
			}
			return n
		}
		for i := range rows {
			rows[i].Line = at(rows[i].Line)
		}
		for i := range problems {
			problems[i].Line = at(problems[i].Line)
		}
	}
	rp.rows, rp.problems = rows, problems
	return rp, nil
}

// ref is the reference of the record at n: "row 2" in a CSV (the row a
// spreadsheet shows), "line 3" in JSON Lines or pasted text, "item 1" in a
// JSON array.
func (rp *rowsPlan) ref(n int) string {
	unit := rp.unit
	if unit == "" {
		unit = "row"
	}
	return fmt.Sprintf("%s %d", unit, n)
}

func hasTicketColumns(cols []string) bool {
	for _, c := range cols {
		switch c {
		case "body", "description", "message", "subject":
			return true
		}
	}
	return false
}

// records reads the raw records of a file, keys lower-cased.
func records(format string, data []byte) ([]map[string]any, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	t := bytes.TrimSpace(data)
	if len(t) == 0 {
		return nil, errors.New("the data is empty")
	}
	if format == scaffold.RowFormatCSV {
		rd := csv.NewReader(bytes.NewReader(data))
		rd.FieldsPerRecord, rd.LazyQuotes = -1, true
		header, err := rd.Read()
		if err != nil {
			return nil, fmt.Errorf("read the header row: %w", err)
		}
		var out []map[string]any
		for {
			rec, err := rd.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("csv: %w", err)
			}
			m := map[string]any{}
			for i, v := range rec {
				if i < len(header) {
					if k := strings.ToLower(strings.TrimSpace(header[i])); k != "" {
						m[k] = v
					}
				}
			}
			out = append(out, m)
		}
		return out, nil
	}
	if t[0] != '{' && t[0] != '[' {
		return nil, nil // plain text tickets: no columns
	}
	var items []any
	var v any
	if err := json.Unmarshal(t, &v); err == nil {
		switch x := v.(type) {
		case []any:
			items = x
		case map[string]any:
			for _, k := range []string{"rows", "tickets", "items", "data", "examples"} {
				if a, ok := x[k].([]any); ok {
					items = a
					break
				}
			}
			if items == nil {
				items = []any{x}
			}
		}
	} else {
		for i, ln := range strings.Split(string(t), "\n") {
			ln = strings.TrimSpace(ln)
			if ln == "" {
				continue
			}
			var m any
			if err := json.Unmarshal([]byte(ln), &m); err != nil {
				return nil, fmt.Errorf("line %d: not a JSON object: %v", i+1, err)
			}
			items = append(items, m)
		}
	}
	out := make([]map[string]any, 0, len(items))
	for i, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("item %d is not an object", i+1)
		}
		low := make(map[string]any, len(m))
		for k, v := range m {
			low[strings.ToLower(strings.TrimSpace(k))] = v
		}
		out = append(out, low)
	}
	return out, nil
}

// columnsOf lists the column paths of the first records: top-level keys and
// one level of nested object keys as dotted paths.
func columnsOf(recs []map[string]any) []string {
	seen := map[string]bool{}
	for i, r := range recs {
		if i >= 50 {
			break
		}
		for k, v := range r {
			seen[k] = true
			if m, ok := v.(map[string]any); ok {
				for k2 := range m {
					seen[k+"."+strings.ToLower(k2)] = true
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// mappingKeys are the row fields a mapping may name, and the column the
// row reader takes first for each.
var mappingKeys = map[string]string{
	"id": "id", "title": "title", "question": "question", "expected": "answer", "answer": "answer",
	"rubric": "rubric", "reference": "reference", "wrong_answers": "wrong_answers", "match": "match",
	"category": "category", "split": "split",
}

// applyMapping copies each mapped column (a dotted path) to the field's
// first column name, so the row reader takes it ahead of any alias.
func applyMapping(recs []map[string]any, mapping map[string]string) ([]map[string]any, error) {
	for field, col := range mapping {
		if _, ok := mappingKeys[field]; !ok {
			return nil, fmt.Errorf("mapping: unknown field %q (want question, expected, rubric, reference, wrong_answers, id, title, category, match or split)", field)
		}
		if len(col) > 200 || strings.ContainsAny(col, "\n\r") {
			return nil, fmt.Errorf("mapping.%s: a column path of at most 200 characters", field)
		}
	}
	out := make([]map[string]any, len(recs))
	for i, r := range recs {
		m := make(map[string]any, len(r)+len(mapping))
		for k, v := range r {
			m[k] = v
		}
		for field, col := range mapping {
			key := mappingKeys[field]
			col = strings.ToLower(strings.TrimSpace(col))
			if col == "" {
				continue
			}
			if col == "-" {
				delete(m, key)
				continue
			}
			if v, ok := pathValue(r, col); ok {
				m[key] = v
			} else {
				delete(m, key)
			}
		}
		out[i] = m
	}
	return out, nil
}

// pathValue reads a dotted path ("inputs.question") from a record.
func pathValue(r map[string]any, p string) (any, bool) {
	if v, ok := r[p]; ok {
		return v, true
	}
	var cur any = r
	for _, seg := range strings.Split(p, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		var found bool
		for k, v := range m {
			if strings.EqualFold(k, seg) {
				cur, found = v, true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return cur, true
}

func (s rowsSource) Inspect(ctx context.Context, env *Env, in Input) (*Inventory, error) {
	name, data, err := s.data(ctx, env, in)
	if err != nil {
		return nil, err
	}
	return inspectRows(name, data, Plan{}, false)
}

// inspectRows is the inventory of row data; public is true for a public
// dataset (no held-out share by default).
func inspectRows(name string, data []byte, plan Plan, public bool) (*Inventory, error) {
	rp, err := parseRowsPlan(name, data, plan)
	if err != nil {
		return nil, err
	}
	inv := &Inventory{Kind: KindRows, Title: strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)), License: "your data", Unit: "row",
		Units: rp.records, Columns: rp.columns, CanDraft: len(rp.rows) > 0}
	if rp.records > scaffold.MaxImportRows {
		inv.Warnings = append(inv.Warnings, fmt.Sprintf("%d rows: one draft takes the first %d", rp.records, scaffold.MaxImportRows))
	}
	judged, ticket := false, rp.format == scaffold.RowFormatTickets
	for i, r := range rp.rows {
		if r.Rubric != "" || r.Ticket {
			judged = true
		}
		if i < 5 {
			f := map[string]string{"question": r.Question}
			if len(r.Expected) > 0 {
				f["expected"] = strings.Join(r.Expected, " | ")
			}
			if r.Rubric != "" {
				f["rubric"] = r.Rubric
			}
			if len(r.Wrong) > 0 {
				f["wrong_answers"] = strings.Join(r.Wrong, " | ")
			}
			inv.Samples = append(inv.Samples, Sample{Ref: rp.ref(r.Line), Fields: f})
		}
	}
	for _, p := range rp.problems {
		if len(inv.Warnings) >= 8 {
			inv.Warnings = append(inv.Warnings, fmt.Sprintf("%d more rows have problems; the draft lists them all", len(rp.problems)-7))
			break
		}
		inv.Warnings = append(inv.Warnings, rp.ref(p.Line)+": "+p.Message)
	}
	plan.Format = rp.format
	plan.Suite = slug(inv.Title)
	plan.Grader = scaffold.RowGraderExact
	if judged {
		plan.Grader = scaffold.RowGraderJudge
		inv.Needs = append(inv.Needs, "a judge model to pin (rubric-graded rows): pick one on the plan")
	}
	if !public {
		plan.Hold = SplitPlan{Pct: 25, Seed: 1}
	} else {
		inv.Warnings = append(inv.Warnings, "public data: models may have seen it in training; nothing is held out by default")
	}
	inv.Suggest = plan
	tasks := len(rp.rows)
	if tasks > scaffold.MaxImportRows {
		tasks = scaffold.MaxImportRows
	}
	wrong := 0
	for _, r := range rp.rows {
		if len(r.Wrong) > 0 {
			wrong++
		}
	}
	inv.Estimate = Estimate{Tasks: tasks, ReadinessRuns: tasks * 6}
	if judged {
		inv.Estimate.JudgeCalls = tasks * 6
	}
	if ticket {
		inv.Unit = "ticket"
	}
	if wrong < tasks {
		inv.Warnings = append(inv.Warnings, fmt.Sprintf("%d of %d rows have no wrong answer. Heimdall will make one for the readiness check by changing the right answer slightly. A real wrong answer from your data (a wrong_answers column) tests the grader better.", tasks-wrong, tasks))
	}
	inv.Next = "Check the plan, then Draft: nothing is added to a benchmark until you publish."
	if !inv.CanDraft {
		inv.Next = "No row can become a task yet: map the question and answer columns, then inspect again."
	}
	return inv, nil
}

func (s rowsSource) Draft(ctx context.Context, env *Env, in Input, plan Plan, set *DraftSet) error {
	name, data, err := s.data(ctx, env, in)
	if err != nil {
		return err
	}
	set.License = "your data"
	return draftRows(env, set, plan, name, data, in.Label())
}

// draftRows writes row tasks into the set's suite folder: a copy of the
// target question-and-answer benchmark (or a new one) with the rows as
// tasks.
func draftRows(env *Env, set *DraftSet, plan Plan, name string, data []byte, label string) error {
	rp, err := parseRowsPlan(name, data, plan)
	if err != nil {
		return err
	}
	grader, existing, err := stageAnswerSuite(env, set, plan)
	if err != nil {
		return err
	}
	rows := rp.rows
	if plan.Limit > 0 && len(rows) > plan.Limit {
		rows = rows[:plan.Limit]
	}
	tasks, more := scaffold.ImportRows(rows, scaffold.RowTaskOptions{Grader: grader, Match: plan.Match, Source: label,
		Taken: func(id string) bool { return existing[id] }})
	for _, p := range append(rp.problems, more...) {
		set.Problems = append(set.Problems, Problem{Ref: rp.ref(p.Line), Message: p.Message})
	}
	wrongFrom := map[int]bool{}
	for _, r := range rows {
		wrongFrom[r.Line] = len(r.Wrong) > 0
	}
	suitePath := filepath.Join(set.SuiteDir(), suite.SuiteFile)
	raw, err := os.ReadFile(suitePath)
	if err != nil {
		return err
	}
	for _, t := range tasks {
		if err := scaffold.WriteFiles(filepath.Join(set.SuiteDir(), "tasks", t.ID), t.Files, false); err != nil {
			return fmt.Errorf("write task %s: %w", t.ID, err)
		}
		if raw, _, err = scaffold.AddTaskID(raw, t.ID); err != nil {
			return fmt.Errorf("add task %s: %w", t.ID, err)
		}
		mutant := "rule"
		if wrongFrom[t.Line] {
			mutant = "source"
		}
		set.Tasks = append(set.Tasks, DraftTask{ID: t.ID, Origin: Origin{Source: label, Ref: rp.ref(t.Line)},
			Controls: []ControlInfo{
				{Kind: "reference", Path: "solution/answer.txt", Origin: "source"},
				{Kind: "empty", Path: "controls/empty.txt", Origin: "rule"},
				{Kind: "wrong_answer", Path: "controls/mutant-1.txt", Origin: mutant},
				{Kind: "exploit", Path: "controls/exploit.sh", Origin: "rule"},
			}})
	}
	return os.WriteFile(suitePath, raw, 0o644)
}

// stageAnswerSuite writes the draft's suite.yaml (and the benchmark's other
// files) with an empty task list: a copy of the target when it exists, else
// a new question-and-answer benchmark. It returns the grader and the task
// ids already taken in the target.
func stageAnswerSuite(env *Env, set *DraftSet, plan Plan) (string, map[string]bool, error) {
	target := filepath.Join(env.Benchmarks(), set.Suite)
	existing := map[string]bool{}
	var files []scaffold.File
	if !set.NewSuite {
		raw, err := os.ReadFile(filepath.Join(target, suite.SuiteFile))
		if err != nil {
			return "", nil, err
		}
		var head struct {
			Grader string   `yaml:"grader"`
			Tasks  []string `yaml:"tasks"`
			Output struct {
				Kind string `yaml:"kind"`
			} `yaml:"output"`
		}
		if err := yaml.Unmarshal(raw, &head); err != nil {
			return "", nil, fmt.Errorf("benchmark %s suite.yaml: %w", set.Suite, err)
		}
		if head.Output.Kind != "answer" {
			return "", nil, fmt.Errorf("benchmark %s grades code or workspace state with its own tests; rows go into a question-and-answer benchmark: pick a new benchmark id", set.Suite)
		}
		for _, t := range head.Tasks {
			existing[t] = true
		}
		if ents, err := os.ReadDir(filepath.Join(target, "tasks")); err == nil {
			for _, e := range ents {
				existing[e.Name()] = true
			}
		}
		if files, err = suiteFiles(target); err != nil {
			return "", nil, err
		}
		if plan.Grader != "" && plan.Grader != head.Grader {
			set.Notes = append(set.Notes, "benchmark "+set.Suite+" uses grader "+head.Grader+"; the draft uses it too (change it on the benchmark's Build tab)")
		}
	} else {
		var err error
		files, err = scaffold.Suite(scaffold.SuiteOptions{ID: set.Suite, Template: scaffold.TemplateQA})
		if err != nil {
			return "", nil, err
		}
	}
	files, err := scaffold.BlankSuite(files)
	if err != nil {
		return "", nil, err
	}
	if set.NewSuite && plan.Grader == scaffold.RowGraderJudge {
		for i, f := range files {
			if f.Path == suite.SuiteFile {
				edited, err := scaffold.SetSuiteGrader(f.Data, scaffold.RowGraderJudge, plan.Judge)
				if err != nil {
					return "", nil, fmt.Errorf("grader: %w (pick the judge provider and model on the plan)", err)
				}
				files[i].Data = edited
			}
		}
	}
	if err := scaffold.WriteFiles(set.SuiteDir(), files, true); err != nil {
		return "", nil, err
	}
	head, _, err := readSuiteHead(set.SuiteDir())
	if err != nil {
		return "", nil, err
	}
	grader := scaffold.RowGraderExact
	if head.Grader == scaffold.RowGraderJudge {
		grader = scaffold.RowGraderJudge
	} else if head.Grader != scaffold.RowGraderExact {
		return "", nil, fmt.Errorf("benchmark %s uses grader %s; rows need the exact answer or the rubric judge grader", set.Suite, head.Grader)
	}
	return grader, existing, nil
}

// suiteFiles reads a benchmark's own files, without its tasks, lock, runs
// and calibration records.
func suiteFiles(dir string) ([]scaffold.File, error) {
	var out []scaffold.File
	for _, rel := range listFiles(dir, 2000) {
		top, _, _ := strings.Cut(rel, "/")
		if top == "tasks" || top == "calibration" || top == ".heimdall" || rel == "suite.lock" || rel == "onboard.yaml" {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(rel))
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out = append(out, scaffold.File{Path: rel, Data: data, Mode: st.Mode().Perm()})
	}
	return out, nil
}
