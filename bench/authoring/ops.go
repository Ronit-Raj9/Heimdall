package authoring

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Op is one assist operation.
type Op string

// Assist ops built in this package (INFRA_I3 section 4.4).
const (
	OpRubric         Op = "rubric"
	OpCriterionWrong Op = "criterion_wrong"
	OpNearMissWrong  Op = "near_miss_wrong"
	OpExploit        Op = "exploit"
)

// AllOps are the ops in the order a job runs them: criterion_wrong needs
// the rubric's criteria.
var AllOps = []Op{OpRubric, OpCriterionWrong, OpNearMissWrong, OpExploit}

// PromptVersion is the version of every shipped op prompt.
const PromptVersion = "v1"

//go:embed prompts/*.md
var promptFS embed.FS

type opSpec struct {
	prompt string
	sha    string // sha256 of the system and op prompt, recorded on every generated field
}

var (
	systemPrompt string
	ops          = map[Op]opSpec{}
)

func init() {
	sys, err := promptFS.ReadFile("prompts/system-" + PromptVersion + ".md")
	if err != nil {
		panic(err)
	}
	systemPrompt = string(sys)
	for _, op := range AllOps {
		b, err := promptFS.ReadFile("prompts/" + string(op) + "-" + PromptVersion + ".md")
		if err != nil {
			panic(err)
		}
		ops[op] = opSpec{prompt: string(b), sha: sha(systemPrompt + "\x00" + string(b))}
	}
}

// ParseOp checks an op name.
func ParseOp(s string) (Op, error) {
	op := Op(strings.TrimSpace(s))
	if _, ok := ops[op]; !ok {
		return "", fmt.Errorf("assist op %q: want rubric, criterion_wrong, near_miss_wrong or exploit", s)
	}
	return op, nil
}

// PromptSHA is the hash recorded for op.
func PromptSHA(op Op) string { return ops[op].sha }

// renderPrompt puts in, as indented JSON, between BEGIN DATA and END DATA
// lines with a random nonce, so no text in the data can close the fence.
func renderPrompt(tmpl string, in any) (string, error) {
	data, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode op input: %w", err)
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(b)
	block := "BEGIN DATA " + nonce + "\n" + string(data) + "\nEND DATA " + nonce
	return strings.Replace(tmpl, "{{DATA}}", block, 1), nil
}

// Limits on what an op may write.
const (
	minCriteria   = 2
	maxCriteria   = 6
	maxTextRunes  = 400
	maxReplyRunes = 4000
	maxNearMiss   = 5
)

// ---- rubric ----

// RubricInput is what the rubric op reads.
type RubricInput struct {
	Instruction string `json:"instruction"`
	Question    string `json:"question,omitempty"`
	Reference   string `json:"reference_reply"`
}

// Criterion is one weighted rubric line.
type Criterion struct {
	Text   string `json:"text"`
	Weight int    `json:"weight"`
}

// RubricOutput is the rubric op's reply.
type RubricOutput struct {
	Criteria []Criterion `json:"criteria"`
}

// Validate checks 2 to 6 distinct criteria, each a sentence with a weight
// from 1 to 5.
func (o *RubricOutput) Validate() error {
	if n := len(o.Criteria); n < minCriteria || n > maxCriteria {
		return fmt.Errorf("want %d to %d criteria, got %d", minCriteria, maxCriteria, n)
	}
	seen := map[string]bool{}
	for i, c := range o.Criteria {
		t := strings.TrimSpace(c.Text)
		if utf8.RuneCountInString(t) < 8 || utf8.RuneCountInString(t) > maxTextRunes {
			return fmt.Errorf("criterion %d: text must be 8 to %d characters", i+1, maxTextRunes)
		}
		if c.Weight < 1 || c.Weight > 5 {
			return fmt.Errorf("criterion %d: weight %d is not 1 to 5", i+1, c.Weight)
		}
		k := norm(t)
		if seen[k] {
			return fmt.Errorf("criterion %d repeats another", i+1)
		}
		seen[k] = true
		o.Criteria[i].Text = t
	}
	return nil
}

