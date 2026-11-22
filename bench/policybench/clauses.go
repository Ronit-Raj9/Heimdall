package policybench

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Clause is one numbered rule of a policy.
type Clause struct {
	Number  string `json:"number"`            // "4", "2.3"
	Title   string `json:"title,omitempty"`   // the heading or first words
	Section string `json:"section,omitempty"` // the heading above it
	Text    string `json:"text"`
	// Skip leaves the clause out of generation (a person unticked it, or
	// it has no rule to test, such as a definition).
	Skip bool `json:"skip,omitempty"`
}

// Limits of a split.
const (
	MaxClauses      = 200
	MaxClauseText   = 4000
	minClauseLetter = 12
)

var (
	mdHeadingRe = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*$`)
	// "Section 4.", "Clause 2.1:", "Article 3 -", "§ 5"
	namedNumRe = regexp.MustCompile(`(?i)^(?:#{1,6}\s+)?(?:section|clause|article|rule|policy|§)\s*(\d+(?:\.\d+)*)\s*[.:)\-]?\s*(.*)$`)
	// "4.", "4)", "2.3", "2.3.", "2.3)" followed by text; plain "2024." years are not numbers.
	plainNumRe = regexp.MustCompile(`^(?:#{1,6}\s+)?(\d{1,3}(?:\.\d{1,3})*)(?:[.)]|\.?\s)\s*(.*)$`)
	bulletRe   = regexp.MustCompile(`^\s*(?:[-*•]|\(?[a-z]\)|\(?[ivx]{1,4}\))\s+(.+)$`)
)

// SplitClauses splits a policy's text into numbered clauses. Numbered
// rules ("4.", "2.3", "Section 4", "Clause 2.1") keep their numbers; a
// number whose own text is only a heading becomes the section of the
// numbers under it. Without numbers, each Markdown heading's list items
// (or its paragraphs) become clauses N.M under heading N; without headings
// either, each paragraph or list item is a clause, numbered from 1.
func SplitClauses(text string) ([]Clause, error) {
	lines := strings.Split(normalise(text), "\n")
	var out []Clause
	switch {
	case countNumbered(lines) >= 2:
		out = splitNumbered(lines)
	case countHeadings(lines) >= 1:
		out = splitHeadings(lines)
	default:
		out = splitParagraphs(lines, "")
		for i := range out {
			out[i].Number = strconv.Itoa(i + 1)
		}
	}
	var kept []Clause
	for _, c := range out {
		c.Text = strings.TrimSpace(c.Text)
		if letters(c.Text) < minClauseLetter {
			continue
		}
		if len(c.Text) > MaxClauseText {
			c.Text = strings.TrimSpace(c.Text[:MaxClauseText]) + " ..."
		}
		if c.Title == "" {
			c.Title = firstWords(c.Text, 8)
		}
		kept = append(kept, c)
	}
	if len(kept) == 0 {
		return nil, fmt.Errorf("no clause found: the text has no numbered rules, headings or paragraphs of at least %d letters", minClauseLetter)
	}
	if len(kept) > MaxClauses {
		return nil, fmt.Errorf("%d clauses; a benchmark takes at most %d (split the document)", len(kept), MaxClauses)
	}
	seen := map[string]int{}
	for i := range kept {
		n := kept[i].Number
		if seen[n] > 0 {
			kept[i].Number = fmt.Sprintf("%s-%d", n, seen[n]+1)
		}
		seen[n]++
	}
	return kept, nil
}

func letters(s string) int {
	n := 0
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 0x7f {
			n++
		}
	}
	return n
}

func firstWords(s string, n int) string {
	f := strings.Fields(s)
	if len(f) > n {
		return strings.Join(f[:n], " ") + " ..."
	}
	return strings.Join(f, " ")
}

