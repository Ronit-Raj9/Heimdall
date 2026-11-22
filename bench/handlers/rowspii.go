package handlers

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/datapolicy"
	"github.com/maximhq/bifrost/bench/dataset"
	"github.com/maximhq/bifrost/bench/pii"
	"github.com/maximhq/bifrost/bench/scaffold"
)

// cleanRows applies benchmark id's personal data policy to rows of the web
// rows import (lane Y15): mask and redact rewrite the text, block refuses
// a row holding personal data, warn only reports.
func cleanRows(root, id string, rows []scaffold.Row) ([]scaffold.Row, []scaffold.RowProblem, *dataset.PIIReport, error) {
	if root == "" {
		rep := dataset.NewPIIReport(pii.ModeOff)
		rep.Finish()
		return rows, nil, rep, nil
	}
	red, _, err := datapolicy.Open(root).Redactor(id)
	if err != nil {
		return nil, nil, nil, err
	}
	rep := dataset.NewPIIReport(red.Policy.Effective())
	var out []scaffold.Row
	var problems []scaffold.RowProblem
	for _, r := range rows {
		found := pii.Summary{}
		var fields []string
		apply := func(name string, s *string) {
			res := red.Apply(*s)
			if len(res.Matches) > 0 {
				found.Add(res.Matches)
				fields = append(fields, name)
			}
			*s = res.Text
		}
		apply("title", &r.Title)
		apply("question", &r.Question)
		apply("rubric", &r.Rubric)
		apply("reference", &r.Reference)
		for i := range r.Expected {
			apply(fmt.Sprintf("expected[%d]", i), &r.Expected[i])
		}
		for i := range r.Wrong {
			apply(fmt.Sprintf("wrong_answers[%d]", i), &r.Wrong[i])
		}
		if found.Total() > 0 {
			f := dataset.Finding{Index: r.Line, ID: r.ID, Fields: fields, Kinds: map[string]int{}}
			for k, n := range found {
				f.Kinds[string(k)] = n
				rep.Kinds[string(k)] += n
			}
			rep.Rows++
			rep.Findings = append(rep.Findings, f)
			if red.Policy.Effective() == pii.ModeBlock {
				problems = append(problems, scaffold.RowProblem{Line: r.Line,
					Message: "refused by the benchmark's personal data policy (block): " + found.String() + " in " + strings.Join(fields, ", ")})
				continue
			}
		}
		out = append(out, r)
	}
	rep.Finish()
	return out, problems, rep, nil
}
