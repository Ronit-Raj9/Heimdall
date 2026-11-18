// Package gaiascore is GAIA's quasi-exact match: the rules of
// question_scorer in scorer.py of the GAIA leaderboard space
// (huggingface.co/spaces/gaia-benchmark/leaderboard, revision 9f133d71,
// read on 2026-11-18), ported to Go with Python's float() and str
// semantics kept where they decide a grade. docs/importers-research.md has
// the rules and the known differences.
//
// The ground truth picks the rule:
//
//   - it parses as a Python float: the answer, with "$", "%" and ","
//     removed, must parse to the same float (an answer that does not parse
//     is +Inf, which equals no finite truth);
//   - else, when it holds "," or ";": both are split on [,;] into lists of
//     the same length, and each pair is compared as a number (when the
//     truth element parses as a float) or as a string with white space
//     removed and lower-cased, punctuation kept;
//   - else: both strings with every white space character removed,
//     lower-cased and ASCII punctuation (Python's string.punctuation)
//     removed must be equal.
//
// The package is a leaf (no bench imports) so the answer_match grader can
// use it.
package gaiascore

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Score reports whether modelAnswer matches groundTruth by GAIA's rules.
// It is pure.
func Score(modelAnswer, groundTruth string) bool {
	if truth, ok := PyFloat(groundTruth); ok {
		return normalizeNumber(modelAnswer) == truth
	}
	if strings.ContainsAny(groundTruth, ",;") {
		gt, ma := splitList(groundTruth), splitList(modelAnswer)
		if len(gt) != len(ma) {
			return false
		}
		for i := range gt {
			if want, ok := PyFloat(gt[i]); ok {
				if normalizeNumber(ma[i]) != want {
					return false
				}
				continue
			}
			if NormalizeString(ma[i], false) != NormalizeString(gt[i], false) {
				return false
			}
		}
		return true
	}
	return NormalizeString(modelAnswer, true) == NormalizeString(groundTruth, true)
}

// Kind names the rule Score applies to a ground truth: "number", "list" or
// "string".
func Kind(groundTruth string) string {
	if _, ok := PyFloat(groundTruth); ok {
		return "number"
	}
	if strings.ContainsAny(groundTruth, ",;") {
		return "list"
	}
	return "string"
}

// finalAnswerRE finds GAIA's answer template line, "FINAL ANSWER: ...".
var finalAnswerRE = regexp.MustCompile(`(?im)^[ \t]*(?:\*\*)?final answer(?:\*\*)?[ \t]*:(?:\*\*)?[ \t]*(.*)$`)

// Extract is the part of an answer file that is scored: the text after the
// last "FINAL ANSWER:" line when the file uses GAIA's answer template (the
// leaderboard is sent that extracted text), else the whole file with
// surrounding white space trimmed. It is pure.
func Extract(answer string) string {
	m := finalAnswerRE.FindAllStringSubmatch(answer, -1)
	if len(m) == 0 {
		return strings.TrimSpace(answer)
	}
	return strings.TrimSpace(m[len(m)-1][1])
}

// normalizeNumber is normalize_number_str: "$", "%" and "," removed, then
// float(); a string that does not parse is +Inf.
func normalizeNumber(s string) float64 {
	s = strings.NewReplacer("$", "", "%", "", ",", "").Replace(s)
	if f, ok := PyFloat(s); ok {
		return f
	}
	return math.Inf(1)
}

