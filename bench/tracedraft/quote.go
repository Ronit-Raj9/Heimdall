package tracedraft

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/maximhq/bifrost/bench/traceimport"
)

// Quote is one passage the model cites. It has the same JSON as
// investigator.Quote: RunID is the inbox item id, Seq the tool call's seq,
// or 0 for the turns and the final answer.
type Quote struct {
	RunID string `json:"run_id"`
	Seq   int    `json:"seq"`
	Text  string `json:"text"` // 8 to 240 characters after Normalize
}

// QuoteResult is the check of one quote.
type QuoteResult struct {
	Quote   Quote  `json:"quote"`
	OK      bool   `json:"ok"`
	Closest string `json:"closest,omitempty"`
	Reason  string `json:"reason,omitempty"` // "no such step" | "too short" | "too long" | "not found in the step"
}

// Quote length bounds, after Normalize.
const (
	minQuote = 8
	maxQuote = 240
)

var (
	spaceRun   = regexp.MustCompile(`\s+`)
	quoteFolds = strings.NewReplacer(
		"“", `"`, "”", `"`, "„", `"`, "″", `"`,
		"‘", "'", "’", "'", "‚", "'", "′", "'",
		`\n`, "\n", `\t`, "\t", `\"`, `"`,
		"*", "", "_", "", "`", "",
	)
)

// Normalize folds what a model changes when it copies text: Unicode NFKC,
// curly quotes to straight, backslash escapes \n \t \" to their characters,
// markdown emphasis (*, _, `) removed, every run of whitespace to one space,
// a leading or trailing "..." or the ellipsis character removed, then
// trimmed. Case is kept. This is this package's own copy of the rules of
// the investigator's quote check.
func Normalize(s string) string {
	s = norm.NFKC.String(s)
	s = quoteFolds.Replace(s)
	s = spaceRun.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)
	for _, cut := range []string{"...", "…"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, cut))
		s = strings.TrimSpace(strings.TrimSuffix(s, cut))
	}
	return s
}

// CheckQuotes checks each quote against source(runID, seq), which returns
// the text of that step and whether it exists. A quote passes when its
// normalized text is inside the normalized step text.
func CheckQuotes(qs []Quote, source func(runID string, seq int) (string, bool)) []QuoteResult {
	out := make([]QuoteResult, 0, len(qs))
	for _, q := range qs {
		r := QuoteResult{Quote: q}
		text := Normalize(q.Text)
		step, ok := source(q.RunID, q.Seq)
		switch {
		case !ok:
			r.Reason = "no such step"
		case len([]rune(text)) < minQuote:
			r.Reason = "too short"
		case len([]rune(text)) > maxQuote:
			r.Reason = "too long"
		case strings.Contains(Normalize(step), text):
			r.OK = true
		default:
			r.Reason = "not found in the step"
			r.Closest = closest(Normalize(step), text)
		}
		out = append(out, r)
	}
	return out
}

// closest is the passage of step, as long as the quote (at most 240
// characters), that shares the most words with it.
func closest(step, quote string) string {
	words := strings.Fields(step)
	n := len(strings.Fields(quote))
	if n == 0 || len(words) == 0 {
		return ""
	}
	want := map[string]bool{}
	for _, w := range strings.Fields(quote) {
		want[w] = true
	}
	best, bestAt := -1, 0
	for i := 0; i+n <= len(words) || i == 0; i++ {
		score := 0
		for _, w := range words[i:min(i+n, len(words))] {
			if want[w] {
				score++
			}
		}
		if score > best {
			best, bestAt = score, i
		}
		if i+n >= len(words) {
			break
		}
	}
	out := strings.Join(words[bestAt:min(bestAt+n, len(words))], " ")
	if r := []rune(out); len(r) > maxQuote {
		out = string(r[:maxQuote])
	}
	return out
}

// SessionSource is the quote source of one (redacted) session: seq 0 is
// every turn and the final answer, seq n the tool call n (its name, its
// arguments, its result and its error).
func SessionSource(item string, s *traceimport.Session) func(runID string, seq int) (string, bool) {
	return func(runID string, seq int) (string, bool) {
		if runID != item || s == nil {
			return "", false
		}
		if seq == 0 {
			var parts []string
			for _, t := range s.Turns {
				parts = append(parts, t.Text)
			}
			parts = append(parts, s.FinalAnswer)
			return strings.Join(parts, "\n"), true
		}
		for _, c := range s.ToolCalls {
			if c.Seq == seq {
				return fmt.Sprintf("%s\n%s\n%s\n%s", c.Name, c.Arguments, c.Result, c.Error), true
			}
		}
		return "", false
	}
}
