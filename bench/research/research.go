// Package research holds what the research track shares between the bench
// and its tools (docs/research-tasks.md): the canonical Tavily request
// bodies the websearch tool sends (so a replayed cassette matches them byte
// for byte), how a run's cited sources are read, which URLs a run's search
// and fetch calls returned according to the tool proxy's ledger, and the
// hidden research spec of a task.
//
// It reads files and ledger rows only. The Tavily stand-in is in
// research/standin and the cassette recorder in research/recording.
package research

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Names the research track uses.
const (
	// APIID is the id of the Tavily external API in research suites. Its
	// agent URL is http://toolproxy:9200/x/tavily.
	APIID = "tavily"
	// TavilyHost is the host of Tavily's API (verified 2026-11-18 from
	// docs.tavily.com: POST https://api.tavily.com/search and /extract).
	TavilyHost = "api.tavily.com"
	// SearchTool and ExtractTool are the ledger tool names of the two
	// allowed calls (method and path, as the tool proxy records them).
	SearchTool  = "POST /search"
	ExtractTool = "POST /extract"
	// CorpusService is the id of a fixed-corpus search service that answers
	// in Tavily's shape (importers/browsecompplus writes it); its calls reach
	// the ledger as http rows of that service.
	CorpusService = "corpus"
	// SearchResults is how many results the websearch tool always asks
	// Tavily for; it cuts the list to what the agent asked for itself, so
	// one recorded call answers every max_results.
	SearchResults = 8
	// MaxQueryRunes caps a normalized query.
	MaxQueryRunes = 400
	// AnswerArtifact and SourcesArtifact are the artifact ids of a research
	// task (output kind artifact): answer.txt and sources.json.
	AnswerArtifact  = "answer"
	SourcesArtifact = "sources"
	// AnswerFile and SourcesFile are their file names.
	AnswerFile  = "answer.txt"
	SourcesFile = "sources.json"
	// SpecFile is the hidden research spec in a task's verifier tests
	// folder.
	SpecFile = "research.json"
	// CorpusURLsFile lists every URL of a fixed corpus, beside SpecFile. The
	// verifier treats a cited URL outside it as invented.
	CorpusURLsFile = "corpus_urls.json"
)

// queryTrim are the characters trimmed from both ends of a query.
const queryTrim = " ?!.,;:"

// NormalizeQuery is the query the websearch tool sends: control characters
// and line separators become spaces, runs of white space become one space,
// letters are lower-cased as Python's str.lower does (pythonLower), " ?!.,;:" are trimmed from both ends, and the
// result is cut to MaxQueryRunes. websearch/server.py does the same; a test
// runs both. It is pure.
func NormalizeQuery(q string) string {
	q = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == ' ' || r == ' ' {
			return ' '
		}
		return r
	}, q)
	q = pythonLower(strings.Join(strings.Fields(q), " "))
	q = strings.Trim(q, queryTrim)
	if utf8.RuneCountInString(q) > MaxQueryRunes {
		q = string([]rune(q)[:MaxQueryRunes])
		q = strings.TrimRight(q, queryTrim)
	}
	return q
}

