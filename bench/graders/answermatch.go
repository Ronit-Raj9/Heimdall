package graders

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// AnswerMatchType is the part name suites use to select this grader.
const AnswerMatchType = "answer_match"

// Match modes of an AnswerSpec (AnswerSpec.Match).
const (
	MatchExact       = "exact"
	MatchContainsAll = "contains_all"
	MatchRegex       = "regex"
	MatchNumeric     = "numeric"
	MatchChoice      = "choice"
	MatchJSON        = "json"
)

// Normalizers an AnswerSpec may name (AnswerSpec.Normalize). They always run
// in the order trim, lower, strip_punct, collapse_space, trim, whatever order
// the spec lists them in, so two specs that name the same set agree.
const (
	NormLower         = "lower"
	NormStripPunct    = "strip_punct"
	NormCollapseSpace = "collapse_space"
	NormTrim          = "trim"
)

// AnswerFile is the run folder file holding the agent's exported answer.
const AnswerFile = "answer.txt"

// GradeModeAnswer is Grade.Mode for a grade reached from an answer.
const GradeModeAnswer = "answer"

const (
	answerMatchVersion = "1.0.0"
	defaultAnswerSpec  = "answer.yaml"
	defaultVerifierDir = "verifier"
	maxAnswerBytes     = 1 << 20
	maxSpecBytes       = 1 << 20
	maxQuote           = 120
)

// abstainPhrases are what an answer must contain (after lower-casing,
// folding curly apostrophes and collapsing spaces) to count as saying the
// answer is not known. The list is fixed so the grade is deterministic; it
// covers the phrasings LoCoMo's own scorer accepts ("no information
// available", "not mentioned") and the plain ways of saying "I do not know".
// docs/answer-grading.md prints it.
var abstainPhrases = []string{
	"i don't know", "i do not know", "i dont know",
	"i don't remember", "i do not remember", "i don't recall", "i do not recall",
	"i'm not sure", "i am not sure",
	"not mentioned", "never mentioned", "wasn't mentioned", "was not mentioned",
	"didn't mention", "did not mention", "never told", "didn't tell", "did not tell",
	"no information", "not enough information", "insufficient information",
	"information is not available", "no record of",
	"cannot be determined", "can't be determined", "cannot determine", "can't determine",
	"cannot answer", "can't answer", "unable to answer", "unanswerable", "not answerable",
	"not known", "unknown",
}

var (
	numberRe  = regexp.MustCompile(`[-+]?(?:\d{1,3}(?:,\d{3})+|\d+)(?:\.\d+)?`)
	wordRe    = regexp.MustCompile(`[a-z]+`)
	choiceRe  = regexp.MustCompile(`^\(?([a-z]|\d{1,2})(?:[).:]|\s|$)`)
	answerPfx = regexp.MustCompile(`^(?:the\s+)?(?:final\s+)?answer\s*(?:is|:)\s*`)
	// numberWords are the spelled numbers numeric matching reads: an answer
	// of "four" is the number 4.
	numberWords = map[string]float64{
		"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7,
		"eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13,
		"fourteen": 14, "fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18,
		"nineteen": 19, "twenty": 20,
	}
	knownNormalizers = map[string]bool{NormLower: true, NormStripPunct: true, NormCollapseSpace: true, NormTrim: true}
)

var _ schemas.Grader = (*answerMatch)(nil)

// answerMatch grades an answer task without a model: it reads the agent's
// answer from the run folder and checks it against the task's hidden
// AnswerSpec. It runs no container and reaches no network.
type answerMatch struct{}

// newAnswerMatch returns the answer_match grader.
func newAnswerMatch() *answerMatch { return &answerMatch{} }

// GetName implements schemas.Part.
func (g *answerMatch) GetName() string { return AnswerMatchType }

// GetVersion implements schemas.Part.
func (g *answerMatch) GetVersion() string { return answerMatchVersion }

// VerifierNetwork reports "none": the grader runs no verifier container and
// opens no connection, it only compares two files on the host.
func (g *answerMatch) VerifierNetwork() string { return verifierNetwork }

