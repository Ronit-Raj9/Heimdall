// Package standin is a Tavily-compatible stand-in server over a fixed,
// hand-written corpus (docs/research-tasks.md). It answers POST /search and
// POST /extract in the response shape of Tavily's API as documented at
// docs.tavily.com on 2026-11-18, ranks pages with a deterministic BM25 score,
// and reaches no network. It exists so research tasks can be recorded and
// replayed with no Tavily key: cassettes recorded from it hold the corpus,
// not the live web.
//
// What it is not: it does not synthesize the include_answer answer (answer
// is null), ignores search_depth, topic, time and domain filters, and
// returns no images. Its scores and request ids are deterministic, so two
// recordings of the same calls are byte-identical.
package standin

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"unicode"

	"github.com/maximhq/bifrost/bench/research"
)

//go:embed corpus/*.json
var corpusFS embed.FS

// DefaultCorpus is the embedded corpus file of the acme-research tasks.
const DefaultCorpus = "corpus/calder-coast.json"

// Request limits, from Tavily's documentation: max_results 0 to 20 (default
// 10) and 1 to 20 URLs per extract.
const (
	defaultMaxResults = 10
	maxMaxResults     = 20
	maxExtractURLs    = 20
	maxRequestBytes   = 1 << 20
	snippetRunes      = 320
)

// Doc is one page of the corpus.
type Doc struct {
	URL           string `json:"url"`
	Title         string `json:"title"`
	PublishedDate string `json:"published_date"` // YYYY-MM-DD
	Content       string `json:"content"`
}

// Corpus is a loaded corpus with its search index.
type Corpus struct {
	Name  string
	Docs  []Doc
	byURL map[string]int
	terms []map[string]int // term counts per doc (title counted twice)
	lens  []int
	df    map[string]int
	avg   float64
}

// LoadDefault loads the embedded corpus.
func LoadDefault() (*Corpus, error) {
	b, err := corpusFS.ReadFile(DefaultCorpus)
	if err != nil {
		return nil, fmt.Errorf("read embedded corpus: %w", err)
	}
	return ParseCorpus(b)
}