// pythonLower lower-cases s exactly as Python's str.lower does, which the
// websearch tool uses: the simple lowercase mapping of each code point,
// except U+0130 (capital I with dot above), whose full mapping is "i" plus
// U+0307, and capital sigma, which becomes final sigma at the end of a word
// (the Final_Sigma rule of Unicode section 3.13). Go's strings.ToLower maps
// both differently, and replay keys on the body's exact bytes.
func pythonLower(s string) string {
	rs := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range rs {
		switch r {
		case 0x130:
			b.WriteString("i\u0307")
		case 0x3a3:
			if finalSigma(rs, i) {
				b.WriteRune(0x3c2)
			} else {
				b.WriteRune(0x3c3)
			}
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// finalSigma reports whether the capital sigma at rs[i] ends a word: a cased
// letter comes before it and none after it, skipping case-ignorable runes,
// as CPython's handle_capital_sigma decides.
func finalSigma(rs []rune, i int) bool {
	j := i - 1
	for j >= 0 && caseIgnorable(rs[j]) {
		j--
	}
	if j < 0 || !cased(rs[j]) {
		return false
	}
	j = i + 1
	for j < len(rs) && caseIgnorable(rs[j]) {
		j++
	}
	return j == len(rs) || !cased(rs[j])
}

// cased is the Unicode Cased property.
func cased(r rune) bool {
	return unicode.In(r, unicode.Lu, unicode.Ll, unicode.Lt, unicode.Other_Lowercase, unicode.Other_Uppercase)
}

// caseIgnorable is the Unicode Case_Ignorable property: the marks, format
// characters, modifier letters and symbols, and the Word_Break MidLetter,
// MidNumLet and Single_Quote characters.
func caseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', 0xb7, 0x387, 0x55f, 0x5f4, 0x2018, 0x2019, 0x2024, 0x2027, 0xfe13, 0xfe52, 0xfe55, 0xff07, 0xff0e, 0xff1a:
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// SearchBody is the exact body of the websearch tool's Tavily search call
// for a query: keys sorted, no spaces, HTML characters unescaped, as
// Python's json.dumps(sort_keys=True, separators=(",", ":"),
// ensure_ascii=False) writes it. The query is normalized first. Replay
// matches the body's sha256, so the tool and the recorder must agree on
// every byte.
func SearchBody(query string) []byte {
	var b bytes.Buffer
	b.WriteString(`{"include_published_date":true,"max_results":`)
	fmt.Fprintf(&b, "%d", SearchResults)
	b.WriteString(`,"query":`)
	b.Write(jsonString(NormalizeQuery(query)))
	b.WriteString(`}`)
	return b.Bytes()
}

// ExtractBody is the exact body of the websearch tool's Tavily extract call
// for one URL (trimmed).
func ExtractBody(rawURL string) []byte {
	var b bytes.Buffer
	b.WriteString(`{"include_published_date":true,"urls":[`)
	b.Write(jsonString(strings.TrimSpace(rawURL)))
	b.WriteString(`]}`)
	return b.Bytes()
}

// jsonString encodes s as a JSON string without HTML escaping.
func jsonString(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // a string always encodes
	return bytes.TrimRight(b.Bytes(), "\n")
}

// NormalizeURL is the form cited and returned URLs are compared in: spaces,
// angle brackets, quotes and trailing punctuation trimmed, the fragment
// dropped, scheme and host lower-cased, and a trailing slash dropped from a
// non-root path. A string that is not an http(s) URL is returned trimmed.
// It is pure.
func NormalizeURL(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Trim(s, "<>\"'`")
	s = strings.TrimRight(s, ".,;:)]}>\"'")
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return s
	}
	u.Host, u.Fragment, u.RawFragment = strings.ToLower(u.Host), "", ""
	if len(u.Path) > 1 {
		u.Path = strings.TrimRight(u.Path, "/")
		u.RawPath = ""
	}
	return u.String()
}

// Citations are the sources a run's answer cites.
type Citations struct {
	URLs   []string // normalized, distinct, in the order cited
	Source string   // where they were read: sources.json, the answer's Sources section, or "" when neither exists
	// Problem says why a sources file could not be used (it does not
	// parse); the run then cites nothing.
	Problem string
}

// sourcesHeading starts the fixed Sources section of an answer: a line that
// starts "Sources:" (or "Source:", "## Sources", "**Sources:**"), or is
// "Sources" alone. verify.py uses the same pattern.
var sourcesHeading = regexp.MustCompile(`(?im)^[ \t]*(?:#+[ \t]*)?(?:\*\*)?sources?(?:\*\*)?[ \t]*(?::|$)`)

// urlRE finds http(s) URLs in free text.
var urlRE = regexp.MustCompile(`https?://[^\s<>"'\x60\]\)]+`)

// SplitAnswer splits an answer into its body and its Sources section (empty
// when it has none). The body is what is graded as the answer. It is pure.
func SplitAnswer(answer string) (body, sources string) {
	loc := sourcesHeading.FindStringIndex(answer)
	if loc == nil {
		return strings.TrimSpace(answer), ""
	}
	return strings.TrimSpace(answer[:loc[0]]), answer[loc[0]:]
}

// ParseSourcesJSON reads a sources.json: a list of URLs, a list of objects
// with "url", or an object whose "sources" is either. It is pure.
func ParseSourcesJSON(b []byte) ([]string, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("%s does not parse: %w", SourcesFile, err)
	}
	if m, ok := v.(map[string]any); ok {
		v = m["sources"]
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not a list of URLs or an object with a sources list", SourcesFile)
	}
	var out []string
	for _, item := range list {
		switch x := item.(type) {
		case string:
			out = append(out, x)
		case map[string]any:
			if s, ok := x["url"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// distinctURLs normalizes and de-duplicates urls, keeping their order and
// dropping empty ones.
func distinctURLs(urls []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, u := range urls {
		n := NormalizeURL(u)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// ReadCitations reads the sources a run cites from its run folder: the
// sources artifact (artifacts/sources/sources.json) when it exists, else the
// Sources section of the answer artifact (artifacts/answer/answer.txt), else
// that of answer.txt (an answer task). A sources file that does not parse
// cites nothing and says why in Problem. Only a folder that cannot be read
// is an error.
func ReadCitations(runDir string) (Citations, error) {
	if runDir == "" {
		return Citations{}, errors.New("no run folder")
	}
	src := filepath.Join(runDir, "artifacts", SourcesArtifact, SourcesFile)
	b, err := os.ReadFile(src)
	switch {
	case err == nil:
		urls, perr := ParseSourcesJSON(b)
		if perr != nil {
			return Citations{URLs: []string{}, Source: SourcesFile, Problem: perr.Error()}, nil
		}
		return Citations{URLs: distinctURLs(urls), Source: SourcesFile}, nil
	case !errors.Is(err, fs.ErrNotExist):
		return Citations{}, fmt.Errorf("read %s: %w", src, err)
	}
	for _, p := range []string{filepath.Join(runDir, "artifacts", AnswerArtifact, AnswerFile), filepath.Join(runDir, AnswerFile)} {
		b, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Citations{}, fmt.Errorf("read %s: %w", p, err)
		}
		_, section := SplitAnswer(string(b))
		if section == "" {
			return Citations{URLs: []string{}}, nil
		}
		return Citations{URLs: distinctURLs(urlRE.FindAllString(section, -1)), Source: "answer Sources section"}, nil
	}
	return Citations{URLs: []string{}}, nil
}

// TavilyAPIs returns the ids of the run's external APIs that are Tavily:
// the id APIID, or a base URL on TavilyHost.
func TavilyAPIs(s *schemas.Suite, t *schemas.Task) map[string]bool {
	out := map[string]bool{}
	for _, ref := range utils.ExternalAPIs(s, t) {
		u, err := url.Parse(ref.API.BaseURL)
		if ref.API.ID == APIID || (err == nil && strings.EqualFold(u.Hostname(), TavilyHost)) {
			out[ref.API.ID] = true
		}
	}
	if hasCorpusService(s, t) {
		out[CorpusService] = true
	}
	return out
}

// hasCorpusService reports whether the suite or the task starts the
// fixed-corpus search service as a tool server.
func hasCorpusService(s *schemas.Suite, t *schemas.Task) bool {
	var specs []schemas.ServiceSpec
	if s != nil {
		specs = append(specs, s.Services...)
	}
	if t != nil {
		specs = append(specs, t.Services...)
	}
	for _, sp := range specs {
		if sp.ID == CorpusService && sp.Role == schemas.ServiceRoleTool {
			return true
		}
	}
	return false
}

// searchRow reports whether c is a call to one of the research APIs: an
// external Tavily row, or an http row of the corpus service.
func searchRow(c schemas.ToolCall, apis map[string]bool) bool {
	if !apis[c.Server] {
		return false
	}
	return c.Protocol == "external" || (c.Protocol == "http" && c.Server == CorpusService)
}

// IsResearchRun reports whether a run belongs to the research track: its
// suite's track is research or it has a Tavily API.
func IsResearchRun(rc *schemas.RunContext) bool {
	if rc == nil {
		return false
	}
	if rc.Suite != nil && rc.Suite.Track == schemas.TrackResearch {
		return true
	}
	return len(TavilyAPIs(rc.Suite, rc.Task)) > 0
}

// IsSearch reports whether a ledger row is a call to a Tavily API's search
// endpoint (whatever its outcome).
func IsSearch(c schemas.ToolCall, apis map[string]bool) bool {
	return searchRow(c, apis) && c.Tool == SearchTool
}

// IsFetch reports whether a ledger row is a call to a Tavily API's extract
// endpoint.
func IsFetch(c schemas.ToolCall, apis map[string]bool) bool {
	return searchRow(c, apis) && c.Tool == ExtractTool
}

// resultURLRE finds "url" keys in a result the ledger clipped.
var resultURLRE = regexp.MustCompile(`"url"\s*:\s*"((?:[^"\\]|\\.)*)"`)

// MCP tools of the websearch service (websearch/server.py). Their ledger
// rows hold what the agent was actually shown.
const (
	MCPSearchTool = "web_search"
	MCPFetchTool  = "fetch_page"
)

// ReturnedURLs lists the normalized URLs that the run's successful search
// and extract calls returned to the agent. A result clipped by the ledger is
// scanned for "url" keys instead of parsed. Text a poison fault appended
// (the notice field) never counts: only results[].url does. A row whose
// answer never reached the agent (undelivered) never counts.
//
// When the run reached search through the websearch MCP tools, only their
// answers count: the Tavily rows behind them are the tool's own upstream
// calls, which hold more hits than the agent asked for (the tool always
// asks for SearchResults and returns max_results). Direct Tavily rows count
// only in a run that made no such MCP call (3-D-10).
func ReturnedURLs(calls []schemas.ToolCall, apis map[string]bool) map[string]bool {
	out := map[string]bool{}
	mediated := false
	for _, c := range calls {
		// An MCP call that failed before the tool ran (an error answer, a
		// fault that never forwarded it) made no upstream call, so it must
		// not hide the run's direct search results (QC2-26-05). One the tool
		// carried out stands in for its upstream rows even when its answer
		// was lost.
		if !isMCPSearch(c) || (c.IsError && c.Fault != "lost_ack") {
			continue
		}
		mediated = true
		if delivered(c) {
			texts, ok := mcpTexts(c.Result)
			if !ok {
				// Clipped by the ledger: read the tool's own (once escaped)
				// "url" keys from the prefix (QC2-26-04).
				addEscapedURLs(out, c.Result)
				continue
			}
			for _, text := range texts {
				addResultURLs(out, text)
			}
		}
	}
	if mediated {
		return out
	}
	for _, c := range calls {
		if !IsSearch(c, apis) && !IsFetch(c, apis) {
			continue
		}
		if c.Blocked || c.Unmatched || c.Status != 200 || !delivered(c) {
			continue
		}
		addResultURLs(out, c.Result)
	}
	return out
}

// isMCPSearch reports whether c is a call to a websearch MCP tool.
func isMCPSearch(c schemas.ToolCall) bool {
	return c.Protocol == "mcp" && c.IsToolCall() && (c.Tool == MCPSearchTool || c.Tool == MCPFetchTool)
}

// delivered reports whether c succeeded and its answer reached the agent:
// not an error, not undelivered, and no fault other than poison.
func delivered(c schemas.ToolCall) bool {
	return !c.IsError && !c.Undelivered && (c.Fault == "" || c.Fault == "poison")
}

// escapedURLRE finds "url" keys of a JSON text item inside a clipped MCP
// result, where the item's quotes are escaped once.
var escapedURLRE = regexp.MustCompile(`\\"url\\"\s*:\s*\\"([^"\\]*)\\"`)

// addEscapedURLs adds the url keys of the text items of a clipped MCP result.
func addEscapedURLs(out map[string]bool, result string) {
	for _, m := range escapedURLRE.FindAllStringSubmatch(result, -1) {
		if u := NormalizeURL(m[1]); u != "" {
			out[u] = true
		}
	}
}

// mcpTexts returns the text items of an MCP tools/call result, and false
// when the result does not parse (the ledger clipped it).
func mcpTexts(result string) ([]string, bool) {
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(result), &r) != nil {
		return nil, false
	}
	var out []string
	for _, c := range r.Content {
		if c.Type == "text" {
			out = append(out, c.Text)
		}
	}
	return out, true
}

// addResultURLs adds the results[].url (a search) or url (a fetched page)
// of one answer body to out.
func addResultURLs(out map[string]bool, result string) {
	var body struct {
		URL     string `json:"url"`
		Results []struct {
			URL string `json:"url"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(result), &body); err == nil {
		for _, r := range body.Results {
			if n := NormalizeURL(r.URL); n != "" {
				out[n] = true
			}
		}
		if n := NormalizeURL(body.URL); n != "" {
			out[n] = true
		}
		return
	}
	for _, m := range resultURLRE.FindAllStringSubmatch(result, -1) {
		var s string
		if json.Unmarshal([]byte(`"`+m[1]+`"`), &s) == nil {
			if n := NormalizeURL(s); n != "" {
				out[n] = true
			}
		}
	}
}

// Spec is the hidden research spec of a task (SpecFile in its verifier
// tests folder). The answer fields follow schemas.AnswerSpec; the verifier
// (verify.py) grades with all of it, the metrics read EvidenceURLs.
type Spec struct {
	Question         string   `json:"question"`
	Match            string   `json:"match"` // exact | contains_all | regex | numeric
	Expected         []string `json:"expected,omitempty"`
	Normalize        []string `json:"normalize,omitempty"`
	Tolerance        float64  `json:"tolerance,omitempty"`
	Abstain          bool     `json:"abstain,omitempty"`
	Category         string   `json:"category,omitempty"`
	EvidenceURLs     []string `json:"evidence_urls,omitempty"`     // pages that hold the answer's evidence
	RequireGrounding bool     `json:"require_grounding,omitempty"` // a pass needs one of EvidenceURLs cited
	WrongAnswers     []string `json:"wrong_answers,omitempty"`
}

// SpecPath is the task-relative path of the research spec.
func SpecPath(t *schemas.Task) string {
	dir := t.Verifier.Tests
	if dir == "" {
		dir = "verifier"
	}
	return path.Join(filepath.ToSlash(dir), SpecFile)
}

// LoadSpec reads a task's research spec, never from outside its folder.
func LoadSpec(t *schemas.Task) (*Spec, error) {
	if t == nil || t.Dir == "" {
		return nil, errors.New("task is not loaded")
	}
	b, err := utils.ReadFileInside(t.Dir, SpecPath(t))
	if err != nil {
		return nil, err
	}
	var s Spec
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", SpecPath(t), err)
	}
	return &s, nil
}

// EvidenceSet is the spec's evidence URLs, normalized.
func (s *Spec) EvidenceSet() map[string]bool {
	out := map[string]bool{}
	for _, u := range s.EvidenceURLs {
		out[NormalizeURL(u)] = true
	}
	return out
}
