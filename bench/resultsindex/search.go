package resultsindex

// Cross-run trace search (X23): a full-text index over every sealed run's
// steps, tool calls, trace events, model error messages, final answer (or
// final message), gate reasons and grade reason, so a
// reader can find "every run that called refund_order and said all tests
// pass" across experiments.
//
// The index is derived, like the results index: built in memory from the
// sealed run files through the server's read index, one document per run,
// cached by run id and seal hash (a sealed run never changes, so a cached
// document stays valid until its seal does). It is never read by a verdict,
// the leaderboard or a release decision.

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Search fields: where a match was found.
const (
	SearchFieldStep     = "step"
	SearchFieldToolCall = "tool_call"
	SearchFieldAnswer   = "answer"
	SearchFieldGate     = "gate"
	SearchFieldGrade    = "grade"
	SearchFieldEvent    = "event" // a trace event's summary or target (fix wave 2, N49)
	SearchFieldError    = "error" // a model error: a gateway row's error message or an error event
)

// Search limits.
const (
	DefaultSearchLimit = 50
	MaxSearchLimit     = 200
	maxQueryRunes      = 500
	maxQueryTerms      = 16
	maxFieldRunes      = 2000 // longest text kept per indexed passage
	maxPassages        = 4000 // passages kept per run
	maxMatchesPerHit   = 5
	snippetRunes       = 160
)

// ErrBadSearch marks a query that cannot be parsed.
var ErrBadSearch = errors.New("bad search query")

// SearchRun is one run the search may index: its identity and filter facts.
type SearchRun struct {
	RunID      string
	Experiment string
	Benchmark  string // suite id, without a version
	Task       string
	Agent      string
	Version    string
	Model      string
	Outcome    string
	Started    time.Time
	SealHash   string
}

// SearchSource lists sealed runs and reads their files.
type SearchSource interface {
	SearchRuns(ctx context.Context) ([]SearchRun, error)
	RunFile(runID, name string) ([]byte, error)
}

// SearchQuery is a parsed query: free words and phrases that must all
// appear in one run, tool names it must have called, and exact filters.
type SearchQuery struct {
	Raw       string
	Terms     []string // lowercased words and phrases
	Tools     []string // tool:NAME, lowercased
	Agent     string
	Version   string
	Benchmark string
	Outcome   string
	Limit     int
}

// SearchMatch is one passage of a run that matched.
type SearchMatch struct {
	Field      string   `json:"field"`
	Seq        int      `json:"seq,omitempty"`
	Label      string   `json:"label,omitempty"`
	Text       string   `json:"text"`
	Highlights [][2]int `json:"highlights"` // [start, end) rune offsets into Text
}

// SearchHit is one run that matched.
type SearchHit struct {
	RunID      string        `json:"run_id"`
	Experiment string        `json:"experiment"`
	Benchmark  string        `json:"benchmark"`
	Task       string        `json:"task"`
	Agent      string        `json:"agent"`
	Version    string        `json:"version,omitempty"`
	Model      string        `json:"model,omitempty"`
	Outcome    string        `json:"outcome"`
	Started    *time.Time    `json:"started,omitempty"`
	Score      int           `json:"score"`
	Matches    []SearchMatch `json:"matches"`
}

// SearchResult is GET /search.
type SearchResult struct {
	Query       string      `json:"query"`
	Total       int         `json:"total"`
	Truncated   bool        `json:"truncated"`
	IndexedRuns int         `json:"indexed_runs"`
	Hits        []SearchHit `json:"hits"`
}

// passage is one indexed piece of a run.
type passage struct {
	field, label, text, lower string
	seq                       int
}

// searchDoc is one run's indexed text.
type searchDoc struct {
	sealHash string
	passages []passage
	tools    map[string]bool // lowercased tool names the run called
}

// SearchIndex caches one document per sealed run. It is safe for
// concurrent use.
type SearchIndex struct {
	mu   sync.Mutex
	docs map[string]*searchDoc
}

// NewSearchIndex returns an empty index.
func NewSearchIndex() *SearchIndex { return &SearchIndex{docs: map[string]*searchDoc{}} }

