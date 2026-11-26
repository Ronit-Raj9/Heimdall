package investigator

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Quote is one cited passage.
type Quote struct {
	RunID string `json:"run_id"`
	Seq   int    `json:"seq"`  // StepRecord.Seq; 0 means the run's final answer or the session text
	Text  string `json:"text"` // 8 to 240 characters after Normalize
}

// QuoteResult is the check of one quote.
type QuoteResult struct {
	Quote   Quote  `json:"quote"`
	OK      bool   `json:"ok"`
	Closest string `json:"closest,omitempty"` // when not OK: the closest passage of that step, at most 240 chars
	Reason  string `json:"reason,omitempty"`  // "no such step" | "too short" | "too long" | "not found in the step"
}

const (
	quoteMin = 8
	quoteMax = 240
)

var quoteFold = strings.NewReplacer(
	"“", `"`, "”", `"`, "„", `"`, "‟", `"`, "«", `"`, "»", `"`,
	"‘", "'", "’", "'", "‚", "'", "‛", "'",
	`\n`, "\n", `\t`, "\t", `\"`, `"`,
	"*", "", "_", "", "`", "",
)

// Normalize folds what a model changes when it copies text: Unicode NFKC,
// curly quotes to straight, every run of whitespace to one space, backslash
// escapes \n \t \" to their characters, markdown emphasis (*, _, `) removed,
// a leading or trailing "..." or "…" removed, then trimmed. Case is kept.
func Normalize(s string) string {
	s = norm.NFKC.String(s)
	s = quoteFold.Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	for _, e := range []string{"...", "…"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, e))
		s = strings.TrimSpace(strings.TrimSuffix(s, e))
	}
	return strings.TrimSpace(s)
}

// CheckQuotes checks each quote against source(runID, seq), which returns the
// step's text (StepRecord.Summary plus, for tool rows, the redacted result
// text the run kept) and whether the step exists.
func CheckQuotes(qs []Quote, source func(runID string, seq int) (string, bool)) []QuoteResult {
	out := make([]QuoteResult, 0, len(qs))
	for _, q := range qs {
		res := QuoteResult{Quote: q}
		text, ok := source(q.RunID, q.Seq)
		nq := Normalize(q.Text)
		switch {
		case !ok:
			res.Reason = "no such step"
		case len([]rune(nq)) < quoteMin:
			res.Reason = "too short"
		case len([]rune(nq)) > quoteMax:
			res.Reason = "too long"
		default:
			ns := Normalize(text)
			if strings.Contains(ns, nq) {
				res.OK = true
			} else {
				res.Reason = "not found in the step"
				res.Closest = closest(ns, nq)
			}
		}
		out = append(out, res)
	}
	return out
}

// closest is the window of the step's words, as long as the quote, that
// shares the most words with it in place, cut at 240 characters.
func closest(step, quote string) string {
	sw, qw := strings.Fields(step), strings.Fields(quote)
	if len(sw) == 0 || len(qw) == 0 {
		return ""
	}
	n := min(len(qw), len(sw))
	best, bestAt := -1, 0
	for i := 0; i+n <= len(sw); i++ {
		score := 0
		for j := 0; j < n; j++ {
			if sw[i+j] == qw[j] {
				score++
			}
		}
		if score > best {
			best, bestAt = score, i
		}
	}
	s := strings.Join(sw[bestAt:bestAt+n], " ")
	if r := []rune(s); len(r) > quoteMax {
		s = string(r[:quoteMax])
	}
	return s
}
