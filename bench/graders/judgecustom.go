package graders

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Custom judge prompts (G2 P1-3): an org writes its own judge prompt as a
// file in the suite folder and pins it by sha256 in suite.yaml:
//
//	grading: judged
//	judge:
//	  provider: openai
//	  model: gpt-4.1-mini
//	  prompt_version: custom-v1
//	  prompt_file: judge/support-v1.md
//	  prompt_sha256: 9f2c...
//	  pass_at: 0.8
//
// The task's rubric lists the criteria, one per line, "[<weight>] <text>"
// or plain text (weight 1). The prompt may use {{question}}, {{reference}}
// (the expected answers), {{rubric}}, {{criteria}}, {{response}} (required)
// and {{transcript}} (the run's trajectory); the response and the
// transcript are fenced like every judged answer, and the judge guard still
// fails an answer that addresses the grader or a bare refusal before any
// call. The reply must be one JSON object, {"criteria":[{"id","met",
// "reason"}]}, with an entry per criterion; the format is appended to the
// prompt unless it places {{format}} itself. A vote is correct when the
// weighted share of criteria met reaches pass_at.

// PromptCustomV1 is the custom judge prompt version.
const PromptCustomV1 = "custom-v1"

const (
	maxCustomPromptBytes = 64 << 10
	customJudgeTokens    = 2048 // default max_tokens: room for a reason per criterion, after a reasoning model's reasoning (fix wave 3, N273)
)

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// judgeCriterion is one criterion of a task's rubric.
type judgeCriterion struct {
	ID     string
	Text   string
	Weight float64
}

// criterionLineRe is "[<weight>] <text>"; the weight may be a decimal.
var criterionLineRe = regexp.MustCompile(`^\[(\d+(?:\.\d+)?)\]\s+(\S.*)$`)

// parseCriteria reads the criteria of a rubric: one per non-blank line,
// "[<weight>] <text>" or plain text with weight 1, a leading "- " or "* "
// dropped. Ids are c1, c2, ... in order.
func parseCriteria(rubric string) ([]judgeCriterion, error) {
	var out []judgeCriterion
	for i, l := range strings.Split(rubric, "\n") {
		l = strings.TrimSpace(l)
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(l, "- "), "* "))
		if l == "" {
			continue
		}
		c := judgeCriterion{ID: "c" + strconv.Itoa(len(out)+1), Text: l, Weight: 1}
		if m := criterionLineRe.FindStringSubmatch(l); m != nil {
			w, err := strconv.ParseFloat(m[1], 64)
			if err != nil || w <= 0 {
				return nil, fmt.Errorf("rubric line %d: weight %q must be a number above 0", i+1, m[1])
			}
			c.Weight, c.Text = w, m[2]
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errors.New("prompt custom-v1 needs the task's criteria as the answer spec rubric, one per line")
	}
	return out, nil
}

// checkCustomSpec validates the custom-v1 fields of a judge spec.
func checkCustomSpec(spec schemas.JudgeSpec) error {
	if spec.PromptVersion != PromptCustomV1 {
		if spec.PromptFile != "" || spec.PromptSHA256 != "" || spec.PassAt != nil {
			return fmt.Errorf("judge prompt_file, prompt_sha256 and pass_at are for prompt_version %s only", PromptCustomV1)
		}
		return nil
	}
	switch {
	case spec.PromptFile == "":
		return errors.New("judge prompt_version custom-v1 needs prompt_file, the prompt in the suite folder")
	case !sha256Re.MatchString(spec.PromptSHA256):
		return errors.New("judge prompt_version custom-v1 needs prompt_sha256, the sha256 of the prompt file (64 lowercase hex)")
	case spec.PassAt != nil && (*spec.PassAt <= 0 || *spec.PassAt > 1):
		return fmt.Errorf("judge pass_at %v: want a share above 0 and at most 1", *spec.PassAt)
	}
	return nil
}

// passAt is the share of weight a pass needs (default 1).
func passAt(spec schemas.JudgeSpec) float64 {
	if spec.PassAt != nil {
		return *spec.PassAt
	}
	return 1
}

