package graders

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// RubricLine is one point-weighted line of a rubric judged by the
// bigfinancebench-v1 prompt.
type RubricLine struct {
	Points int
	Text   string
}

// rubricLineRe reads one rubric line as the importer writes it:
// "[<points>] <text>".
var rubricLineRe = regexp.MustCompile(`^\[(\d+)\]\s+(\S.*)$`)

// ParseRubricLines reads a point-weighted rubric: one "[<points>] <text>"
// line per analyst step. Blank lines are skipped; any other line is an
// error.
func ParseRubricLines(rubric string) ([]RubricLine, error) {
	var out []RubricLine
	for i, l := range strings.Split(rubric, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		m := rubricLineRe.FindStringSubmatch(l)
		if m == nil {
			return nil, fmt.Errorf("rubric line %d is not \"[<points>] <text>\": %q", i+1, l)
		}
		p, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("rubric line %d points: %w", i+1, err)
		}
		out = append(out, RubricLine{Points: p, Text: m[2]})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("rubric has no lines")
	}
	return out, nil
}

// FormatRubricLines writes lines in the form ParseRubricLines reads.
func FormatRubricLines(lines []RubricLine) string {
	var b strings.Builder
	for _, l := range lines {
		fmt.Fprintf(&b, "[%d] %s\n", l.Points, strings.Join(strings.Fields(l.Text), " "))
	}
	return b.String()
}

// rubricForJudge numbers the rubric lines without their points, as the
// upstream grader does, so the judge is not biased toward heavy lines. A
// rubric that does not parse is passed through unchanged.
func rubricForJudge(rubric string) string {
	lines, err := ParseRubricLines(rubric)
	if err != nil {
		return rubric
	}
	var b strings.Builder
	for i, l := range lines {
		fmt.Fprintf(&b, "%d. %s\n", i+1, l.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}

// rubricReply is the JSON object the bigfinancebench-v1 judge returns.
type rubricReply struct {
	FinalAnswerCorrect *bool `json:"final_answer_correct"`
	Rubric             []struct {
		Index     int  `json:"index"`
		Satisfied bool `json:"satisfied"`
	} `json:"rubric"`
}

// parseRubricReply reads the judge's JSON object; a fenced block or text
// around the outermost braces is tolerated.
func parseRubricReply(reply string) (*rubricReply, bool) {
	s := strings.TrimSpace(reply)
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return nil, false
	}
	var r rubricReply
	if json.Unmarshal([]byte(s[i:j+1]), &r) != nil || r.FinalAnswerCorrect == nil {
		return nil, false
	}
	return &r, true
}

// replyVerdict reads one vote's verdict for the prompt version.
func replyVerdict(version, reply string) string {
	if version != PromptBigFinanceV1 {
		return ParseVerdict(reply)
	}
	r, ok := parseRubricReply(reply)
	switch {
	case !ok:
		return VoteUnparsable
	case *r.FinalAnswerCorrect:
		return VoteCorrect
	}
	return VoteIncorrect
}

// applyRubricScore sets the rubric_score dimension: the share of rubric
// points the parsable votes found satisfied, averaged over those votes.
// final_answer_correct (Passed) is never changed by it.
func applyRubricScore(grade *schemas.Grade, ans *schemas.AnswerSpec, replies []string) {
	lines, err := ParseRubricLines(ans.Rubric)
	if err != nil {
		return
	}
	total := 0
	for _, l := range lines {
		total += l.Points
	}
	if total <= 0 {
		return
	}
	sum, n := 0.0, 0
	for _, reply := range replies {
		r, ok := parseRubricReply(reply)
		if !ok {
			continue
		}
		got, seen := 0, map[int]bool{}
		for _, v := range r.Rubric {
			if v.Satisfied && v.Index >= 1 && v.Index <= len(lines) && !seen[v.Index] {
				seen[v.Index] = true
				got += lines[v.Index-1].Points
			}
		}
		sum += float64(got) / float64(total)
		n++
	}
	if n == 0 {
		return
	}
	if grade.Dimensions == nil {
		grade.Dimensions = map[string]float64{}
	}
	grade.Dimensions["rubric_score"] = sum / float64(n)
	if grade.Passed {
		grade.Dimensions["final_answer_correct"] = 1
	} else {
		grade.Dimensions["final_answer_correct"] = 0
	}
}