// Grade reads <run folder>/answer.txt and the task's AnswerSpec and decides.
// A missing or empty answer fails with the reason: the agent was asked for
// an answer and gave none. A spec that is missing, malformed or uses an
// unknown mode makes the evaluator invalid, since nothing was decided about
// the agent. A task whose output kind is not answer is a configuration
// error. Grade.Mode is "answer" and Grade.Category the spec's category.
func (g *answerMatch) Grade(_ context.Context, rc *schemas.RunContext, diff *schemas.Diff) (*schemas.Grade, error) {
	if err := checkAnswerTask(rc); err != nil {
		return nil, fmt.Errorf("%s: %w", AnswerMatchType, err)
	}
	grade := baseAnswerGrade(rc, diff, answerMatchVersion)
	spec, err := LoadAnswerSpec(rc.Task)
	if err == nil {
		err = ValidateAnswerSpec(spec)
	}
	if err != nil {
		grade.InvalidEvaluator = true
		grade.Reason = "answer spec: " + err.Error()
		return grade, nil
	}
	grade.Category = spec.Category
	answer, problem, err := ReadAnswer(rc.Dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", AnswerMatchType, err)
	}
	if problem != "" {
		grade.Reason = problem
		return grade, nil
	}
	grade.Passed, grade.Reason = MatchAnswer(spec, answer)
	return grade, nil
}

// LoadAnswerSpec reads the task's hidden AnswerSpec: Task.Verifier.Answer
// when set, else answer.yaml in the verifier tests folder (verifier/ when the
// task names none), relative to the task folder and never outside it.
// Unknown keys are an error, so a typo cannot silently drop a field.
func LoadAnswerSpec(t *schemas.Task) (*schemas.AnswerSpec, error) {
	if t == nil || t.Dir == "" {
		return nil, errors.New("task is not loaded")
	}
	rel := AnswerSpecPath(t)
	abs, err := utils.ResolveInside(t.Dir, rel)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", rel, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	if fi.Size() > maxSpecBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", rel, maxSpecBytes)
	}
	raw, err := utils.ReadFileInside(t.Dir, rel)
	if err != nil {
		return nil, err
	}
	var spec schemas.AnswerSpec
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&spec); err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	return &spec, nil
}

// AnswerSpecPath is the task-relative path of the hidden answer file.
func AnswerSpecPath(t *schemas.Task) string {
	if t.Verifier.Answer != "" {
		return t.Verifier.Answer
	}
	dir := t.Verifier.Tests
	if dir == "" {
		dir = defaultVerifierDir
	}
	return path.Join(filepath.ToSlash(dir), defaultAnswerSpec)
}

// ValidateAnswerSpec checks that spec can be matched: a known mode, known
// normalizers, at least one expected value unless the spec is an abstention,
// regular expressions that compile, numbers that parse, JSON that decodes and
// a tolerance that is finite and not negative.
func ValidateAnswerSpec(spec *schemas.AnswerSpec) error {
	for _, n := range spec.Normalize {
		if !knownNormalizers[n] {
			return fmt.Errorf("normalize: unknown normalizer %q (want lower, strip_punct, collapse_space or trim)", n)
		}
	}
	if math.IsNaN(spec.Tolerance) || math.IsInf(spec.Tolerance, 0) || spec.Tolerance < 0 {
		return fmt.Errorf("tolerance: %v is not a finite number >= 0", spec.Tolerance)
	}
	if spec.Abstain {
		return nil
	}
	if len(spec.Expected) == 0 {
		return errors.New("expected: empty; a spec that is not an abstention needs at least one expected answer")
	}
	switch spec.Match {
	case MatchExact, MatchContainsAll, MatchChoice:
	case MatchRegex:
		for i, e := range spec.Expected {
			if _, err := regexp.Compile(e); err != nil {
				return fmt.Errorf("expected[%d]: %w", i, err)
			}
		}
	case MatchNumeric:
		for i, e := range spec.Expected {
			if _, err := parseNumber(e); err != nil {
				return fmt.Errorf("expected[%d]: %w", i, err)
			}
		}
	case MatchJSON:
		for i, e := range spec.Expected {
			if _, err := decodeJSONValue(e); err != nil {
				return fmt.Errorf("expected[%d]: %w", i, err)
			}
		}
	case "":
		return errors.New("match: empty (want exact, contains_all, regex, numeric, choice or json)")
	default:
		return fmt.Errorf("match: unknown mode %q (want exact, contains_all, regex, numeric, choice or json)", spec.Match)
	}
	return nil
}