// customTemplate reads the suite's prompt file and checks it against the
// pinned hash. The file must stay inside the suite folder.
func customTemplate(s *schemas.Suite, spec schemas.JudgeSpec) (string, error) {
	if err := checkCustomSpec(spec); err != nil {
		return "", err
	}
	if s == nil || s.Root == "" {
		return "", errors.New("custom judge prompt: the suite folder is not known")
	}
	b, err := utils.ReadFileInside(s.Root, spec.PromptFile)
	if err != nil {
		return "", fmt.Errorf("custom judge prompt %s: %w", spec.PromptFile, err)
	}
	if len(b) > maxCustomPromptBytes {
		return "", fmt.Errorf("custom judge prompt %s is larger than %d bytes", spec.PromptFile, maxCustomPromptBytes)
	}
	if got := utils.SHA256Bytes(b); got != spec.PromptSHA256 {
		return "", fmt.Errorf("custom judge prompt %s changed: its sha256 is %s, suite.yaml pins %s; review the change and update prompt_sha256", spec.PromptFile, got, spec.PromptSHA256)
	}
	t := string(b)
	if !strings.Contains(t, "{{response}}") {
		return "", fmt.Errorf("custom judge prompt %s has no {{response}}: the judge would never see the answer", spec.PromptFile)
	}
	return t, nil
}

// customFormat is the reply format a custom prompt gets.
func customFormat(crit []judgeCriterion) string {
	ids := make([]string, len(crit))
	for i, c := range crit {
		ids[i] = c.ID
	}
	return "Reply with only one JSON object and nothing else, with one entry per criterion id (" + strings.Join(ids, ", ") + "):\n" +
		`{"criteria":[{"id":"c1","met":true,"reason":"one short sentence"}]}`
}