// ParseSearchQuery parses q plus explicit filters (which win over the same
// filter written in q). Words are matched case-insensitively; "quoted
// phrases" must appear as written; tool:NAME requires a call of that tool;
// agent:, version:, benchmark: and outcome: filter exactly.
func ParseSearchQuery(q string, filters map[string]string, limit int) (SearchQuery, error) {
	out := SearchQuery{Raw: strings.TrimSpace(q), Limit: limit}
	if utf8.RuneCountInString(out.Raw) > maxQueryRunes {
		return out, fmt.Errorf("%w: longer than %d characters", ErrBadSearch, maxQueryRunes)
	}
	if out.Limit <= 0 {
		out.Limit = DefaultSearchLimit
	}
	if out.Limit > MaxSearchLimit {
		out.Limit = MaxSearchLimit
	}
	tokens, err := splitQuery(out.Raw)
	if err != nil {
		return out, err
	}
	set := map[string]*string{"agent": &out.Agent, "version": &out.Version, "benchmark": &out.Benchmark, "outcome": &out.Outcome}
	for _, tok := range tokens {
		if !tok.quoted {
			if k, v, ok := strings.Cut(tok.text, ":"); ok && v != "" {
				key := strings.ToLower(k)
				if key == "tool" {
					out.Tools = append(out.Tools, strings.ToLower(v))
					continue
				}
				if p, ok := set[key]; ok {
					*p = v
					continue
				}
			}
		}
		if t := strings.ToLower(strings.TrimSpace(tok.text)); t != "" {
			out.Terms = append(out.Terms, t)
		}
	}
	for k, v := range filters {
		if p, ok := set[k]; ok && strings.TrimSpace(v) != "" {
			*p = strings.TrimSpace(v)
		}
	}
	if len(out.Terms)+len(out.Tools) > maxQueryTerms {
		return out, fmt.Errorf("%w: more than %d terms", ErrBadSearch, maxQueryTerms)
	}
	return out, nil
}

// Empty reports whether the query has nothing to search or filter by.
func (q SearchQuery) Empty() bool {
	return len(q.Terms) == 0 && len(q.Tools) == 0 && q.Agent == "" && q.Version == "" && q.Benchmark == "" && q.Outcome == ""
}

// Search brings the cache up to date with src's sealed runs and returns the
// runs that match q, newest first, each with its best passages.
func (ix *SearchIndex) Search(ctx context.Context, src SearchSource, q SearchQuery) (*SearchResult, error) {
	runs, err := src.SearchRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("search: list runs: %w", err)
	}
	res := &SearchResult{Query: q.Raw, Hits: []SearchHit{}}
	if q.Empty() {
		return res, nil
	}
	runs = slices.Clone(runs) // the source's slice is not ours to reorder
	slices.SortStableFunc(runs, func(a, b SearchRun) int {
		if c := b.Started.Compare(a.Started); c != 0 {
			return c
		}
		return strings.Compare(a.RunID, b.RunID)
	})
	live := make(map[string]bool, len(runs))
	for _, r := range runs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		live[r.RunID] = true
		if !q.filters(r) {
			continue
		}
		doc, err := ix.doc(src, r)
		if err != nil {
			return nil, err
		}
		res.IndexedRuns++
		hit, ok := match(doc, q)
		if !ok {
			continue
		}
		res.Total++
		if len(res.Hits) >= q.Limit {
			res.Truncated = true
			continue
		}
		hit.RunID, hit.Experiment, hit.Benchmark, hit.Task = r.RunID, r.Experiment, r.Benchmark, r.Task
		hit.Agent, hit.Version, hit.Model, hit.Outcome = r.Agent, r.Version, r.Model, r.Outcome
		if !r.Started.IsZero() {
			t := r.Started
			hit.Started = &t
		}
		res.Hits = append(res.Hits, hit)
	}
	ix.prune(live)
	return res, nil
}