// RenderRubric is the rubric text the judge reads: one numbered line per
// criterion with its weight.
func RenderRubric(cs []Criterion) string {
	var b strings.Builder
	b.WriteString("A correct reply meets every criterion below (weight 1 to 5 says how much it matters):\n")
	for i, c := range cs {
		fmt.Fprintf(&b, "%d. %s (weight %d)\n", i+1, c.Text, c.Weight)
	}
	return b.String()
}

// ParseRubric reads criteria back from a rubric text: one per numbered or
// bulleted line, with an optional "(weight N)" suffix (3 when absent). Lines
// that are not a list item are skipped.
func ParseRubric(rubric string) []Criterion {
	var out []Criterion
	for _, line := range strings.Split(rubric, "\n") {
		l := strings.TrimSpace(line)
		item := false
		if i := strings.IndexAny(l, ".)"); i > 0 && i <= 3 && strings.Trim(l[:i], "0123456789") == "" {
			l, item = strings.TrimSpace(l[i+1:]), true
		} else if strings.HasPrefix(l, "- ") || strings.HasPrefix(l, "* ") {
			l, item = strings.TrimSpace(l[2:]), true
		}
		if !item || l == "" {
			continue
		}
		w := 3
		if i := strings.LastIndex(l, "(weight "); i >= 0 && strings.HasSuffix(l, ")") {
			var n int
			if _, err := fmt.Sscanf(l[i:], "(weight %d)", &n); err == nil && n >= 1 && n <= 5 {
				w, l = n, strings.TrimSpace(l[:i])
			}
		}
		out = append(out, Criterion{Text: l, Weight: w})
	}
	return out
}

// ---- criterion_wrong ----

// CriterionWrongInput is what the criterion_wrong op reads.
type CriterionWrongInput struct {
	Instruction string      `json:"instruction"`
	Question    string      `json:"question,omitempty"`
	Reference   string      `json:"reference_reply"`
	Criteria    []Criterion `json:"criteria"`
}

// CriterionReply is a reply that breaks one criterion.
type CriterionReply struct {
	Criterion int    `json:"criterion"` // 1-based
	Reply     string `json:"reply"`
}

// CriterionWrongOutput is the criterion_wrong op's reply. Want and
// Reference are set by the caller before validation.
type CriterionWrongOutput struct {
	Replies []CriterionReply `json:"replies"`

	want      int
	reference string
}

// ExpectCriteria tells Validate how many criteria there are and what the
// reference is.
func (o *CriterionWrongOutput) ExpectCriteria(n int, reference string) *CriterionWrongOutput {
	o.want, o.reference = n, reference
	return o
}

// Validate checks exactly one reply per criterion, each a real reply that
// is not the reference.
func (o *CriterionWrongOutput) Validate() error {
	if len(o.Replies) != o.want {
		return fmt.Errorf("want exactly %d replies, one per criterion, got %d", o.want, len(o.Replies))
	}
	seen := map[int]bool{}
	for i, r := range o.Replies {
		if r.Criterion < 1 || r.Criterion > o.want {
			return fmt.Errorf("reply %d names criterion %d; criteria are numbered 1 to %d", i+1, r.Criterion, o.want)
		}
		if seen[r.Criterion] {
			return fmt.Errorf("criterion %d has two replies", r.Criterion)
		}
		seen[r.Criterion] = true
		if err := checkReply(r.Reply, o.reference); err != nil {
			return fmt.Errorf("reply for criterion %d: %v", r.Criterion, err)
		}
		o.Replies[i].Reply = strings.TrimSpace(r.Reply)
	}
	return nil
}

// ---- near_miss_wrong ----

