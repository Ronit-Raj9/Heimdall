package scaffold

// Generated wrong answers for judged rows (lane Y07). A judged row's
// readiness check must show the judge answers it has to fail; "I don't
// know." never reaches it (the judge guard fails a refusal unasked). When a
// row names no wrong answers, the import writes plausible ones built from
// the row itself, deterministically, so the same file always gives the same
// task: the reference with its numbers changed, with a statement negated,
// with a yes or no flipped, another row's reference (the right answer to a
// different question), and, when nothing else applies, an answer that
// sounds helpful but states nothing. They are stored apart from the
// author's (generated_wrong_answers in verifier/answer.yaml) and the author
// can edit or remove them.

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// maxGeneratedWrong is how many wrong answers a row gets generated; a row
// with a rubric gets one more per rubric criterion, up to maxRubricWrong.
const (
	maxGeneratedWrong = 3
	maxRubricWrong    = 6
)

// Fallback wrong answers: helpful in tone, empty in content, so a judge that
// passes them grades tone, not substance.
const (
	vagueAnswer = "It depends on several factors, so the exact answer varies from case to case."
	vagueReply  = "Thanks for reaching out. I have passed your message to the right team and someone will be in touch soon."
)

var numberRE = regexp.MustCompile(`\d+(?:[.,]\d+)?`)

// negations are statement verbs and their negated forms, tried in order on
// the first sentence that has one.
var negations = [][2]string{
	{" is not ", " is "}, {" are not ", " are "}, {" was not ", " was "}, {" were not ", " were "},
	{" cannot ", " can "}, {" will not ", " will "}, {" does not ", " does "}, {" do not ", " do "},
	{" isn't ", " is "}, {" aren't ", " are "}, {" can't ", " can "}, {" won't ", " will "}, {" doesn't ", " does "}, {" don't ", " do "},
}

var affirmatives = [][2]string{
	{" is ", " is not "}, {" are ", " are not "}, {" was ", " was not "}, {" were ", " were not "},
	{" can ", " cannot "}, {" will ", " will not "}, {" does ", " does not "}, {" should ", " should not "},
	{" must ", " must not "}, {" has ", " does not have "}, {" have ", " do not have "},
}

// GenerateWrongAnswers returns up to three plausible wrong answers for a
// judged row whose right answer is reference. other is the reference of
// another row of the same import ("" when there is none). Nothing it returns
// equals the reference or an expected answer after normalising case and
// spaces.
func GenerateWrongAnswers(r Row, reference, other string) []string {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return nil
	}
	right := map[string]bool{normWrong(reference): true}
	for _, e := range r.Expected {
		right[normWrong(e)] = true
	}
	var out []string
	limit := maxGeneratedWrong
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || len(out) >= limit || right[normWrong(s)] {
			return
		}
		for _, o := range out {
			if normWrong(o) == normWrong(s) {
				return
			}
		}
		out = append(out, s)
	}
	// A rubric reply: one reply per criterion that breaks it, so the judge
	// is shown a reply missing each thing the rubric asks for.
	if crit := rubricCriteria(r.Rubric); len(crit) > 0 {
		limit = min(maxRubricWrong, len(crit)+maxGeneratedWrong)
		uncovered := false
		for _, c := range crit {
			v := violate(reference, c)
			uncovered = uncovered || v == "" || right[normWrong(v)]
			add(v)
		}
		if uncovered {
			// A criterion no edit of the reference breaks still gets a reply
			// that misses it: the polite reply that does nothing misses
			// every criterion.
			add(vagueReply)
		}
	}
	add(flipYesNo(reference))
	add(shiftNumbers(reference))
	add(negate(reference))
	add(other)
	if len(out) == 0 {
		if r.Ticket {
			add(vagueReply)
		} else {
			add(vagueAnswer)
		}
	}
	return out
}