// filters applies the exact filters.
func (q SearchQuery) filters(r SearchRun) bool {
	eq := func(want, got string) bool { return want == "" || strings.EqualFold(want, got) }
	bench := q.Benchmark
	if i := strings.IndexByte(bench, '@'); i >= 0 {
		bench = bench[:i]
	}
	return eq(q.Agent, r.Agent) && eq(searchVersion(q.Version), searchVersion(r.Version)) && eq(bench, r.Benchmark) && eq(q.Outcome, r.Outcome)
}

// searchVersion is the comparable form of an agent version: "1", "v1" and
// "V1" are all saved version 1 and read "v1" (N291); any other value (a
// config's free-form agent_version) is kept as written.
func searchVersion(v string) string {
	v = strings.TrimSpace(v)
	n := strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")
	if n == "" || len(n) > 9 {
		return v
	}
	for _, c := range n {
		if c < '0' || c > '9' {
			return v
		}
	}
	if t := strings.TrimLeft(n, "0"); t != "" {
		n = t
	} else {
		n = "0"
	}
	return "v" + n
}

// doc returns the run's cached document, building it on first use or when
// its seal changed.
func (ix *SearchIndex) doc(src SearchSource, r SearchRun) (*searchDoc, error) {
	ix.mu.Lock()
	d, ok := ix.docs[r.RunID]
	ix.mu.Unlock()
	if ok && d.sealHash == r.SealHash {
		return d, nil
	}
	d, err := buildDoc(src, r)
	if err != nil {
		return nil, err
	}
	ix.mu.Lock()
	ix.docs[r.RunID] = d
	ix.mu.Unlock()
	return d, nil
}

// prune drops documents of runs that are gone.
func (ix *SearchIndex) prune(live map[string]bool) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for id := range ix.docs {
		if !live[id] {
			delete(ix.docs, id)
		}
	}
}

// searchEvidence is the part of evidence.json the search reads.
type searchEvidence struct {
	Steps     []schemas.StepRecord `json:"steps"`
	ToolCalls []schemas.ToolCall   `json:"tool_calls"`
	Answer    *schemas.Answer      `json:"answer"`
	Grade     *schemas.Grade       `json:"grade"`
	Gates     []schemas.GateResult `json:"gates"`
	// Events, the claim and the gateway rows carry what a run whose agent
	// runs its own tools records nowhere else: the command lines, the
	// model error bodies and the final message (N49).
	Events   []schemas.Event     `json:"events"`
	Claim    *schemas.Claim      `json:"claim"`
	Requests *schemas.RequestLog `json:"requests"`
}

// buildDoc reads one run's evidence.json, tools.jsonl, gates.json and
// grade.json. A missing file adds nothing; an unreadable one is an error.
func buildDoc(src SearchSource, r SearchRun) (*searchDoc, error) {
	var ev searchEvidence
	if err := readJSONFile(src, r.RunID, "evidence.json", &ev); err != nil {
		return nil, err
	}
	if data, err := src.RunFile(r.RunID, "tools.jsonl"); err == nil {
		var calls []schemas.ToolCall
		for _, line := range strings.Split(string(data), "\n") {
			var c schemas.ToolCall
			if strings.TrimSpace(line) != "" && json.Unmarshal([]byte(line), &c) == nil {
				calls = append(calls, c)
			}
		}
		if len(calls) > 0 {
			ev.ToolCalls = calls
		}
	} else if !notFound(err) {
		return nil, fmt.Errorf("search: run %s tools.jsonl: %w", r.RunID, err)
	}
	var gates []schemas.GateResult
	if err := readJSONFile(src, r.RunID, "gates.json", &gates); err != nil {
		return nil, err
	}
	if gates != nil {
		ev.Gates = gates
	}
	var grade schemas.Grade
	if err := readJSONFile(src, r.RunID, "grade.json", &grade); err != nil {
		return nil, err
	}
	if grade.Reason != "" || len(grade.FailedTests) > 0 {
		ev.Grade = &grade
	}
	return docFrom(r.SealHash, &ev), nil
}