// ParseCorpus reads a corpus file: {"name": ..., "docs": [Doc, ...]}. Every
// doc needs an http(s) URL (unique after research.NormalizeURL), a title, a
// date and content.
func ParseCorpus(b []byte) (*Corpus, error) {
	var f struct {
		Name string `json:"name"`
		Docs []Doc  `json:"docs"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse corpus: %w", err)
	}
	if len(f.Docs) == 0 {
		return nil, errors.New("corpus has no docs")
	}
	c := &Corpus{Name: f.Name, Docs: f.Docs, byURL: map[string]int{}, df: map[string]int{}}
	total := 0
	for i, d := range f.Docs {
		n := research.NormalizeURL(d.URL)
		switch {
		case !strings.HasPrefix(n, "http://") && !strings.HasPrefix(n, "https://"):
			return nil, fmt.Errorf("doc %d: url %q is not http(s)", i+1, d.URL)
		case d.Title == "" || d.Content == "" || len(d.PublishedDate) != len("2006-01-02"):
			return nil, fmt.Errorf("doc %s: needs a title, content and a YYYY-MM-DD published_date", d.URL)
		}
		if _, dup := c.byURL[n]; dup {
			return nil, fmt.Errorf("doc %s: url used twice", d.URL)
		}
		c.byURL[n] = i
		counts := map[string]int{}
		toks := append(append(Tokens(d.Title), Tokens(d.Title)...), Tokens(d.Content)...)
		for _, t := range toks {
			counts[t]++
		}
		for t := range counts {
			c.df[t]++
		}
		c.terms = append(c.terms, counts)
		c.lens = append(c.lens, len(toks))
		total += len(toks)
	}
	c.avg = float64(total) / float64(len(f.Docs))
	return c, nil
}

// URLs returns every doc URL, sorted.
func (c *Corpus) URLs() []string {
	out := make([]string, 0, len(c.Docs))
	for _, d := range c.Docs {
		out = append(out, d.URL)
	}
	sort.Strings(out)
	return out
}

// Lookup returns the doc at a URL (compared normalized).
func (c *Corpus) Lookup(rawURL string) (Doc, bool) {
	i, ok := c.byURL[research.NormalizeURL(rawURL)]
	if !ok {
		return Doc{}, false
	}
	return c.Docs[i], true
}

// stopwords are left out of the index and of queries.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields("a an and are as at be by did do does for from had has have how in is it its of on or that the their this to was were what when where which who whom why with after before since today current currently now many much long") {
		stopwords[w] = true
	}
}

// Tokens splits text into lower-case letter and digit runs, drops stop
// words and one-letter tokens, and joins thousands separators ("48,310" is
// one token, 48310). It is pure.
func Tokens(text string) []string {
	text = strings.ToLower(text)
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) > 1 {
			if w := string(cur); !stopwords[w] {
				out = append(out, w)
			}
		}
		cur = cur[:0]
	}
	rs := []rune(text)
	for i, r := range rs {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			cur = append(cur, r)
		case r == ',' && len(cur) > 0 && unicode.IsDigit(cur[len(cur)-1]) && i+3 < len(rs) && allDigits(rs[i+1:i+4]) && (i+4 == len(rs) || !unicode.IsDigit(rs[i+4])):
			// a thousands separator: keep the number whole
		default:
			flush()
		}
	}
	flush()
	return out
}

func allDigits(rs []rune) bool {
	for _, r := range rs {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return len(rs) > 0
}

// Hit is one ranked doc.
type Hit struct {
	Doc   Doc
	Score float64 // in (0, 1), rounded to 4 places
}

// Search ranks the docs for query with BM25 (k1 1.2, b 0.75; the title
// counts twice) and returns at most n docs that share a term with it, best
// first, ties broken by URL. The score shown is bm25/(bm25+5), rounded to 4
// places, so it lies in (0, 1) like Tavily's. It is deterministic.
func (c *Corpus) Search(query string, n int) []Hit {
	q := map[string]bool{}
	for _, t := range Tokens(query) {
		q[t] = true
	}
	const k1, b = 1.2, 0.75
	N := float64(len(c.Docs))
	var hits []Hit
	for i, d := range c.Docs {
		s := 0.0
		for t := range q {
			tf := float64(c.terms[i][t])
			if tf == 0 {
				continue
			}
			df := float64(c.df[t])
			idf := math.Log(1 + (N-df+0.5)/(df+0.5))
			s += idf * tf * (k1 + 1) / (tf + k1*(1-b+b*float64(c.lens[i])/c.avg))
		}
		if s > 0 {
			hits = append(hits, Hit{Doc: d, Score: math.Round(s/(s+5)*1e4) / 1e4})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Doc.URL < hits[j].Doc.URL
	})
	if n < len(hits) {
		hits = hits[:n]
	}
	return hits
}

// Snippet is the search result content of a doc: its text up to the last
// sentence end within snippetRunes runes (the whole text when shorter).
func Snippet(content string) string {
	rs := []rune(content)
	if len(rs) <= snippetRunes {
		return content
	}
	cut := string(rs[:snippetRunes])
	if i := strings.LastIndex(cut, ". "); i > 0 {
		return cut[:i+1]
	}
	return cut
}

// Server serves the Tavily API shape over a corpus.
type Server struct {
	corpus *Corpus
	key    string
}

// New returns a server. When key is not empty every call must carry
// "Authorization: Bearer <key>", as Tavily requires; a wrong or missing key
// gets 401.
func New(c *Corpus, key string) *Server { return &Server{corpus: c, key: key} }

// ServeHTTP implements http.Handler: POST /search, POST /extract and GET
// /healthz.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/healthz":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "corpus": s.corpus.Name, "docs": len(s.corpus.Docs)})
		return
	case "/search", "/extract":
	default:
		writeError(w, http.StatusNotFound, "not found: "+r.URL.Path)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if s.key != "" && r.Header.Get("Authorization") != "Bearer "+s.key {
		writeError(w, http.StatusUnauthorized, "Unauthorized: missing or invalid API key.")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil || len(raw) > maxRequestBytes {
		writeError(w, http.StatusBadRequest, "request body missing or too large")
		return
	}
	var req map[string]json.RawMessage
	if err := json.Unmarshal(raw, &req); err != nil {
		writeError(w, http.StatusBadRequest, "request body is not a JSON object")
		return
	}
	id := requestID(r.URL.Path, raw)
	if r.URL.Path == "/search" {
		s.search(w, req, id)
		return
	}
	s.extract(w, req, id)
}

// searchResult is one result of /search, in Tavily's field names.
type searchResult struct {
	URL           string  `json:"url"`
	Title         string  `json:"title"`
	Content       string  `json:"content"`
	Score         float64 `json:"score"`
	RawContent    *string `json:"raw_content,omitempty"`
	PublishedDate string  `json:"published_date,omitempty"`
}

func (s *Server) search(w http.ResponseWriter, req map[string]json.RawMessage, id string) {
	var query string
	if err := json.Unmarshal(req["query"], &query); err != nil || strings.TrimSpace(query) == "" {
		writeError(w, http.StatusBadRequest, "query is required and must be a non-empty string")
		return
	}
	n := defaultMaxResults
	if v, ok := req["max_results"]; ok {
		if err := json.Unmarshal(v, &n); err != nil || n < 0 || n > maxMaxResults {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("max_results must be an integer from 0 to %d", maxMaxResults))
			return
		}
	}
	dates, raw := truthy(req["include_published_date"]), truthy(req["include_raw_content"])
	results := []searchResult{}
	for _, h := range s.corpus.Search(query, n) {
		res := searchResult{URL: h.Doc.URL, Title: h.Doc.Title, Content: Snippet(h.Doc.Content), Score: h.Score}
		if raw {
			full := markdown(h.Doc)
			res.RawContent = &full
		}
		if dates {
			res.PublishedDate = h.Doc.PublishedDate
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, struct {
		Query        string         `json:"query"`
		Answer       *string        `json:"answer"`
		Images       []string       `json:"images"`
		Results      []searchResult `json:"results"`
		ResponseTime float64        `json:"response_time"`
		RequestID    string         `json:"request_id"`
	}{query, nil, []string{}, results, 0.01, id})
}

// extractResult is one result of /extract.
type extractResult struct {
	URL           string `json:"url"`
	RawContent    string `json:"raw_content"`
	PublishedDate string `json:"published_date,omitempty"`
}

// failedResult is one URL /extract could not read.
type failedResult struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

func (s *Server) extract(w http.ResponseWriter, req map[string]json.RawMessage, id string) {
	var urls []string
	if err := json.Unmarshal(req["urls"], &urls); err != nil {
		var one string
		if err := json.Unmarshal(req["urls"], &one); err != nil || one == "" {
			writeError(w, http.StatusBadRequest, "urls is required: a URL or a list of 1 to 20 URLs")
			return
		}
		urls = []string{one}
	}
	if len(urls) == 0 || len(urls) > maxExtractURLs {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("urls must hold 1 to %d URLs", maxExtractURLs))
		return
	}
	dates := truthy(req["include_published_date"])
	results, failed := []extractResult{}, []failedResult{}
	for _, u := range urls {
		d, ok := s.corpus.Lookup(u)
		if !ok {
			failed = append(failed, failedResult{URL: u, Error: "page not in the stand-in corpus"})
			continue
		}
		res := extractResult{URL: d.URL, RawContent: markdown(d)}
		if dates {
			res.PublishedDate = d.PublishedDate
		}
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, struct {
		Results       []extractResult `json:"results"`
		FailedResults []failedResult  `json:"failed_results"`
		ResponseTime  float64         `json:"response_time"`
		RequestID     string          `json:"request_id"`
	}{results, failed, 0.01, id})
}

// markdown is a doc's full text as extract returns it (format markdown).
func markdown(d Doc) string {
	return "# " + d.Title + "\n\nPublished " + d.PublishedDate + "\n\n" + d.Content
}

// truthy reads a boolean-or-string option: true, or a non-empty string
// other than "false".
func truthy(v json.RawMessage) bool {
	var b bool
	if json.Unmarshal(v, &b) == nil {
		return b
	}
	var s string
	return json.Unmarshal(v, &s) == nil && s != "" && s != "false"
}

// requestID is deterministic: the path and body's sha256, shortened.
func requestID(path string, body []byte) string {
	sum := sha256.Sum256(append([]byte(path+"\n"), body...))
	return "standin-" + hex.EncodeToString(sum[:8])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"detail":{"error":"encode response"}}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// writeError answers in Tavily's error shape, {"detail": {"error": msg}}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"detail": map[string]string{"error": msg}})
}