func normWrong(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// shiftNumbers adds one to every number of s (keeping decimals and
// thousands separators), "" when s has none.
func shiftNumbers(s string) string {
	if !numberRE.MatchString(s) {
		return ""
	}
	return numberRE.ReplaceAllStringFunc(s, func(n string) string {
		plain := strings.ReplaceAll(n, ",", "")
		if strings.Contains(n, ",") && strings.Count(n, ",") == 1 && len(n)-strings.Index(n, ",") != 4 {
			plain = strings.ReplaceAll(n, ",", ".") // decimal comma
		}
		if i, err := strconv.ParseInt(plain, 10, 64); err == nil {
			return strconv.FormatInt(i+1, 10)
		}
		if f, err := strconv.ParseFloat(plain, 64); err == nil {
			dec := len(plain) - strings.Index(plain, ".") - 1
			return strconv.FormatFloat(f+1, 'f', dec, 64)
		}
		return n
	})
}

// negate flips the first statement verb of s: a negated one loses its
// "not", else the first affirmative one gains it. "" when s has neither.
func negate(s string) string {
	padded := " " + s + " "
	low := strings.ToLower(padded)
	best, pair := -1, [2]string{}
	for _, set := range [][][2]string{negations, affirmatives} {
		for _, p := range set {
			if i := strings.Index(low, p[0]); i >= 0 && (best < 0 || i < best) {
				best, pair = i, p
			}
		}
		if best >= 0 {
			break
		}
	}
	if best < 0 {
		return ""
	}
	out := padded[:best] + pair[1] + padded[best+len(pair[0]):]
	return strings.TrimSpace(out)
}

// flipYesNo turns an answer that opens with yes into one that opens with no
// and the other way round, "" otherwise.
func flipYesNo(s string) string {
	word := strings.TrimLeftFunc(s, func(r rune) bool { return !unicode.IsLetter(r) })
	end := strings.IndexFunc(word, func(r rune) bool { return !unicode.IsLetter(r) })
	first, rest := word, ""
	if end >= 0 {
		first, rest = word[:end], word[end:]
	}
	switch strings.ToLower(first) {
	case "yes":
		return matchCase(first, "no") + rest
	case "no":
		return matchCase(first, "yes") + rest
	}
	return ""
}

func matchCase(model, word string) string {
	switch {
	case model == strings.ToUpper(model):
		return strings.ToUpper(word)
	case len(model) > 0 && unicode.IsUpper(rune(model[0])):
		return strings.ToUpper(word[:1]) + word[1:]
	}
	return word
}

var (
	criterionSplitRE = regexp.MustCompile(`\n+|;|(?:[.!?])\s+`)
	sentenceRE       = regexp.MustCompile(`[^.!?\n]+[.!?]*`)
	wordRE           = regexp.MustCompile(`[\p{L}\p{N}]+`)
)

// rubricCriteria splits a rubric into its criteria: lines, bullets,
// semicolons and sentences, each with at least two words.
func rubricCriteria(rubric string) []string {
	var out []string
	for _, c := range criterionSplitRE.Split(rubric, -1) {
		c = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(c), "-*0123456789.) "))
		if len(strings.Fields(c)) >= 2 {
			out = append(out, c)
		}
	}
	return out
}

// keywords are a text's content words, cut to a five-letter stem so
// "refund" matches "refunded".
func keywords(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range wordRE.FindAllString(strings.ToLower(s), -1) {
		if len([]rune(w)) < 4 {
			continue
		}
		if r := []rune(w); len(r) > 5 {
			w = string(r[:5])
		}
		out[w] = true
	}
	return out
}

// violate is reference changed so it breaks criterion: the sentence that
// meets it (most shared keywords) is dropped when the reply has others,
// else negated or its numbers changed. "" when no sentence meets it.
func violate(reference, criterion string) string {
	want := keywords(criterion)
	sentences := sentenceRE.FindAllString(reference, -1)
	best, score := -1, 0
	for i, sn := range sentences {
		n := 0
		for k := range keywords(sn) {
			if want[k] {
				n++
			}
		}
		if n > score {
			best, score = i, n
		}
	}
	if best < 0 {
		return ""
	}
	if len(sentences) > 1 {
		rest := append(append([]string(nil), sentences[:best]...), sentences[best+1:]...)
		return strings.Join(strings.Fields(strings.Join(rest, " ")), " ")
	}
	if c := dropClause(reference, want); c != "" {
		return c
	}
	if n := negate(reference); n != "" {
		return n
	}
	return shiftNumbers(reference)
}

var clauseRE = regexp.MustCompile(`,\s+(?:and\s+)?|\s+and\s+`)

// dropClause is a one-sentence reference without the clause that meets
// the criterion keywords want ("Apologise, confirm the charge and refund
// it" without "refund it" for "Refund the charge"), "" when the sentence
// has one clause or no clause shares a keyword with want.
func dropClause(reference string, want map[string]bool) string {
	s := strings.TrimSpace(reference)
	end := strings.TrimRight(s, ".!?")
	stop := s[len(end):]
	cuts := clauseRE.FindAllStringIndex(end, -1)
	if len(cuts) == 0 {
		return ""
	}
	var clauses []string
	prev := 0
	for _, c := range cuts {
		clauses = append(clauses, end[prev:c[0]])
		prev = c[1]
	}
	clauses = append(clauses, end[prev:])
	best, score := -1, 0
	for i, c := range clauses {
		n := 0
		for k := range keywords(c) {
			if want[k] {
				n++
			}
		}
		if n > score {
			best, score = i, n
		}
	}
	if best < 0 {
		return ""
	}
	rest := append(append([]string(nil), clauses[:best]...), clauses[best+1:]...)
	for i := range rest {
		rest[i] = strings.TrimSpace(rest[i])
	}
	var out string
	switch len(rest) {
	case 1:
		out = rest[0]
	default:
		out = strings.Join(rest[:len(rest)-1], ", ") + " and " + rest[len(rest)-1]
	}
	if r := []rune(out); len(r) > 0 {
		out = strings.ToUpper(string(r[:1])) + string(r[1:])
	}
	return out + stop
}