// docFrom builds a document from the run's evidence. It is pure.
func docFrom(sealHash string, ev *searchEvidence) *searchDoc {
	d := &searchDoc{sealHash: sealHash, tools: map[string]bool{}}
	seen := map[string]bool{} // lowered passage texts: the same line from a step and an event counts once
	add := func(field, label, text string, seq int) {
		text = clip(strings.TrimSpace(text), maxFieldRunes)
		if text == "" || len(d.passages) >= maxPassages {
			return
		}
		lower := strings.ToLower(text)
		if seen[lower] {
			return
		}
		seen[lower] = true
		d.passages = append(d.passages, passage{field: field, label: label, text: text, lower: lower, seq: seq})
	}
	for _, s := range ev.Steps {
		if s.ToolName != "" {
			d.tools[strings.ToLower(s.ToolName)] = true
		}
		if s.WitnessOf != 0 {
			continue // a second witness of a counted row says the same thing
		}
		add(SearchFieldStep, cmp.Or(s.ToolName, s.Kind), cmp.Or(s.Summary, s.ToolName), s.Seq)
	}
	for _, c := range ev.ToolCalls {
		if !c.IsToolCall() {
			continue
		}
		d.tools[strings.ToLower(c.Tool)] = true
		text := c.Tool
		if c.Arguments != "" {
			text += " " + c.Arguments
		}
		if c.Result != "" {
			text += " -> " + c.Result
		}
		add(SearchFieldToolCall, c.Tool, text, c.Seq)
	}
	for _, e := range ev.Events {
		if e.ToolName != "" {
			d.tools[strings.ToLower(e.ToolName)] = true
		}
		text := strings.TrimSpace(e.Summary)
		if e.Target != "" && !strings.Contains(text, e.Target) {
			text += " " + e.Target
		}
		// Event seqs are per source and do not match step numbers: no seq.
		add(eventField(e), cmp.Or(e.ToolName, string(e.Type)), text, 0)
	}
	if ev.Requests != nil {
		for _, r := range ev.Requests.Requests {
			if r.ErrorMessage != "" {
				add(SearchFieldError, cmp.Or(r.ModelServed, r.ModelRequested), r.ErrorMessage, 0)
			}
		}
	}
	if ev.Answer != nil && strings.TrimSpace(ev.Answer.Text) != "" {
		add(SearchFieldAnswer, "", ev.Answer.Text, 0)
	} else if ev.Claim != nil {
		add(SearchFieldAnswer, "", ev.Claim.Text, 0) // the final message when the run has no answer
	}
	for _, g := range ev.Gates {
		if g.Fired {
			add(SearchFieldGate, g.Gate, g.Gate+": "+g.Detail, 0)
		}
	}
	if g := ev.Grade; g != nil {
		text := g.Reason
		if len(g.FailedTests) > 0 {
			text += " failed: " + strings.Join(g.FailedTests, ", ")
		}
		add(SearchFieldGrade, "", text, 0)
	}
	return d
}

// eventField is the search field of one trace event: error for an error,
// tool_call for a tool call or result, else event.
func eventField(e schemas.Event) string {
	switch {
	case e.IsError || e.Type == schemas.EventError || e.Type == schemas.EventRetry || strings.HasPrefix(e.Summary, "error "):
		return SearchFieldError
	case e.Type == schemas.EventToolCall || e.Type == schemas.EventToolResult:
		return SearchFieldToolCall
	}
	return SearchFieldEvent
}

// match checks q's terms and tools against a document: every term must
// appear in some passage, every tool must have been called. The hit keeps
// the passages with the most terms, in document order.
func match(d *searchDoc, q SearchQuery) (SearchHit, bool) {
	var hit SearchHit
	for _, t := range q.Tools {
		if !d.tools[t] {
			return hit, false
		}
	}
	found := make([]bool, len(q.Terms))
	type scored struct {
		i, n int
	}
	var cands []scored
	// A plain word that is the name of a tool the run called matches like
	// tool: does (N50): a witness row may be the only one that names it.
	for k, t := range q.Terms {
		if d.tools[t] {
			found[k] = true
		}
	}
	for i, p := range d.passages {
		n := 0
		label := strings.ToLower(p.label)
		for k, t := range q.Terms {
			if strings.Contains(p.lower, t) || (label == t && d.tools[t]) {
				found[k] = true
				n++
			}
		}
		toolHit := len(q.Terms) == 0 && len(q.Tools) > 0 && slices.Contains(q.Tools, strings.ToLower(p.label))
		if n > 0 || toolHit {
			cands = append(cands, scored{i, n})
		}
	}
	for _, f := range found {
		if !f {
			return hit, false
		}
	}
	hit.Score = len(cands)
	slices.SortStableFunc(cands, func(a, b scored) int { return b.n - a.n })
	if len(cands) > maxMatchesPerHit {
		cands = cands[:maxMatchesPerHit]
	}
	slices.SortFunc(cands, func(a, b scored) int { return a.i - b.i })
	hit.Matches = make([]SearchMatch, 0, len(cands))
	for _, c := range cands {
		p := d.passages[c.i]
		terms := q.Terms
		if len(terms) == 0 {
			terms = q.Tools
		}
		text, hl := snippet(p.text, terms)
		hit.Matches = append(hit.Matches, SearchMatch{Field: p.field, Seq: p.seq, Label: p.label, Text: text, Highlights: hl})
	}
	return hit, true
}