// criteriaText lists the criteria for the prompt, without weights so the
// judge is not biased toward heavy ones.
func criteriaText(crit []judgeCriterion) string {
	var b strings.Builder
	for _, c := range crit {
		fmt.Fprintf(&b, "%s. %s\n", c.ID, c.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderCustomPrompt fills a custom template in one pass. The response and
// the transcript are capped, their fence markers broken up and wrapped in
// tagged markers, as for the shipped prompts.
func renderCustomPrompt(tmpl string, ans *schemas.AnswerSpec, answer, transcript string, limit int) (string, error) {
	crit, err := parseCriteria(ans.Rubric)
	if err != nil {
		return "", err
	}
	defence := strings.NewReplacer("<<<", "< < <", ">>>", "> > >")
	fence := func(s string) string {
		if r := []rune(s); len(r) > limit {
			s = string(r[:limit]) + "\n[truncated]"
		}
		return fenceAnswer(defence.Replace(strings.TrimSpace(s)))
	}
	format := customFormat(crit)
	out := strings.NewReplacer(
		"{{question}}", ans.Question,
		"{{reference}}", strings.Join(ans.Expected, "\nor: "),
		"{{expected}}", strings.Join(ans.Expected, "\nor: "),
		"{{rubric}}", ans.Rubric,
		"{{criteria}}", criteriaText(crit),
		"{{response}}", fence(answer),
		"{{transcript}}", fence(transcript),
		"{{format}}", format,
	).Replace(tmpl)
	if !strings.Contains(tmpl, "{{format}}") {
		out = strings.TrimRight(out, "\n") + "\n\n" + format + "\n"
	}
	return out, nil
}

// criterionVote is one vote's verdict on one criterion.
type criterionVote struct {
	Met    bool
	Reason string
}

// parseCriteriaReply reads one reply: a JSON object (a fenced block or text
// around the outermost braces is tolerated) with an entry, met set, for
// every criterion id. Anything else is unparsable.
func parseCriteriaReply(reply string, crit []judgeCriterion) (map[string]criterionVote, bool) {
	s := strings.TrimSpace(reply)
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return nil, false
	}
	var r struct {
		Criteria []struct {
			ID     string `json:"id"`
			Met    *bool  `json:"met"`
			Reason string `json:"reason"`
		} `json:"criteria"`
	}
	if json.Unmarshal([]byte(s[i:j+1]), &r) != nil {
		return nil, false
	}
	out := map[string]criterionVote{}
	for _, c := range r.Criteria {
		if c.Met != nil {
			out[strings.TrimSpace(c.ID)] = criterionVote{Met: *c.Met, Reason: strings.TrimSpace(c.Reason)}
		}
	}
	for _, c := range crit {
		if _, ok := out[c.ID]; !ok {
			return nil, false
		}
	}
	return out, true
}

// criteriaScore is the weighted share of crit that votes met.
func criteriaScore(votes map[string]criterionVote, crit []judgeCriterion) float64 {
	var got, total float64
	for _, c := range crit {
		total += c.Weight
		if votes[c.ID].Met {
			got += c.Weight
		}
	}
	if total == 0 {
		return 0
	}
	return got / total
}

// customScorer decides one reply's vote for a task's criteria.
func customScorer(crit []judgeCriterion, at float64) scorer {
	return func(reply string) string {
		v, ok := parseCriteriaReply(reply, crit)
		switch {
		case !ok:
			return VoteUnparsable
		case criteriaScore(v, crit)+1e-9 >= at:
			return VoteCorrect
		}
		return VoteIncorrect
	}
}

// applyCriteria records the per-criterion verdict on the grade: each
// criterion met by the majority of parsable votes, with the reason of the
// first vote on the majority side; the mean weighted score as the
// rubric_score dimension and each criterion's share of met votes as a
// criterion_<id> dimension. Passed is never changed by it.
func applyCriteria(grade *schemas.Grade, rec *schemas.JudgeRecord, ans *schemas.AnswerSpec, replies []string, at float64) {
	crit, err := parseCriteria(ans.Rubric)
	if err != nil || rec == nil {
		return
	}
	var parsed []map[string]criterionVote
	for _, r := range replies {
		if v, ok := parseCriteriaReply(r, crit); ok {
			parsed = append(parsed, v)
		}
	}
	rec.PassAt = at
	if len(parsed) == 0 {
		return
	}
	if grade.Dimensions == nil {
		grade.Dimensions = map[string]float64{}
	}
	score := 0.0
	for _, v := range parsed {
		score += criteriaScore(v, crit)
	}
	score /= float64(len(parsed))
	rec.Score = &score
	grade.Dimensions["rubric_score"] = score
	for _, c := range crit {
		met := 0
		for _, v := range parsed {
			if v[c.ID].Met {
				met++
			}
		}
		majority := 2*met > len(parsed)
		jc := schemas.JudgeCriterion{ID: c.ID, Text: c.Text, Weight: c.Weight, Met: majority}
		for _, v := range parsed {
			if v[c.ID].Met == majority {
				jc.Reason = v[c.ID].Reason
				break
			}
		}
		rec.Criteria = append(rec.Criteria, jc)
		grade.Dimensions["criterion_"+c.ID] = float64(met) / float64(len(parsed))
	}
}

// customTemplates is the template map of a custom prompt for calibration:
// its one template and the file's hash.
func customTemplates(spec schemas.JudgeSpec) map[string]string {
	return map[string]string{PromptCustomV1: spec.PromptSHA256}
}

// judgeTemplateFor is judgeTemplate for a suite's judge spec: the custom
// prompt file for custom-v1, else the shipped template.
func judgeTemplateFor(s *schemas.Suite, spec schemas.JudgeSpec, ans *schemas.AnswerSpec) (string, string, error) {
	if spec.PromptVersion != PromptCustomV1 {
		return judgeTemplate(spec.PromptVersion, ans)
	}
	if _, err := parseCriteria(ans.Rubric); err != nil {
		return "", "", err
	}
	t, err := customTemplate(s, spec)
	if err != nil {
		return "", "", err
	}
	return PromptCustomV1, t, nil
}

// PromptTemplatesFor is PromptTemplates for a suite's judge spec.
func PromptTemplatesFor(spec schemas.JudgeSpec) (map[string]string, error) {
	if spec.PromptVersion == PromptCustomV1 {
		if err := checkCustomSpec(spec); err != nil {
			return nil, err
		}
		return customTemplates(spec), nil
	}
	return PromptTemplates(spec.PromptVersion)
}