func numberOf(line string) (num, rest string, ok bool) {
	l := strings.TrimSpace(line)
	if m := namedNumRe.FindStringSubmatch(l); m != nil {
		return m[1], strings.TrimSpace(m[2]), true
	}
	if m := plainNumRe.FindStringSubmatch(l); m != nil {
		if strings.TrimSpace(m[2]) == "" && !strings.ContainsAny(l, ".)") {
			return "", "", false
		}
		// A sentence that starts with an amount ("30 days ...") is not a number.
		if !strings.Contains(m[1], ".") && !strings.HasPrefix(l[len(m[1]):], ".") && !strings.HasPrefix(l[len(m[1]):], ")") &&
			!strings.HasPrefix(l, "#") {
			return "", "", false
		}
		return m[1], strings.TrimSpace(m[2]), true
	}
	return "", "", false
}

func countNumbered(lines []string) int {
	n := 0
	for _, l := range lines {
		if _, _, ok := numberOf(l); ok {
			n++
		}
	}
	return n
}

func countHeadings(lines []string) int {
	n := 0
	for _, l := range lines {
		if mdHeadingRe.MatchString(strings.TrimSpace(l)) {
			n++
		}
	}
	return n
}

func splitNumbered(lines []string) []Clause {
	var raw []Clause
	section := ""
	var cur *Clause
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if num, rest, ok := numberOf(t); ok {
			raw = append(raw, Clause{Number: num, Section: section, Text: rest})
			cur = &raw[len(raw)-1]
			continue
		}
		if m := mdHeadingRe.FindStringSubmatch(t); m != nil {
			section, cur = m[2], nil
			continue
		}
		if cur != nil && t != "" {
			if cur.Text != "" {
				cur.Text += "\n"
			}
			cur.Text += t
		}
	}
	// A number with children ("2" before "2.1") whose text is only a short
	// heading becomes their section.
	var out []Clause
	for i, c := range raw {
		hasChild := i+1 < len(raw) && strings.HasPrefix(raw[i+1].Number, c.Number+".")
		if hasChild && !strings.Contains(c.Text, "\n") && (len(strings.Fields(c.Text)) <= 8 || !strings.ContainsAny(c.Text, ".:;")) {
			for j := i + 1; j < len(raw) && strings.HasPrefix(raw[j].Number, c.Number+"."); j++ {
				raw[j].Section = strings.TrimSpace(c.Number + " " + c.Text)
			}
			continue
		}
		if first, rest, ok := strings.Cut(c.Text, "\n"); ok && len(strings.Fields(first)) <= 8 && !strings.HasSuffix(first, ".") {
			c.Title = first
			c.Text = rest
		}
		out = append(out, c)
	}
	return out
}

func splitHeadings(lines []string) []Clause {
	var out []Clause
	type sec struct {
		title string
		body  []string
	}
	var secs []sec
	var pre []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if m := mdHeadingRe.FindStringSubmatch(t); m != nil {
			secs = append(secs, sec{title: m[2]})
			continue
		}
		if len(secs) == 0 {
			pre = append(pre, l)
			continue
		}
		secs[len(secs)-1].body = append(secs[len(secs)-1].body, l)
	}
	_ = pre // the text before the first heading is the preamble, not a rule
	n := 0
	for _, s := range secs {
		parts := splitParagraphs(s.body, s.title)
		if len(parts) == 0 {
			continue
		}
		n++
		if len(parts) == 1 {
			parts[0].Number, parts[0].Title, parts[0].Section = strconv.Itoa(n), s.title, ""
			out = append(out, parts[0])
			continue
		}
		for i := range parts {
			parts[i].Number = fmt.Sprintf("%d.%d", n, i+1)
		}
		out = append(out, parts...)
	}
	return out
}

// splitParagraphs makes one clause per list item or paragraph.
func splitParagraphs(lines []string, section string) []Clause {
	var out []Clause
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, Clause{Section: section, Text: strings.Join(para, " ")})
			para = nil
		}
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == "":
			flush()
		case bulletRe.MatchString(t):
			flush()
			para = []string{bulletRe.FindStringSubmatch(t)[1]}
		default:
			para = append(para, t)
		}
	}
	flush()
	return out
}

// ClauseTag is the task tag of a clause number ("clause-2-3").
func ClauseTag(number string) string {
	return "clause-" + strings.Trim(slugRe.ReplaceAllString(strings.ToLower(number), "-"), "-")
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string, n int) string {
	out := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(out) > n {
		out = strings.Trim(out[:n], "-")
	}
	return out
}