// ReadAnswer reads <dir>/answer.txt. problem is set, and err nil, when the
// answer is missing, empty or larger than 1 MiB: each is a failed answer
// with that reason. err is set only when the file exists and cannot be
// read.
func ReadAnswer(dir string) (answer, problem string, err error) {
	if dir == "" {
		return "", "", errors.New("run context has no run folder")
	}
	f, err := os.Open(filepath.Join(dir, AnswerFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", "no answer: " + AnswerFile + " is not in the run folder", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("open %s: %w", AnswerFile, err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxAnswerBytes+1))
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", AnswerFile, err)
	}
	if len(b) > maxAnswerBytes {
		return "", fmt.Sprintf("answer is larger than %d bytes", maxAnswerBytes), nil
	}
	answer = strings.TrimPrefix(string(b), "\ufeff")
	if strings.TrimSpace(answer) == "" {
		return "", "no answer: " + AnswerFile + " is empty", nil
	}
	return answer, "", nil
}

// MatchAnswer decides whether answer satisfies spec, which must have passed
// ValidateAnswerSpec, and says why in a short reason that quotes the answer
// but never the expected value. An abstention spec passes only an answer that
// says the answer is not known, whatever its match mode.
func MatchAnswer(spec *schemas.AnswerSpec, answer string) (bool, string) {
	if spec.Abstain {
		if SaysUnknown(answer) {
			return true, "answer says it is not known, as expected"
		}
		return false, fmt.Sprintf("expected the answer to say it is not known; got %s", quote(answer))
	}
	ok := false
	switch spec.Match {
	case MatchExact:
		a := normalize(answer, spec.Normalize)
		for _, e := range spec.Expected {
			ok = ok || a == normalize(e, spec.Normalize)
		}
	case MatchContainsAll:
		a := normalize(answer, spec.Normalize)
		ok = true
		for _, e := range spec.Expected {
			ok = ok && strings.Contains(a, normalize(e, spec.Normalize))
		}
	case MatchRegex:
		a := normalize(answer, spec.Normalize)
		for _, e := range spec.Expected {
			ok = ok || regexp.MustCompile(e).MatchString(a)
		}
	case MatchNumeric:
		return matchNumeric(spec, answer)
	case MatchChoice:
		ok = matchChoice(spec, answer)
	case MatchJSON:
		return matchJSON(spec, answer)
	}
	if ok {
		return true, spec.Match + ": answer matches"
	}
	return false, fmt.Sprintf("%s: answer does not match; got %s", spec.Match, quote(answer))
}

// SaysUnknown reports whether answer contains one of the fixed abstention
// phrases.
func SaysUnknown(answer string) bool {
	a := strings.NewReplacer("’", "'", "‘", "'").Replace(strings.ToLower(answer))
	a = strings.Join(strings.Fields(a), " ")
	for _, p := range abstainPhrases {
		if strings.Contains(a, p) {
			return true
		}
	}
	return false
}

// checkAnswerTask refuses a run context without a task or whose task's
// output kind is not answer.
func checkAnswerTask(rc *schemas.RunContext) error {
	if rc == nil || rc.Task == nil {
		return errors.New("run context has no task")
	}
	if k := utils.OutputKind(rc.Suite, rc.Task); k != schemas.OutputAnswer {
		return fmt.Errorf("task %s has output kind %s; this grader reads answers (output kind answer)", rc.Task.ID, k)
	}
	return nil
}

// baseAnswerGrade is a failing grade carrying the fields every answer grade
// has.
func baseAnswerGrade(rc *schemas.RunContext, diff *schemas.Diff, version string) *schemas.Grade {
	g := &schemas.Grade{GraderVersion: version, Mode: GradeModeAnswer}
	if rc.Suite != nil {
		g.SuiteVersion = rc.Suite.Version
	}
	if diff != nil {
		g.DiffSHA256 = diff.SHA256
	}
	return g
}

// normalize applies the named normalizers in their fixed order.
func normalize(s string, names []string) string {
	on := map[string]bool{}
	for _, n := range names {
		on[n] = true
	}
	if on[NormTrim] {
		s = strings.TrimSpace(s)
	}
	if on[NormLower] {
		s = strings.ToLower(s)
	}
	if on[NormStripPunct] {
		s = strings.Map(func(r rune) rune {
			if unicode.IsPunct(r) {
				return -1
			}
			return r
		}, s)
	}
	if on[NormCollapseSpace] {
		s = strings.Join(strings.Fields(s), " ")
	}
	if on[NormTrim] {
		s = strings.TrimSpace(s)
	}
	return s
}

// matchNumeric passes when the answer states exactly one distinct number and
// it is within the tolerance of an expected value. Digits (with thousands
// commas and a decimal part) and the words zero to twenty are read; an answer
// naming two different numbers is ambiguous and fails.
func matchNumeric(spec *schemas.AnswerSpec, answer string) (bool, string) {
	nums := answerNumbers(answer)
	if len(nums) == 0 {
		return false, fmt.Sprintf("numeric: no number in the answer; got %s", quote(answer))
	}
	if len(nums) > 1 {
		return false, fmt.Sprintf("numeric: the answer states %d different numbers; got %s", len(nums), quote(answer))
	}
	for _, e := range spec.Expected {
		want, _ := parseNumber(e)
		if math.Abs(nums[0]-want) <= spec.Tolerance {
			return true, "numeric: answer matches"
		}
	}
	return false, fmt.Sprintf("numeric: %s is not within %v of the expected value", strconv.FormatFloat(nums[0], 'g', -1, 64), spec.Tolerance)
}

// answerNumbers lists the distinct numbers stated in s, in order.
func answerNumbers(s string) []float64 {
	low := strings.ToLower(s)
	var out []float64
	seen := map[float64]bool{}
	add := func(v float64) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, m := range numberRe.FindAllString(low, -1) {
		if v, err := parseNumber(m); err == nil {
			add(v)
		}
	}
	for _, w := range wordRe.FindAllString(low, -1) {
		if v, ok := numberWords[w]; ok {
			add(v)
		}
	}
	return out
}

// parseNumber reads a decimal number, allowing thousands commas and
// surrounding spaces.
func parseNumber(s string) (float64, error) {
	v, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", ""), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	return v, nil
}

// matchChoice reads the option the answer picks and compares it with the
// expected options, case-insensitively and after the spec's normalizers.
// The answer may be the option alone ("B", "(b)", "b."), start with it
// ("B) Paris", "(b) not mentioned") or say "answer: B"; an expected option
// longer than one label is compared with the whole normalized answer.
func matchChoice(spec *schemas.AnswerSpec, answer string) bool {
	a := strings.ToLower(strings.TrimSpace(answer))
	a = answerPfx.ReplaceAllString(a, "")
	label := ""
	if m := choiceRe.FindStringSubmatch(a); m != nil {
		label = m[1]
	}
	whole := normalize(a, append([]string{NormLower, NormTrim}, spec.Normalize...))
	for _, e := range spec.Expected {
		want := strings.ToLower(strings.Trim(strings.TrimSpace(e), "().:"))
		if label != "" && want == label {
			return true
		}
		if whole == normalize(e, append([]string{NormLower, NormTrim}, spec.Normalize...)) {
			return true
		}
	}
	return false
}

// matchJSON passes when the whole answer is one JSON value equal to an
// expected value. Object key order and number spelling (1 and 1.0) do not
// matter; normalizers are not applied.
func matchJSON(spec *schemas.AnswerSpec, answer string) (bool, string) {
	got, err := decodeJSONValue(answer)
	if err != nil {
		return false, fmt.Sprintf("json: the answer is not one JSON value (%v); got %s", err, quote(answer))
	}
	for _, e := range spec.Expected {
		want, _ := decodeJSONValue(e)
		if reflect.DeepEqual(got, want) {
			return true, "json: answer matches"
		}
	}
	return false, fmt.Sprintf("json: answer does not match; got %s", quote(answer))
}

// decodeJSONValue decodes exactly one JSON value from s.
func decodeJSONValue(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data after the JSON value")
	}
	return v, nil
}

// quote shortens s for a reason line.
func quote(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxQuote {
		s = string(r[:maxQuote]) + "..."
	}
	return strconv.Quote(s)
}