// splitList is re.split("[,;]", s): no trimming, empty parts kept.
func splitList(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' || s[i] == ';' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// punctuation is Python's string.punctuation.
const punctuation = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

// NormalizeString is normalize_str: every white space character removed
// (re.sub(r"\s", "", s)), lower-cased, and with removePunct the characters
// of string.punctuation removed. It is pure.
func NormalizeString(s string, removePunct bool) string {
	var b strings.Builder
	for _, r := range s {
		if pyIsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	out := strings.ToLower(b.String())
	if !removePunct {
		return out
	}
	return strings.Map(func(r rune) rune {
		if r < 0x80 && strings.ContainsRune(punctuation, r) {
			return -1
		}
		return r
	}, out)
}

// pyIsSpace is Python's str.isspace for one character, which is also what
// \s matches in a str pattern and what float() strips: Unicode white space
// plus the four ASCII separators U+001C to U+001F.
func pyIsSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// PyFloat parses s as Python's float(s) does: surrounding white space
// stripped, an optional sign, then "inf", "infinity" or "nan" in any case,
// or a decimal number whose digits may be Unicode decimal digits and may be
// grouped by single underscores between digits, with an optional exponent.
// Hexadecimal and Go-only forms are refused. It is pure.
func PyFloat(s string) (float64, bool) {
	s = strings.TrimFunc(s, pyIsSpace)
	if s == "" {
		return 0, false
	}
	body := s
	neg := false
	if body[0] == '+' || body[0] == '-' {
		neg = body[0] == '-'
		body = body[1:]
	}
	switch strings.ToLower(body) {
	case "inf", "infinity":
		if neg {
			return math.Inf(-1), true
		}
		return math.Inf(1), true
	case "nan":
		return math.NaN(), true
	}
	ascii, ok := pyDecimal(body)
	if !ok {
		return 0, false
	}
	if neg {
		ascii = "-" + ascii
	}
	f, err := strconv.ParseFloat(ascii, 64)
	if err != nil {
		// Out of range: Python returns inf for an overflowing literal and
		// 0 for an underflowing one; ParseFloat reports both with the
		// right value and ErrRange.
		if ne, isNum := err.(*strconv.NumError); isNum && ne.Err == strconv.ErrRange {
			return f, true
		}
		return 0, false
	}
	return f, true
}

// pyDecimal checks body (no sign) against Python's float grammar,
//
//	number   ::= [digitpart] "." digitpart | digitpart ["."]
//	exponent ::= ("e" | "E") [sign] digitpart
//	digitpart ::= digit (["_"] digit)*
//
// and returns it with ASCII digits and no underscores.
func pyDecimal(body string) (string, bool) {
	var out strings.Builder
	rs := []rune(body)
	i := 0
	digits := func() int {
		n := 0
		for i < len(rs) {
			if d, ok := digitValue(rs[i]); ok {
				out.WriteByte(byte('0' + d))
				n++
				i++
				continue
			}
			if rs[i] == '_' && n > 0 && i+1 < len(rs) {
				if _, ok := digitValue(rs[i+1]); ok {
					i++
					continue
				}
			}
			break
		}
		return n
	}
	intDigits := digits()
	fracDigits := 0
	if i < len(rs) && rs[i] == '.' {
		out.WriteByte('.')
		i++
		fracDigits = digits()
	}
	if intDigits == 0 && fracDigits == 0 {
		return "", false
	}
	if i < len(rs) && (rs[i] == 'e' || rs[i] == 'E') {
		out.WriteByte('e')
		i++
		if i < len(rs) && (rs[i] == '+' || rs[i] == '-') {
			out.WriteRune(rs[i])
			i++
		}
		if digits() == 0 {
			return "", false
		}
	}
	if i != len(rs) {
		return "", false
	}
	return out.String(), true
}

// digitValue is the value of a Unicode decimal digit (category Nd). Unicode
// encodes every Nd digit in contiguous runs of whole 0 to 9 blocks, so the
// value is the distance from the start of the run, modulo 10.
func digitValue(r rune) (int, bool) {
	if r >= '0' && r <= '9' {
		return int(r - '0'), true
	}
	if r < 0x80 || !unicode.IsDigit(r) {
		return 0, false
	}
	start := r
	for unicode.IsDigit(start - 1) {
		start--
	}
	return int(r-start) % 10, true
}