// NearMissInput is what the near_miss_wrong op reads.
type NearMissInput struct {
	Instruction string `json:"instruction"`
	Question    string `json:"question,omitempty"`
	Reference   string `json:"reference_answer"`
	Count       int    `json:"count"`
}

// NearMissAnswer is one plausible wrong answer.
type NearMissAnswer struct {
	Kind   string `json:"kind"`
	Answer string `json:"answer"`
}

// NearMissOutput is the near_miss_wrong op's reply.
type NearMissOutput struct {
	Answers []NearMissAnswer `json:"answers"`

	want      int
	reference string
}

// Expect sets the count asked for and the reference.
func (o *NearMissOutput) Expect(n int, reference string) *NearMissOutput {
	o.want, o.reference = n, reference
	return o
}

var nearMissKinds = map[string]bool{"entity_swapped": true, "step_missing": true, "policy_broken": true, "wrong_number": true}

// Validate checks the count, known kinds, and answers that differ from the
// reference and from each other.
func (o *NearMissOutput) Validate() error {
	if len(o.Answers) == 0 || len(o.Answers) > maxNearMiss || (o.want > 0 && len(o.Answers) != o.want) {
		return fmt.Errorf("want %d answers, got %d", o.want, len(o.Answers))
	}
	seen := map[string]bool{}
	for i, a := range o.Answers {
		if !nearMissKinds[a.Kind] {
			return fmt.Errorf("answer %d: kind %q is not entity_swapped, step_missing, policy_broken or wrong_number", i+1, a.Kind)
		}
		if err := checkReply(a.Answer, o.reference); err != nil {
			return fmt.Errorf("answer %d: %v", i+1, err)
		}
		k := norm(a.Answer)
		if seen[k] {
			return fmt.Errorf("answer %d repeats another", i+1)
		}
		seen[k] = true
		o.Answers[i].Answer = strings.TrimSpace(a.Answer)
	}
	return nil
}

// ---- exploit ----

// ExploitInput is what the exploit op reads.
type ExploitInput struct {
	Instruction string `json:"instruction"`
	Question    string `json:"question,omitempty"`
	Checks      string `json:"checks"`         // the rubric, or how the answer is matched
	Reference   string `json:"correct_answer"` // what the exploit must not contain
}

// ExploitOutput is the exploit op's reply.
type ExploitOutput struct {
	Kind  string `json:"kind"`
	Reply string `json:"reply"`

	reference string
}

// Expect sets the reference the exploit must not contain.
func (o *ExploitOutput) Expect(reference string) *ExploitOutput {
	o.reference = reference
	return o
}

var exploitKinds = map[string]bool{"claim_without_acting": true, "quote_policy": true, "flatter_grader": true, "hedge_everything": true}

// Validate checks a known kind and a reply that does not hold the
// reference.
func (o *ExploitOutput) Validate() error {
	if !exploitKinds[o.Kind] {
		return fmt.Errorf("kind %q is not claim_without_acting, quote_policy, flatter_grader or hedge_everything", o.Kind)
	}
	if err := checkReply(o.Reply, o.reference); err != nil {
		return err
	}
	if r := norm(o.reference); r != "" && strings.Contains(" "+norm(o.Reply)+" ", " "+r+" ") {
		return errors.New("the exploit contains the correct answer")
	}
	o.Reply = strings.TrimSpace(o.Reply)
	return nil
}

// checkReply refuses an empty, oversized or reference-equal reply.
func checkReply(reply, reference string) error {
	r := strings.TrimSpace(reply)
	if r == "" {
		return errors.New("empty")
	}
	if utf8.RuneCountInString(r) > maxReplyRunes {
		return fmt.Errorf("longer than %d characters", maxReplyRunes)
	}
	if reference != "" && norm(r) == norm(reference) {
		return errors.New("same as the reference")
	}
	return nil
}

// norm lower-cases and collapses spaces and punctuation, for equality.
func norm(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	}), " ")
}