// snippet cuts text to a window around the first term and returns the rune
// ranges of every term inside it. It is pure.
func snippet(text string, terms []string) (string, [][2]int) {
	runes := []rune(text)
	lower := []rune(strings.ToLower(text))
	if len(lower) != len(runes) { // a case change altered the length: match on the text as written
		lower = runes
	}
	first := -1
	for _, t := range terms {
		if i := indexRunes(lower, []rune(t), 0); i >= 0 && (first < 0 || i < first) {
			first = i
		}
	}
	start := 0
	if first > snippetRunes/3 {
		start = first - snippetRunes/3
	}
	end := min(len(runes), start+snippetRunes)
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "..."
	}
	if end < len(runes) {
		suffix = "..."
	}
	var hl [][2]int
	off := len([]rune(prefix))
	for _, t := range terms {
		tr := []rune(t)
		if len(tr) == 0 {
			continue
		}
		for i := indexRunes(lower, tr, start); i >= 0 && i+len(tr) <= end; i = indexRunes(lower, tr, i+len(tr)) {
			hl = append(hl, [2]int{i - start + off, i - start + off + len(tr)})
		}
	}
	slices.SortFunc(hl, func(a, b [2]int) int { return a[0] - b[0] })
	return prefix + string(runes[start:end]) + suffix, mergeRanges(hl)
}

// indexRunes finds sub in s at or after from, or -1.
func indexRunes(s, sub []rune, from int) int {
	for i := from; i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// mergeRanges joins overlapping sorted ranges.
func mergeRanges(in [][2]int) [][2]int {
	out := [][2]int{}
	for _, r := range in {
		if n := len(out); n > 0 && r[0] <= out[n-1][1] {
			out[n-1][1] = max(out[n-1][1], r[1])
			continue
		}
		out = append(out, r)
	}
	return out
}

type queryToken struct {
	text   string
	quoted bool
}

// splitQuery splits on spaces, keeping "quoted phrases" whole.
func splitQuery(q string) ([]queryToken, error) {
	var out []queryToken
	var cur strings.Builder
	inQuote := false
	flush := func(quoted bool) {
		if cur.Len() > 0 || quoted {
			if s := cur.String(); strings.TrimSpace(s) != "" {
				out = append(out, queryToken{text: s, quoted: quoted})
			}
		}
		cur.Reset()
	}
	for _, r := range q {
		switch {
		case r == '"':
			flush(inQuote)
			inQuote = !inQuote
		case unicode.IsSpace(r) && !inQuote:
			flush(false)
		default:
			cur.WriteRune(r)
		}
	}
	if inQuote {
		return nil, fmt.Errorf("%w: a quote is not closed", ErrBadSearch)
	}
	flush(false)
	return out, nil
}

// clip shortens s to n runes.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// readJSONFile decodes one run file into dst; a missing file leaves dst.
func readJSONFile(src SearchSource, runID, name string, dst any) error {
	data, err := src.RunFile(runID, name)
	if notFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("search: run %s %s: %w", runID, name, err)
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return nil // an unreadable file adds nothing to search; the seal check is the run page's job
	}
	return nil
}

func notFound(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound)
}
